package gristload

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/qload"
	"github.com/chucklehead-dev/gotogether/qmeta"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Load is what a deployment must survive (a declared rate and burst),
// and the policies in force that sizing judges beside its own.
type Load struct {
	// Rate is the new client calls per second, all tenants (-rate 40/s).
	Rate float64
	// Burst is the stressor: Factor times Rate for For (-burst 3x30s).
	Burst Burst
	// Within bounds recovery after the burst (default 60 s); Prob is the
	// probability of recovering within it a setting must reach (default
	// 0.99).
	Within time.Duration
	Prob   float64
	// HotShare is the busiest tenant's share of Rate (0: the recording's,
	// Measured.HotShare).
	HotShare float64
	// Concurrency is the requests one pod serves at once (default 10);
	// MaxReplicas the most replicas considered (default 8); Zones the
	// zones the replicas spread over, one of which may be lost (default
	// 3).
	Concurrency, MaxReplicas, Zones int
	// Timeout is the per-try timeout in force: the client's per-attempt
	// timeout, a Gateway's per-try timeout (default 2 s, gristsim's
	// CallTimeout).
	Timeout time.Duration
	// Client is grist/client's retry settings in force (MaxAttempts,
	// RetryDelay, MaxRetryDelay, Budget; nil: its defaults).
	Client *client.Client
	// Gateway is the tries a Gateway (or mesh) makes per client attempt,
	// on commands (POST) and on queries (GET): 0 is 1, no retry.
	Gateway Gateway
	// Slow is how many times slower a dependency gets in the processors'
	// rows' stressor, for the burst's duration (default 10: an incident's
	// timeouts, not a busy afternoon).
	Slow float64
	// CPU is the CPU one request costs, from a real tier's profile (go
	// test -cpuprofile; 0: the CPU row is not sized). Heap is the peak
	// heap of the largest tenant's read models at the horizon's log, in
	// bytes (0: the memory row is not sized).
	CPU  time.Duration
	Heap int64
	// EndpointLag is how long endpoints keep routing to a terminating pod
	// (default grist.DefaultDrainDelay); MaxConnections the store's
	// connection limit, Reserve the connections kept for others (0: the
	// pool's limit is not checked).
	EndpointLag             time.Duration
	MaxConnections, Reserve int
	// Seeds is how many seeds each simulated confirmation runs (default
	// 6); negative: none (the chain and the arithmetic alone, each row
	// saying so).
	Seeds int
	// Calibrate fits the chain's timeout to simulated trajectories (λ
	// held) before TimeoutFor's ladder.
	Calibrate bool
	// MaxStates bounds a chain's states (default 8000): the chain counts
	// requests in units (qmeta.Params.Unit) to stay within it.
	MaxStates int
}

// Burst is a declared burst: Factor times the rate for For.
type Burst struct {
	Factor float64
	For    time.Duration
}

func (b Burst) String() string { return fmt.Sprintf("%gx%v", b.Factor, b.For) }

// Gateway is a proxy's tries per client attempt, by method.
type Gateway struct{ Commands, Queries int }

func (l Load) withDefaults(m *Measured) Load {
	if l.Within <= 0 {
		l.Within = time.Minute
	}
	if l.Prob <= 0 {
		l.Prob = 0.99
	}
	if l.HotShare <= 0 {
		l.HotShare = m.HotShare
	}
	if l.Concurrency <= 0 {
		l.Concurrency = 10
	}
	if l.MaxReplicas <= 0 {
		l.MaxReplicas = 8
	}
	if l.Zones <= 0 {
		l.Zones = 3
	}
	if l.Timeout <= 0 {
		l.Timeout = 2 * time.Second
	}
	if l.Client == nil {
		l.Client = &client.Client{}
	}
	l.Gateway.Commands, l.Gateway.Queries = max(l.Gateway.Commands, 1), max(l.Gateway.Queries, 1)
	if l.Slow <= 0 {
		l.Slow = 10
	}
	if l.EndpointLag <= 0 {
		l.EndpointLag = grist.DefaultDrainDelay
	}
	if l.Seeds == 0 {
		l.Seeds = 6
	}
	if l.MaxStates <= 0 {
		l.MaxStates = 8000
	}
	return l
}

