package gristload

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qload"
	"github.com/chucklehead-dev/gotogether/qmeta"
)

// timeout is the per-try timeout at the tenant hop under the
// recommended retries (qmeta.TimeoutFor), calibrated on simulated
// trajectories with λ held when Load.Calibrate.
func (z *sizer) timeout(h hop, svc qmeta.Service) {
	l := z.load
	p := z.params(h, z.rec, l.Timeout)
	var data []qmeta.Trajectory
	tool := "chain"
	if l.Calibrate {
		cfg := z.sim(h, z.rec, l.Timeout)
		cfg.Server.QueueCap = p.Queue // the chain's bound, so their states compare
		for _, from := range []qmeta.State{{U: p.Queue / 2, V: p.Orbit / 8}, {U: p.Queue / 4}, {U: p.Queue, V: p.Orbit / 4}} {
			data = append(data, qload.SimulateTrajectory(cfg, from, []float64{5, 10, 20}, 10))
		}
		tool = "calibrated chain (CalibrateTimeout, λ held)"
	}
	t := qmeta.TimeoutFor(p, data, svc, z.burst(h.lambda), z.healthy(p), l.Within.Seconds(), l.Prob)
	value := round(time.Duration(t.Value * float64(time.Second)))
	z.timeoutValue = value
	margin := ""
	below := 0.0
	for c := range t.Recovery {
		if c < t.Value && c > below {
			below = c
		}
	}
	if below > 0 {
		margin = fmt.Sprintf("recovery %s at %s", pr(t.Recovery[below]), secs(below))
	} else if t.Value > 0 {
		half := p
		half.Timeout = t.Value / 2 * t.Scale
		margin = fmt.Sprintf("the floor itself: below it healthy requests time out (at half of it, %s, recovery %s)", secs(t.Value/2),
			pr(qmeta.RecoveryAfter(half, z.burst(h.lambda), z.healthy(half), l.Within.Seconds())))
	}
	if t.Value > 0 && l.Seeds > 0 {
		n := z.confirm(h, z.rec, value)
		tool += fmt.Sprintf(" + qload (%d of %d seeds recover at it)", n, l.Seeds)
	}
	caveat := "the chain's service is exponential: "
	if svc.Exponential() {
		caveat += fmt.Sprintf("the measured CV %.2f is near 1", svc.CV)
	} else {
		caveat += fmt.Sprintf("the measured CV %.2f is not, so the floor's wait is scaled by (1+CV²)/2 and the drain bound applies; the simulation draws the measured times", svc.CV)
	}
	var v any = value.String()
	if t.Value == 0 {
		v = "none recovers"
	}
	z.out.Settings = append(z.out.Settings, Setting{
		Key: "client.attemptTimeout", Value: v,
		Because: fmt.Sprintf("%s, under %s: %s", h.name, z.rec, t.Why),
		Formula: "the shortest timeout at or above p99.9 of service plus p99.9 of the wait at λ (and, for regular service, the burst's backlog drain) whose recovery is at least " + fmt.Sprint(l.Prob),
		Tool:    tool, Margin: margin, Caveat: caveat,
	})
}

// secs is s seconds as a duration's text.
func secs(s float64) string { return round(time.Duration(s * float64(time.Second))).String() }

