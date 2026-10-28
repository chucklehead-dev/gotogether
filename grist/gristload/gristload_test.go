package gristload_test

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/gristload"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type (
	Place   struct{ Order string }
	Placed  struct{ Order string }
	Look    struct{ Order string }
	Seen    struct{ N int }
	Mail    struct{ To string }
	Mailed  struct{}
	Receipt struct{ Order string }
)

// shop is a one-command, one-query app whose processor mails each order
// through a declared call.
func shop() *grist.App {
	a := grist.New("shop")
	placed := grist.NewEvent[Placed](a, "placed")
	sent := grist.NewEvent[Receipt](a, "receipted")
	grist.NewCommand(a, "place", func(r *grist.Reads, c Place) ([]grist.Event, error) {
		return []grist.Event{placed.Of(Placed(c))}, nil
	})
	grist.NewQuery(a, "look", func(r *grist.Reads, q Look) (Seen, error) { return Seen{}, nil })
	mailer := grist.NewDependency(a, "mailer")
	send := grist.NewCall[Mail, Mailed](mailer, "send")
	grist.NewProcessor(a, "receipt", placed, func(ctx context.Context, r *grist.Reads, e Placed, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := send.Do(ctx, Mail{To: e.Order})
			return err
		}, grist.AtLeastOnce, grist.OnSuccess(sent.Of(Receipt(e))))
	}, grist.Delivers(grist.AtLeastOnce, sent), grist.Uses(send))
	return a
}

// recording is a run of shop as its steps record it: n calls one at a
// time, three in four on t1, nine in ten a place taking `place` plus
// growth per event of t1's log (exponential around it), the rest a look
// taking `look`; each place's commit at the store, and its receipt's
// mail taking `mail` at the node.
func recording(app *grist.App, n int, place, look, mail, growth time.Duration) []qtrace.Step {
	cat := app.Catalog()
	quint := map[string]string{}
	for _, c := range cat.Commands {
		quint[c.Name] = c.Quint
	}
	for _, q := range cat.Queries {
		quint[q.Name] = q.Quint
	}
	sendQ := cat.Dependencies[0].Calls[0].Quint
	rnd := rand.New(rand.NewPCG(1, 2))
	exp := func(d time.Duration) time.Duration { return time.Duration(rnd.ExpFloat64() * float64(d)) }
	now := time.Unix(1_800_000_000, 0)
	var steps []qtrace.Step
	seq := map[string]uint64{}
	next := func(rec string) uint64 { seq[rec]++; return seq[rec] }
	heads := map[string]int64{}
	for i := range n {
		tenant := "t1"
		if rnd.IntN(4) == 0 {
			tenant = "t2"
		}
		id := "c" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
		name, d := "look", exp(look)
		if rnd.IntN(10) < 9 {
			name, d = "place", exp(place+time.Duration(heads[tenant])*growth)
		}
		inv := next("clients")
		args := map[string]any{"id": id}
		steps = append(steps, qtrace.Step{Action: quint[name], Recorder: "clients", Seq: inv, Actor: tenant + "/clients", Invocation: true, Time: now, Args: args})
		end := now.Add(d)
		if name == "place" {
			heads[tenant]++
			steps = append(steps, qtrace.Step{Action: quint[name], Recorder: "grist/shop/" + tenant, Seq: next("grist/shop/" + tenant), Actor: tenant,
				Outcome: "ok", Time: end.Add(-time.Microsecond), Args: map[string]any{"id": id}, Obs: map[string]any{"head": heads[tenant], "events": `["placed"]`}})
			m := next("n1")
			steps = append(steps, qtrace.Step{Action: sendQ, Recorder: "n1", Seq: m, Actor: tenant, Invocation: true, Time: end})
			steps = append(steps, qtrace.Step{Action: sendQ, Recorder: "n1", Seq: next("n1"), Call: m, Actor: tenant, Outcome: "ok", Time: end.Add(exp(mail))})
		}
		steps = append(steps, qtrace.Step{Action: quint[name], Recorder: "clients", Seq: next("clients"), Call: inv, Actor: tenant + "/clients", Outcome: "ok", Time: end, Args: args})
		now = end.Add(time.Millisecond)
	}
	return steps
}