// A Setting is one recommended value with what it comes from: never a
// bare number (docs/grist-platform.md §8.5).
type Setting struct {
	// Key is the setting's place in sizing.yaml ("gateway.retries.POST").
	Key   string
	Value any
	// Because says why, with the numbers; Formula is the rule that gave
	// the value (§8.3's criterion).
	Because, Formula string
	// Tool is what produced it ("chain", "chain + qload (6 of 6 seeds)",
	// "arithmetic", "measurement").
	Tool string
	// Margin is the nearest failing setting or the smallest failing
	// stressor; Caveat what the tool cannot see; Evidence a file beside
	// sizing.yaml.
	Margin, Caveat, Evidence string
	// InForce is the verdict on the setting in force, where the Load
	// names one ("advised against: gateway 3 × client 3 recovers with
	// ..."), "" where it names none.
	InForce string
}

// Sizing is Report's answer.
type Sizing struct {
	App      string
	Load     Load
	Measured *Measured
	Settings []Setting
	// Findings are what the rows make visible, in a sentence each.
	Findings []string
	// Evidence is the evidence's files by name (SVG fields), which Write
	// puts beside sizing.yaml.
	Evidence map[string][]byte
	// Retries is the retry settings ranked on the command path, the one
	// in force among them: the chain's recovery of each, and the seeds
	// that recovered of those simulated.
	Retries []Ranked
	// Recommended is the command path's recommended setting: a Gateway's
	// tries per client attempt and the per-try timeout (0: none
	// recovers).
	Recommended Recommended

	z      *sizer
	tenant hop
}

// Ranked is one retry setting of the command path, as Report ranked it.
type Ranked struct {
	// Gateway is the proxy's tries per client attempt, Attempts the
	// client's MaxAttempts.
	Gateway, Attempts int
	// Chain is its recovery after the burst by the chain
	// (qmeta.RecoveryAfter); Seeds how many of the Load's seeds
	// recovered in simulation, -1 if it was not simulated.
	Chain float64
	Seeds int
}

// Recommended is a sizing's setting of the command path.
type Recommended struct {
	Gateway int
	Timeout time.Duration
}

// Commands is the command path's bottleneck (the busiest tenant's
// commits, one at a time, at the measured service) under gateway tries
// per client attempt, grist/client's retry settings c (nil: the Load's)
// and a per-try timeout, as a qload target: what Report's
// confirmations run, for a test to state its own claim over seeds
// (qload.Runs, Recovery.Expect) with Trigger.
func (s *Sizing) Commands(gateway int, c *client.Client, timeout time.Duration) qload.Target {
	if c == nil {
		c = s.Load.Client
	}
	return qload.OfSimulation(s.z.sim(s.tenant, retry{gateway: max(gateway, 1), c: c}, timeout), s.tenant.lambda, s.Load.Within)
}

// Trigger is the declared burst at the command path's bottleneck.
func (s *Sizing) Trigger() qload.Trigger { return s.z.trigger(s.tenant.lambda) }

// Get is the setting of key, nil if none.
func (s *Sizing) Get(key string) *Setting {
	for i := range s.Settings {
		if s.Settings[i].Key == key {
			return &s.Settings[i]
		}
	}
	return nil
}

// hop is one server the chain abstracts: a tenant's commits, the API's
// pods.
type hop struct {
	name    string
	lambda  float64
	svc     qmeta.Service
	workers int     // servers of the hop
	mu      float64 // each worker's rate
}

// retry is a setting of the retry layers: tries per client attempt at
// the proxy, and the client's policy.
type retry struct {
	gateway int
	c       *client.Client
}