// replicas sizes the API's pods (qmeta.Size, each pod Concurrency
// workers' worth) for the whole rate, adds a zone's loss, and ranks the
// Gateway's tries on queries there.
func (z *sizer) replicas(qrys qmeta.Service, queryShare float64) {
	l := z.load
	all := z.m.Of(Command, Query)
	t := z.timeoutValue
	if t <= 0 {
		t = l.Timeout
	}
	// a pod as one server of Concurrency × μ: slower than Concurrency
	// workers at a short queue, the same at a long one, where storms are
	// its bounds those of one pod: more pods drain a queue that long
	// faster, so the bound truncates nothing they would reach
	api := hop{name: "api", lambda: l.Rate, svc: all, workers: 1, mu: float64(l.Concurrency) * all.Mu()}
	p := z.params(api, z.rec, t)
	sized := qmeta.Sized{Workers: 1, Recovery: map[int]float64{1: 1}}
	if !z.idle(api) {
		sized = qmeta.Size(p, z.burst(api.lambda), z.healthyAt(p), l.Within.Seconds(), l.Prob, l.MaxReplicas)
	}
	n := 0
	if sized.Workers > 0 {
		n = sized.Workers
		for n-(n+l.Zones-1)/l.Zones < sized.Workers {
			n++
		}
	}
	var rs []string
	for w := 1; w <= l.MaxReplicas; w++ {
		if r, ok := sized.Recovery[w]; ok {
			rs = append(rs, fmt.Sprintf("%s at %d", pr(r), w))
		}
	}
	var v any = n
	margin := ""
	if sized.Workers > 1 {
		margin = fmt.Sprintf("%d pods recover with %s", sized.Workers-1, pr(sized.Recovery[sized.Workers-1]))
	} else if sized.Workers == 1 {
		margin = "one pod is enough for the load; the rest is the zone's loss"
	}
	if n == 0 {
		v = fmt.Sprintf("more than %d", l.MaxReplicas)
	}
	z.replicasValue = max(n, 1)
	z.out.Settings = append(z.out.Settings, Setting{
		Key: "replicas.min", Value: v,
		Because: fmt.Sprintf("the API at %.3g/s, %d requests at once per pod (μ %.3g/s per request, measured over commands and queries): Size gives %d recovering after a %v burst (recovery %s)%s, and %d spread over %d zones keep %d after losing one",
			l.Rate, l.Concurrency, all.Mu(), sized.Workers, l.Burst, strings.Join(rs, ", "), z.idleNote(api), n, l.Zones, sized.Workers),
		Formula: "the fewest pods n with n - ceil(n/zones) ≥ the pods whose recovery after the burst is at least " + fmt.Sprint(l.Prob),
		Tool:    "chain (qmeta.Size)", Margin: margin,
		Caveat: "replicas add API throughput, never a hot tenant's commit rate or a processor's rate on one tenant (tenant.rateLimit, processors); " +
			"several request classes share a pod's CPU, which the chain does not see",
	})
	// the Gateway's tries on queries, at the API
	if qrys.Mean > 0 {
		qh := hop{name: "api queries", lambda: l.Rate * queryShare, svc: qrys, workers: z.replicasValue, mu: float64(l.Concurrency) * qrys.Mu()}
		best := 0
		var parts []string
		for g := 1; g <= 3; g++ {
			r := z.recovery(qh, retry{gateway: g, c: l.Client}, t)
			parts = append(parts, fmt.Sprintf("%s %s", retry{gateway: g, c: l.Client}, pr(r)))
			if r >= l.Prob {
				best = g
			}
		}
		z.out.Settings = append(z.out.Settings, Setting{
			Key: "gateway.retries.GET", Value: max(best-1, 0),
			Because: fmt.Sprintf("queries at %.3g/s on %d pods (μ %.3g/s per query, mean %v): recovery after the burst %s%s; a query commits nothing, so a retry is safe and only its load counts",
				qh.lambda, z.replicasValue, qrys.Mu(), round(qrys.Mean), strings.Join(parts, ", "), z.idleNote(qh)),
			Formula: "the most proxy tries per client attempt whose recovery after the declared burst is at least " + fmt.Sprint(l.Prob),
			Tool:    "chain", Caveat: "queries' pods are the API's: commands share their CPU",
		})
	}
}

// healthyAt is healthy for a chain whose workers Size varies: the
// queue its fewest workers serve in half the timeout.
func (z *sizer) healthyAt(p qmeta.Params) func(qmeta.State) bool {
	one := p
	one.Workers = 1
	return z.healthy(one)
}

// tenantLimit is the busiest tenant's row: its commits are one at a time
// whatever the replicas (finding 1), so its answer is a rate limit.
func (z *sizer) tenantLimit(h hop) {
	l := z.load
	t := z.timeoutValue
	if t <= 0 {
		t = l.Timeout
	}
	inForce := retry{gateway: l.Gateway.Commands, c: l.Client}
	p := z.params(h, inForce, t)
	// what the hop would need, if its workers could be added
	need := qmeta.Size(p, z.burst(h.lambda), z.healthy(p), l.Within.Seconds(), l.Prob, 4)
	at := need.Recovery[1]
	rp := z.params(h, z.rec, t)
	limit := qmeta.LimitFor(rp, l.Burst.Factor, l.Burst.For.Seconds(), z.healthy(rp), l.Within.Seconds(), l.Prob, h.mu) // one worker: nothing above μ recovers
	needs := "one worker recovers under it"
	switch {
	case need.Workers == 0:
		needs = "no count of workers up to 4 would recover it"
	case need.Workers > 1:
		needs = fmt.Sprintf("it would need %d workers (qmeta.Size), which no replica count gives", need.Workers)
	}
	z.out.Settings = append(z.out.Settings, Setting{
		Key: "tenant.rateLimit", Value: fmt.Sprintf("%.3g/s", limit),
		Because: fmt.Sprintf("%s's commands commit one at a time (its head: workers 1 at any replica count) at μ %.3g/s; under %s its recovery after the burst is %s at 1 to %d replicas alike: %s; "+
			"under %s it recovers up to %.3g/s at the tenant (now %.3g/s): limit a tenant's commands there",
			h.name, h.mu, inForce, pr(at), l.MaxReplicas, needs, z.rec, limit, h.lambda),
		Formula: "the highest rate at one tenant whose recovery after a burst of the declared factor and duration is at least " + fmt.Sprint(l.Prob) + " (qmeta.LimitFor)",
		Tool:    "chain (qmeta.Size, qmeta.LimitFor)",
		Margin:  fmt.Sprintf("the tenant is at %.0f%% of its limit", 100*h.lambda/limit),
		Caveat:  "a tenant's commands serialize at its head (the commit's condition); on SQLite every tenant shares one writer, which makes the whole store this hop",
	})
	z.out.Findings = append(z.out.Findings, fmt.Sprintf("replicas never speed up a hot tenant: %s commits one at a time, recovery %s at 1 to %d replicas alike under %s (%s); a per-tenant limit (%.3g/s) does", h.name, pr(at), l.MaxReplicas, inForce, needs, limit))
}