// Measure finds each block's service from its calls' invocations and
// answers, a command's growth with the log, the busiest tenant, one call
// at a time, and a processor's triggers per call.
func TestMeasure(t *testing.T) {
	app := shop()
	m, err := gristload.Measure(app, recording(app, 2000, 20*time.Millisecond, 5*time.Millisecond, 30*time.Millisecond, 10*time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	place, look, send := m.Blocks["place"], m.Blocks["look"], m.Blocks["mailer.send"]
	if place == nil || look == nil || send == nil {
		t.Fatalf("blocks %v: want place, look and mailer.send", m.Order())
	}
	// place's mean is 20 ms plus 10 µs per event over a log of 0..about
	// 1350 events: about 27 ms
	near := func(got, want time.Duration) bool {
		return math.Abs(got.Seconds()-want.Seconds()) < 0.12*want.Seconds()
	}
	if !near(look.Service.Mean, 5*time.Millisecond) || !near(send.Service.Mean, 30*time.Millisecond) || !look.Service.Exponential() {
		t.Errorf("look %v (CV %.2f), send %v: want about 5 ms (exponential) and 30 ms", look.Service.Mean, look.Service.CV, send.Service.Mean)
	}
	if g := place.Growth; math.Abs(g.Slope-10e-6) > 3e-6 || math.Abs(g.Intercept-0.020) > 0.004 || g.To < 1000 {
		t.Errorf("place's growth %+v: want 10 µs per event from 20 ms over about 1350 events", g)
	}
	if m.Hot != "t1" || math.Abs(m.HotShare-0.75) > 0.04 || m.InFlight != 1 || m.Calls != 2000 {
		t.Errorf("hot %s at %.2f, %d in flight, %d calls: want t1 at about 0.75, 1, 2000", m.Hot, m.HotShare, m.InFlight, m.Calls)
	}
	if len(m.Processors) != 1 || math.Abs(m.Processors[0].PerCall-0.9) > 0.03 || m.Processors[0].Uses[0] != "mailer.send" {
		t.Errorf("processors %+v: want receipt at about 0.9 triggers per call, using mailer.send", m.Processors)
	}
	if _, err := gristload.Measure(app, nil); err == nil || !strings.Contains(err.Error(), "no client calls") {
		t.Errorf("no steps: %v, want the error naming the clients' steps", err)
	}
}

// Report advises against Gateway retries that multiply the client's
// into a storm at the busiest tenant, recommends none, limits the
// tenant rather than adding replicas, finds a processor's lag no pod
// clears, and gives every value a reason and a tool; the YAML is
// sizing.yaml's shape.
func TestReport(t *testing.T) {
	app := shop()
	steps := recording(app, 1200, 50*time.Millisecond, 5*time.Millisecond, 30*time.Millisecond, 0)
	inForce := gristload.Load{Rate: 16, Burst: gristload.Burst{Factor: 3, For: 20 * time.Second}, Timeout: time.Second,
		Gateway: gristload.Gateway{Commands: 3}, Client: &client.Client{MaxAttempts: 3}, Seeds: -1}
	s, err := gristload.Report(app, steps, inForce)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + s.Text())
	post := s.Get("gateway.retries.POST")
	if post == nil || post.Value != 0 || !strings.HasPrefix(post.InForce, "advised against: gateway 3 × client 3") || s.Recommended.Gateway != 1 {
		t.Fatalf("POST retries %+v: want 0, the setting in force advised against", post)
	}
	limit := s.Get("tenant.rateLimit")
	if limit == nil || !strings.Contains(limit.Because, "at 1 to 8 replicas alike") || !strings.Contains(limit.Because, "workers (qmeta.Size), which no replica count gives") {
		t.Fatalf("tenant limit %+v: want replicas shown not to help, and the workers it would need", limit)
	}
	lag := s.Get("processors.receipt.scaleOnLag")
	if lag == nil || lag.Value != false || !strings.Contains(lag.Because, "10x slower") || strings.Contains(lag.Because, "slower for 20s leaves none") {
		t.Fatalf("processor %+v: want a lag the slowed dependency leaves, at every replica count", lag)
	}
	if len(s.Findings) != 3 {
		t.Errorf("findings %q: want the retries', the tenant's and the lag's", s.Findings)
	}
	for _, st := range s.Settings {
		if st.Because == "" || st.Tool == "" || st.Formula == "" {
			t.Errorf("%s: a value without its reason, tool or formula: %+v", st.Key, st)
		}
	}
	if g := s.Get("terminationGracePeriodSeconds"); g == nil || g.Value != 45 {
		t.Errorf("grace %+v: want 45 (5 + 10 + 10 + 10 + 10)", g)
	}
	b, err := s.YAML("shop sizing -rate 16/s -burst 3x20s")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Measured map[string]map[string]string
		Gateway  struct {
			Retries map[string]struct {
				Value   int
				Because string
				InForce string `yaml:"inForce"`
			}
		}
		Findings []string
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if doc.Gateway.Retries["POST"].Because == "" || doc.Gateway.Retries["POST"].InForce == "" || doc.Measured["place"]["kind"] != "command" || len(doc.Findings) != 3 {
		t.Fatalf("sizing.yaml lacks POST's because or inForce, the measured place, or the findings:\n%s", b)
	}
}

func TestParse(t *testing.T) {
	if r, err := gristload.ParseRate("40/s"); err != nil || r != 40 {
		t.Errorf("40/s: %v %v", r, err)
	}
	if r, err := gristload.ParseRate("120/m"); err != nil || r != 2 {
		t.Errorf("120/m: %v %v", r, err)
	}
	if b, err := gristload.ParseBurst("3x30s"); err != nil || b.Factor != 3 || b.For != 30*time.Second {
		t.Errorf("3x30s: %v %v", b, err)
	}
	for _, bad := range []string{"1x30s", "3x", "x30s", "3*30s"} {
		if _, err := gristload.ParseBurst(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// The command line writes sizing.yaml and its evidence, and says what it
// wrote; without -rate it is a usage error naming the flags.
func TestRun(t *testing.T) {
	app := shop()
	dir := t.TempDir()
	steps := recording(app, 600, 50*time.Millisecond, 5*time.Millisecond, 30*time.Millisecond, 0)
	path := dir + "/steps.jsonl"
	var lines []byte
	for _, s := range steps {
		b, err := s.MarshalLine()
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, b...)
	}
	if err := os.WriteFile(path, lines, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errs strings.Builder
	err := gristload.Run(context.Background(), app, []string{"-steps", path, "-rate", "16/s", "-burst", "3x20s", "-timeout", "1s", "-seeds", "-1", "-out", dir + "/deploy/sizing.yaml"}, &out, &errs)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dir + "/deploy/sizing.yaml")
	if err != nil || !strings.Contains(string(b), "# generated by `shop sizing -steps") || !strings.Contains(out.String(), "gateway.retries.POST: ") {
		t.Fatalf("sizing.yaml %v:\n%s\nstdout:\n%s", err, b, out.String())
	}
	if _, err := os.Stat(dir + "/deploy/sizing/tenant-retries.svg"); err != nil {
		t.Errorf("no evidence beside it: %v", err)
	}
	var usage *gristload.UsageError
	if err := gristload.Run(context.Background(), app, []string{"-steps", path}, &out, &errs); !errors.As(err, &usage) || !strings.Contains(err.Error(), "-rate") {
		t.Errorf("without -rate: %v, want a usage error naming it", err)
	}
}