func (r retry) String() string {
	return fmt.Sprintf("gateway %d × client %d", r.gateway, attempts(r.c))
}

func attempts(c *client.Client) int {
	if c.MaxAttempts > 0 {
		return c.MaxAttempts
	}
	return client.DefaultMaxAttempts
}

// budget is c's budget's ratio and its most tokens (grist/client's
// defaults without one).
func budget(c *client.Client) (ratio, most float64) {
	if c.Budget != nil {
		return c.Budget.Ratio, c.Budget.Max
	}
	b := client.DefaultBudget()
	return b.Ratio, b.Max
}

// meanBackoff is the client's mean wait over its first retries (at most
// five: a storm's requests rarely get more under a budget), equal
// jitter's mean three quarters of each step.
func meanBackoff(c *client.Client) float64 {
	d, top := c.RetryDelay, c.MaxRetryDelay
	if d <= 0 {
		d = client.DefaultRetryDelay
	}
	if top <= 0 {
		top = client.DefaultMaxRetryDelay
	}
	n := min(attempts(c)-1, 5)
	if n <= 0 {
		return 0
	}
	sum := 0.0
	for i := 1; i <= n; i++ {
		sum += 0.75 * min(d<<(i-1), top).Seconds()
	}
	return sum / float64(n)
}

// sizer holds one Report's inputs and the chain's unit per hop.
type sizer struct {
	load  Load
	m     *Measured
	burst func(rate float64) qmeta.Burst
	unit  map[string]int
	out   *Sizing

	rec           retry // the recommended retry setting on commands
	timeoutValue  time.Duration
	replicasValue int
}

// params is the chain of hop h under retry setting r and per-try
// timeout t, in requests (its Unit set for the hop): the proxy's tries
// multiply the attempts (Retries), the client's budget caps its own
// retries but not the proxy's (RetryBudget: (G-1) + G × ratio retries per
// request), and the client's backoff is a share 1/G of the retries'.
func (z *sizer) params(h hop, r retry, t time.Duration) qmeta.Params {
	g := r.gateway
	ratio, _ := budget(r.c)
	capacity := float64(h.workers) * h.mu
	queue := max(20, int(math.Ceil(3*capacity*t.Seconds())))
	peak := h.lambda * z.load.Burst.Factor
	orbit := max(2*queue, int(math.Ceil(2*peak*float64(g)*t.Seconds())))
	p := qmeta.Params{Lambda: h.lambda, Mu: h.mu, Workers: h.workers, Timeout: t.Seconds(),
		Retries: g*attempts(r.c) - 1, Queue: queue, Orbit: orbit,
		Backoff: meanBackoff(r.c) / float64(g), RetryBudget: float64(g-1) + float64(g)*ratio}
	p.Unit = z.unitFor(h.name, t, queue, max(2*queue, int(math.Ceil(2*peak*3*t.Seconds()))))
	return p
}

// unitFor is the hop's unit at per-try timeout t: the fewest requests
// per unit that keep the chain within MaxStates for the queue and the
// largest orbit (three proxy tries) of any setting at t, so the settings
// of a hop at one timeout are ranked on one state space.
func (z *sizer) unitFor(name string, t time.Duration, queue, orbit int) int {
	key := fmt.Sprintf("%s@%v", name, t)
	if u, ok := z.unit[key]; ok {
		return u
	}
	q, o := float64(queue), float64(orbit)
	u := 1
	for (q/float64(u)+1)*(o/float64(u)+1) > float64(z.load.MaxStates) {
		u++
	}
	z.unit[key] = u
	return u
}

// healthy is the hop's healthy set in its chain's units: no more queued
// than the hop serves in half the timeout (no attempt times out).
func (z *sizer) healthy(p qmeta.Params) func(qmeta.State) bool {
	maxU := int(math.Ceil(float64(p.Workers)*p.Mu*p.Timeout/2)) / max(p.Unit, 1)
	return qmeta.Healthy{MaxU: max(maxU, 1)}.Contains
}