// processors is each processor's row whose effect's calls are measured:
// one at a time per tenant, at the dependency's pace, so a lag it
// causes is not the pods' to clear (finding 2).
func (z *sizer) processors() {
	l := z.load
	lagged := false
	for _, p := range z.m.Processors {
		if len(p.Uses) == 0 || p.PerCall == 0 {
			continue
		}
		var mean time.Duration
		for _, u := range p.Uses {
			mean += z.m.Blocks[u].Service.Mean
		}
		mu := 1 / mean.Seconds()
		lambda := l.Rate * l.HotShare * p.PerCall
		d := l.Burst.For.Seconds()
		slow := mu / l.Slow
		// fluid: the backlog a burst of triggers, or the dependency slowed
		// for as long, leaves, and the time it takes to drain at μ
		backlog := math.Max(0, lambda*l.Burst.Factor-mu) * d
		slowLog := math.Max(0, lambda-slow) * d
		drain := func(n float64) string {
			if n == 0 {
				return "none"
			}
			if lambda >= mu {
				return fmt.Sprintf("%.0f events, never drained (λ ≥ μ)", n)
			}
			return fmt.Sprintf("%.0f events, drained in %s", n, secs(n/(mu-lambda)))
		}
		var b bytes.Buffer
		fmt.Fprintf(&b, "%s runs one effect at a time per tenant, each its call to %s (mean %v, μ %.3g/s), on %.3g triggers/s at %s (%s per call measured); ",
			p.Name, strings.Join(p.Uses, " and "), round(mean), mu, lambda, z.m.Hot, fmt.Sprintf("%.2g", p.PerCall))
		fmt.Fprintf(&b, "the %v burst leaves a lag of %s, the dependency %gx slower for %v leaves %s, at 1 to %d replicas alike: more pods poll the same tenant's checkpoint and wait on the same dependency",
			l.Burst, drain(backlog), l.Slow, l.Burst.For, drain(slowLog), l.MaxReplicas)
		z.out.Settings = append(z.out.Settings, Setting{
			Key: "processors." + p.Name + ".scaleOnLag", Value: false,
			Because: b.String(),
			Formula: "lag = max(0, λ·factor − μ)·duration, drained at μ − λ; μ = 1 / the effect's calls' mean, per tenant, whatever the pods",
			Tool:    "arithmetic (fluid)",
			Caveat:  "a closed loop over the dependency's own capacity, and a scaler's delay, are simulation's (slice 7: gristsim.Load, qload.Autoscaler); scale on the API's u (qrole), not on this lag",
		})
		if slowLog > 0 {
			z.out.Findings = append(z.out.Findings, fmt.Sprintf("scaling on a lag a slow dependency causes only adds waiting pods: %s runs one effect at a time per tenant at %s's pace, and %s %gx slower leaves %s at 1 to %d replicas alike",
				p.Name, strings.Join(p.Uses, " and "), strings.Join(p.Uses, " and "), l.Slow, drain(slowLog), l.MaxReplicas))
			lagged = true
		}
	}
	if !lagged && len(z.m.Processors) > 0 {
		z.out.Findings = append(z.out.Findings, "no processor's dependency, slowed by the stressor, leaves a lag here; were one to, more pods would not clear it: a processor runs one effect at a time per tenant")
	}
}