func (z *sizer) recovery(h hop, r retry, t time.Duration) float64 {
	if z.idle(h) {
		return 1
	}
	p := z.params(h, r, t)
	return qmeta.RecoveryAfter(p, z.burst(h.lambda), z.healthy(p), z.load.Within.Seconds())
}

// idle reports whether the burst's peak at h is below half its
// capacity: no queue builds for a timeout to tip, and its chain (a large
// one, for a fast hop) is not solved. A reason says so (idleNote).
func (z *sizer) idle(h hop) bool {
	return h.lambda*z.load.Burst.Factor < float64(h.workers)*h.mu/2
}

// idleNote is a reason's account of an idle hop.
func (z *sizer) idleNote(h hop) string {
	if !z.idle(h) {
		return ""
	}
	return fmt.Sprintf(" (the burst's peak %.3g/s is under half the capacity %.3g/s: no queue builds to tip, so the chain is not solved and recovery is 1)", h.lambda*z.load.Burst.Factor, float64(h.workers)*h.mu)
}

// sim is hop h under r and t as a qload simulation: its workers, the
// measured service times (Draw), no queue bound (an HTTP server's is
// its memory), grist/client's own retry decision behind the proxy's.
func (z *sizer) sim(h hop, r retry, t time.Duration) qload.SimConfig {
	tmpl := struct {
		MaxAttempts               int
		RetryDelay, MaxRetryDelay time.Duration
		Budget                    *client.Budget
	}{r.c.MaxAttempts, r.c.RetryDelay, r.c.MaxRetryDelay, r.c.Budget}
	return qload.SimConfig{
		Server: qload.ServerConfig{Name: h.name, Workers: h.workers, QueueCap: 1 << 20, Service: h.svc.Draw},
		Client: qload.ClientConfig{Name: "clients", Rate: qload.Constant(h.lambda), Timeout: t,
			Retries: r.gateway*attempts(r.c) - 1,
			Retrier: func(*rand.Rand) qload.Retrier {
				c := &client.Client{MaxAttempts: tmpl.MaxAttempts, RetryDelay: tmpl.RetryDelay, MaxRetryDelay: tmpl.MaxRetryDelay}
				if tmpl.Budget != nil {
					c.Budget = &client.Budget{Ratio: tmpl.Budget.Ratio, Max: tmpl.Budget.Max}
				}
				return qload.Layered(r.gateway, c.Policy())
			}},
	}
}

// trigger is the declared burst on top of rate lambda.
func (z *sizer) trigger(lambda float64) qload.Trigger {
	return qload.Trigger{Start: qload.Window, Duration: z.load.Burst.For, ExtraRate: lambda * (z.load.Burst.Factor - 1)}
}

// confirm runs the declared burst against h under r in simulation:
// how many of the seeds recover.
func (z *sizer) confirm(h hop, r retry, t time.Duration) int {
	return qload.Count(qload.OfSimulation(z.sim(h, r, t), h.lambda, z.load.Within), z.trigger(h.lambda), z.load.Seeds)
}