// arithmetic is the rows of sums and Little's law: the pool, the grace,
// CPU and memory.
func (z *sizer) arithmetic(cmds qmeta.Service) {
	l := z.load
	all := z.m.Of(Command, Query)
	replicas := max(z.replicasValue, 1)
	peak := l.Rate * l.Burst.Factor / float64(replicas)
	conns := peak * all.Mean.Seconds()
	pool := max(2, int(math.Ceil(conns*1.5)))
	pr := Setting{
		Key: "store.pool", Value: pool,
		Because: fmt.Sprintf("Little's law: %.3g/s per pod at the burst's peak (%.3g/s over %d pods) × %v mean per call = %.2g connections busy, plus half", peak, l.Rate*l.Burst.Factor, replicas, round(all.Mean), conns),
		Formula: "pool ≥ λ_peak / replicas × mean transaction time × 1.5",
		Tool:    "arithmetic",
		Caveat:  "a call's time bounds its transaction's from above; a recording with calls in flight together includes their waits",
	}
	if l.MaxConnections > 0 {
		most := l.MaxReplicas * pool
		ok := most <= l.MaxConnections-l.Reserve
		pr.Margin = fmt.Sprintf("%d pods × %d = %d against max_connections %d − reserve %d", l.MaxReplicas, pool, most, l.MaxConnections, l.Reserve)
		if !ok {
			pr.Margin += ": too many: lower the pool or MaxReplicas, or put a pooler in front"
		}
	}
	z.out.Settings = append(z.out.Settings, pr)

	delay := max(l.EndpointLag, grist.DefaultDrainDelay)
	request := all.Quantile(0.999)
	shutdown := max(10*time.Second, request)
	settle, flush, margin := 10*time.Second, 10*time.Second, 10*time.Second
	grace := delay + shutdown + settle + flush + margin
	z.out.Settings = append(z.out.Settings, Setting{
		Key: "terminationGracePeriodSeconds", Value: int(grace.Seconds()),
		Because: fmt.Sprintf("drain delay %v (endpoint lag %v) + shutdown %v (≥ p99.9 request %v) + settle %v + flush %v + margin %v", delay, l.EndpointLag, shutdown, round(request), settle, flush, margin),
		Formula: "grace ≥ drain delay + max(shutdown bound, p99.9 request) + settle + flush + margin (grist.Drain's phases)",
		Tool:    "sum",
		Caveat:  "the endpoint lag is the cluster's, measured once per cluster; -grace budgets a shorter one",
	})
	if l.CPU > 0 {
		cores := peak * l.CPU.Seconds()
		milli := int(math.Ceil(cores * 1.35 * 1000))
		z.out.Settings = append(z.out.Settings, Setting{
			Key: "resources.requests.cpu", Value: fmt.Sprintf("%dm", milli),
			Because: fmt.Sprintf("%.3g/s per pod at the peak × %v CPU per request = %.3g cores, + 35%% headroom", peak, l.CPU, cores),
			Formula: "λ_peak / replicas × CPU per request × 1.35",
			Tool:    "arithmetic", Caveat: "CPU per request from a real profile; no CPU limit by default (throttling stalls the pollers in 100 ms periods)",
		})
	}
	if l.Heap > 0 {
		limit := int64(math.Ceil(float64(l.Heap) * 1.25 / (1 << 20)))
		z.out.Settings = append(z.out.Settings, Setting{
			Key: "resources.limits.memory", Value: fmt.Sprintf("%dMi", limit),
			Because: fmt.Sprintf("peak heap %dMi × 1.25; GOMEMLIMIT is 90%% of it (grist.Main)", l.Heap>>20),
			Formula: "peak heap at the horizon's log × 1.25",
			Tool:    "measurement", Caveat: "no queueing model; read models folded from the log grow with it until snapshots",
		})
	}
}

// growth is a row per command whose service grows with the log.
func (z *sizer) growth() {
	for _, n := range z.m.Order() {
		b := z.m.Blocks[n]
		g := b.Growth
		if b.Kind != Command || g.Calls < 10 || g.Slope <= 0 || g.To <= g.From {
			continue
		}
		from, to := g.At(g.From), g.At(g.To)
		if to < 1.2*from {
			continue // under 20% over the recording's log: a point, not yet a line
		}
		z.out.Settings = append(z.out.Settings, Setting{
			Key: "growth." + b.Name, Value: fmt.Sprintf("+%s per 1000 events", secs(g.Slope*1000)),
			Because: fmt.Sprintf("%s's service grew from %s to %s as the log went from %d to %d events (%d calls, least squares): its decision folds the log", b.Name, secs(from), secs(to), g.From, g.To, g.Calls),
			Formula: "service(n) = intercept + slope × n events",
			Tool:    "measurement",
			Caveat:  "the rows above use the recording's mean: re-size at the horizon's log (a gristsim prologue, or a recording that long), or snapshot the read models",
		})
	}
}

// svg puts the chain's field for p in the evidence.
func (z *sizer) svg(name string, p qmeta.Params) {
	c := qmeta.New(p)
	var b bytes.Buffer
	if err := c.SVG(&b, max(1, c.N/12), max(1, c.V/12)); err == nil {
		z.out.Evidence[name] = b.Bytes()
	}
}