// margin is the smallest burst factor (to 0.1, up to 4 × the declared)
// whose burst most seeds of h under r do not recover from, 0 if none.
func (z *sizer) margin(h hop, r retry, t time.Duration) float64 {
	sys := qload.OfSimulation(z.sim(h, r, t), h.lambda, z.load.Within)
	breaks := func(f float64) bool {
		ok, _ := qload.Mostly(sys, qload.Trigger{Start: qload.Window, Duration: z.load.Burst.For, ExtraRate: h.lambda * (f - 1)}, z.load.Seeds)
		return !ok
	}
	lo, hi := z.load.Burst.Factor, 4*z.load.Burst.Factor
	if !breaks(hi) {
		return 0
	}
	for hi-lo > 0.1 {
		mid := (lo + hi) / 2
		if breaks(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// Report sizes app's deployment for load from the steps of a run of it
// (Measure): every row of docs/grist-platform.md §8.3 this slice sizes,
// each with its value, formula, tool, margin, caveat and evidence. The
// chain (qmeta) ranks the retry layers per hop and gives the timeout
// (TimeoutFor), the replicas (Size) and a hot tenant's limit (LimitFor);
// a simulation (qload, grist/client's retry decision behind the
// Gateway's, Layered) confirms the recommended setting and the one in
// force over Load.Seeds seeds and finds the recommended one's margin;
// the pool, the grace, CPU and memory are arithmetic on the
// measurements. It takes seconds to a minute: a chain per setting, a
// few simulations per confirmation.
func Report(app *grist.App, steps []qtrace.Step, load Load) (*Sizing, error) {
	m, err := Measure(app, steps)
	if err != nil {
		return nil, err
	}
	if load.Rate <= 0 || load.Burst.Factor <= 0 || load.Burst.For <= 0 {
		return nil, fmt.Errorf("gristload: a Load needs a rate and a burst (-rate 40/s -burst 3x30s): got rate %g, burst %v", load.Rate, load.Burst)
	}
	load = load.withDefaults(m)
	out := &Sizing{App: app.Name, Load: load, Measured: m, Evidence: map[string][]byte{}}
	z := &sizer{load: load, m: m, unit: map[string]int{}, out: out}
	z.burst = func(rate float64) qmeta.Burst {
		return qmeta.Burst{Rate: rate * load.Burst.Factor, Duration: load.Burst.For.Seconds()}
	}
	cmds, qrys := m.Of(Command), m.Of(Query)
	if cmds.Mean <= 0 {
		return nil, fmt.Errorf("gristload: no command calls measured in %d steps: the tenant hop needs a command's service time", len(steps))
	}
	share := func(k Kind) float64 {
		n := 0
		for _, b := range m.Blocks {
			if b.Kind == k {
				n += len(b.Service.Samples)
			}
		}
		return float64(n) / float64(m.Calls)
	}
	tenant := hop{name: "tenant " + m.Hot, lambda: load.Rate * load.HotShare * share(Command), svc: cmds, workers: 1, mu: cmds.Mu()}
	out.z, out.tenant = z, tenant
	z.retries(tenant, cmds)
	z.timeout(tenant, cmds)
	z.replicas(qrys, share(Query))
	z.tenantLimit(tenant)
	z.processors()
	z.arithmetic(cmds)
	z.growth()
	out.Recommended = Recommended{Gateway: z.rec.gateway, Timeout: z.timeoutValue}
	return out, nil
}

// tried is a retry setting's chain recovery and its simulated seeds.
type tried struct {
	r        retry
	chain    float64
	seeds    int // -1: not simulated
	recovers bool
}

// retries ranks the Gateway's tries on commands at the tenant hop (the
// command path's bottleneck: one tenant's commits one at a time),
// recommends the most tries that recover, and confirms it and the one in
// force in simulation.
func (z *sizer) retries(h hop, svc qmeta.Service) {
	l := z.load
	inForce := retry{gateway: l.Gateway.Commands, c: l.Client}
	var all []tried
	best := -1
	for g := 1; g <= 3; g++ {
		r := retry{gateway: g, c: l.Client}
		tr := tried{r: r, chain: z.recovery(h, r, l.Timeout), seeds: -1}
		tr.recovers = tr.chain >= l.Prob
		if tr.recovers {
			best = g - 1
		}
		all = append(all, tr)
	}
	if inForce.gateway > 3 {
		tr := tried{r: inForce, chain: z.recovery(h, inForce, l.Timeout), seeds: -1}
		all = append(all, tr)
	}
	rec := retry{gateway: 1, c: l.Client}
	if best >= 0 {
		rec = all[best].r
	}
	z.rec = rec
	tool := "chain"
	margin := ""
	if l.Seeds > 0 {
		for i := range all {
			if all[i].r.gateway == rec.gateway || all[i].r.gateway == inForce.gateway || all[i].r.gateway == rec.gateway+1 {
				all[i].seeds = z.confirm(h, all[i].r, l.Timeout)
			}
		}
		if f := z.margin(h, rec, l.Timeout); f > 0 {
			margin = fmt.Sprintf("its smallest failing burst %.1fx%v (+%.0f/s at the tenant), from %d seeds each", f, l.Burst.For, h.lambda*(f-1), l.Seeds)
		} else {
			margin = fmt.Sprintf("no burst up to %.0fx%v breaks it (%d seeds each)", 4*l.Burst.Factor, l.Burst.For, l.Seeds)
		}
		tool = fmt.Sprintf("chain + qload (%d seeds)", l.Seeds)
	}
	var because bytes.Buffer
	fmt.Fprintf(&because, "%s commits one command at a time (μ %.3g/s, measured mean %v) at %.3g/s; recovery within %v after a %v burst:", h.name, h.mu, round(svc.Mean), h.lambda, l.Within, l.Burst)
	for _, tr := range all {
		fmt.Fprintf(&because, " %s %s", tr.r, pr(tr.chain))
		if tr.seeds >= 0 {
			fmt.Fprintf(&because, " (simulated: %d of %d seeds recover)", tr.seeds, l.Seeds)
		}
		fmt.Fprint(&because, ";")
	}
	fmt.Fprintf(&because, " in force: %s", inForce)
	verdict := "kept: it is the recommended setting"
	if inForce.gateway != rec.gateway {
		verdict = fmt.Sprintf("advised against: %s", inForce)
		for _, tr := range all {
			if tr.r.gateway == inForce.gateway {
				verdict += fmt.Sprintf(" recovers with %s by the chain", pr(tr.chain))
				if tr.seeds >= 0 {
					verdict += fmt.Sprintf(", %d of %d seeds in simulation", tr.seeds, l.Seeds)
				}
			}
		}
	}
	fmt.Fprintf(&because, "; %s's own retries are budgeted (ratio %.2g), the Gateway's are not", "grist/client", first(budget(l.Client)))
	if best < 0 {
		fmt.Fprintf(&because, "; no Gateway retry count recovers at %.3g: lower the client's budget or the load", l.Prob)
	}
	ev := "sizing/tenant-retries.svg"
	z.svg(ev, z.params(h, rec, l.Timeout))
	z.out.Settings = append(z.out.Settings, Setting{
		Key: "gateway.retries.POST", Value: rec.gateway - 1,
		Because: because.String(),
		Formula: "the most proxy tries per client attempt (attempts multiply: gateway × client) whose recovery after the declared burst is at least " + fmt.Sprint(l.Prob),
		Tool:    tool, Margin: margin, InForce: verdict,
		Caveat:   "the chain caps the client's budget as a rate (ratio × λ), without its burst of Max tokens; the chain's service is exponential (measured CV " + fmt.Sprintf("%.2f", svc.CV) + ")",
		Evidence: ev,
	})
	z.out.Findings = append(z.out.Findings, fmt.Sprintf("POST retries at the Gateway multiply the client's (%s): the client's budget bounds only its own", inForce))
	for _, tr := range all {
		z.out.Retries = append(z.out.Retries, Ranked{Gateway: tr.r.gateway, Attempts: attempts(tr.r.c), Chain: tr.chain, Seeds: tr.seeds})
	}
}

func first(a, _ float64) float64 { return a }

// pr is a probability for a reason's text: three decimals, or its
// order of magnitude when below a thousandth.
func pr(p float64) string {
	if p > 0 && p < 0.001 {
		return fmt.Sprintf("%.1e", p)
	}
	return fmt.Sprintf("%.3f", p)
}

// round is d to a readable precision.
func round(d time.Duration) time.Duration {
	switch {
	case d >= time.Second:
		return d.Round(10 * time.Millisecond)
	case d >= time.Millisecond:
		return d.Round(100 * time.Microsecond)
	}
	return d.Round(time.Microsecond)
}
