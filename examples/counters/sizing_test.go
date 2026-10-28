package counters

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/gristload"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qload"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// sizingSteps is the recording sizing reads: one client, one call at a
// time (so each call's duration is its service, no wait), on the real
// tier (SQLite, real HTTP, the mailer's 20 ms).
const sizingSteps = "testdata/sizing/steps.jsonl"

// TestRecordSizingSteps records sizingSteps (GRIST_UPDATE=1): 400 calls,
// three in four on t1, a mixture of creates, increments, shows and
// retirements. Checked in, so sizing's tests do not depend on this
// machine's speed: its measured times are one machine's, once.
func TestRecordSizingSteps(t *testing.T) {
	if os.Getenv("GRIST_UPDATE") == "" {
		t.Skip("GRIST_UPDATE=1 records " + sizingSteps)
	}
	tier := realTier(t, gristtest.TierConfig{Tenants: []string{"t1", "t2"}, DependencyLoseEvery: 1 << 30})
	tier.Start("n1")
	ctx := qobs.Thread(tier.Clients())
	r := rand.New(rand.NewPCG(6, 1))
	made := map[string][]string{}
	for i := range 400 {
		tenant := "t1"
		if r.IntN(4) == 0 {
			tenant = "t2"
		}
		env := grist.Envelope{ID: fmt.Sprintf("s%d", i), Tenant: tenant, Caller: "client"}
		have := made[tenant]
		switch k := r.IntN(20); {
		case k < 3 || len(have) == 0:
			c := fmt.Sprintf("c%d", i)
			made[tenant] = append(have, c)
			tier.Do(ctx, env, "create-counter", CreateCounter{Counter: c, Name: c})
		case k < 15:
			tier.Do(ctx, env, "increment-counter", IncrementCounter{Counter: have[r.IntN(len(have))], By: 1 + r.IntN(3)})
		case k < 19:
			tier.Do(ctx, env, "show-counter", ShowCounter{Counter: have[r.IntN(len(have))]})
		default:
			tier.Do(ctx, env, "retire-counter", RetireCounter{Counter: have[r.IntN(len(have))]})
		}
	}
	tier.Settle(nil)
	f, err := os.Create(filepath.FromSlash(sizingSteps))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, s := range tier.Steps() {
		b, err := s.MarshalLine()
		if err != nil {
			t.Fatal(err)
		}
		f.Write(b)
	}
}

// postRetries3 is the configuration mutant (docs/grist-platform.md §9
// slice 6): a Gateway retrying commands three times inside grist/client's
// three attempts, nine tries a call, the Gateway's not budgeted by the
// client.
func postRetries3() gristload.Load {
	return gristload.Load{Rate: 60, Burst: gristload.Burst{Factor: 3, For: 30 * time.Second},
		Gateway: gristload.Gateway{Commands: 3}, Client: &client.Client{MaxAttempts: 3}}
}

func sizing(t *testing.T, l gristload.Load) *gristload.Sizing {
	t.Helper()
	steps, err := gristload.ReadSteps(sizingSteps)
	if err != nil {
		t.Fatal(err)
	}
	s, err := gristload.Report(App, steps, l)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + s.Text())
	return s
}

// `counters sizing -rate 60/s -burst 3x30s` with postRetries3 in force,
// by the chain alone: the mutant is advised against, its recovery orders
// of magnitude below the recommended setting's (no Gateway retry on
// commands); t1, whose commits are one at a time, gets a rate limit
// between its load and its service rate, replicas shown not to help;
// the welcome processor's lag under a slow mailer is no pod's to clear.
// Orderings and orders of magnitude, never the chain's seconds.
func TestSizing(t *testing.T) {
	l := postRetries3()
	l.Seeds = -1
	s := sizing(t, l)
	if post := s.Get("gateway.retries.POST"); post.Value != 0 || !strings.HasPrefix(post.InForce, "advised against") {
		t.Fatalf("POST retries %v (%s): want 0, postRetries3 advised against", post.Value, post.InForce)
	}
	var rec, mutant gristload.Ranked
	for _, r := range s.Retries {
		switch {
		case r.Gateway == 1:
			rec = r
		case r.Gateway == 3 && r.Attempts == 3:
			mutant = r
		}
	}
	if rec.Chain < 0.99 || mutant.Chain > 1e-2*rec.Chain {
		t.Fatalf("chain recovery: recommended %.3g, postRetries3 %.3g: want the mutant two orders of magnitude below", rec.Chain, mutant.Chain)
	}
	limit := s.Get("tenant.rateLimit")
	v, err := strconv.ParseFloat(strings.TrimSuffix(limit.Value.(string), "/s"), 64)
	if err != nil {
		t.Fatal(err)
	}
	mu := s.Measured.Of(gristload.Command).Mu()
	load := 60 * s.Measured.HotShare
	if v <= load*0.5 || v >= mu {
		t.Fatalf("t1's limit %.3g/s: want it above its share of the load (%.3g/s, commands a part of it) and below its service rate %.3g/s", v, load, mu)
	}
	if !strings.Contains(limit.Because, "replicas alike") || !strings.Contains(limit.Because, "which no replica count gives") {
		t.Fatalf("t1's limit says nothing of replicas: %s", limit.Because)
	}
	welcome := s.Get("processors.welcome.scaleOnLag")
	if welcome == nil || welcome.Value != false || !strings.Contains(welcome.Because, "mailer.send") {
		t.Fatalf("welcome: %+v", welcome)
	}
	if s.Recommended.Gateway != 1 || s.Recommended.Timeout <= 0 || s.Recommended.Timeout > 2*time.Second {
		t.Fatalf("recommended %+v: one try at the Gateway, a timeout up to the 2 s in force", s.Recommended)
	}
	if len(s.Findings) != 3 {
		t.Fatalf("findings %q: want the retries', the hot tenant's and the processor's", s.Findings)
	}
}

// The acceptance (docs/grist-platform.md §9 slice 6): with the
// simulation confirming, the recommended setting recovers from the
// declared burst and postRetries3 stays degraded, in most of 6 seeds,
// grist/client's own retry decision behind the Gateway's tries in each.
func TestSizingAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("load simulations: about a minute")
	}
	s := sizing(t, postRetries3())
	if tool := s.Get("gateway.retries.POST").Tool; tool != "chain + qload (6 seeds)" {
		t.Fatalf("POST retries' tool %q: want the simulation's confirmation", tool)
	}
	count := func(rs []qload.Recovery) (n int) {
		for _, r := range rs {
			if r.Recovered {
				n++
			}
		}
		return n
	}
	rec := qload.Runs(s.Commands(s.Recommended.Gateway, nil, s.Recommended.Timeout), s.Trigger(), 6)
	mutant := qload.Runs(s.Commands(3, nil, 2*time.Second), s.Trigger(), 6)
	t.Logf("recommended (gateway %d, %v): %d of 6 recover; postRetries3: %d of 6", s.Recommended.Gateway, s.Recommended.Timeout, count(rec), count(mutant))
	if count(rec) < 4 || count(mutant) > 2 {
		t.Fatalf("recommended %d of 6, postRetries3 %d of 6: want most and few", count(rec), count(mutant))
	}
	rec[0].Expect(t, qload.Recovered())
	mutant[0].Expect(t, qload.StaysDegraded())
}

// Sizing is a binary of its own: cmd/counters links none of qload, qmeta
// or gristload; cmd/counters-sizing does.
func TestSizingStaysOutOfTheBinary(t *testing.T) {
	deps := func(pkg string) string {
		out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
		}
		return string(out)
	}
	sizingOnly := []string{"/qload\n", "/qmeta\n", "/grist/gristload\n"}
	app, tool := deps("./cmd/counters"), deps("./cmd/counters-sizing")
	for _, p := range sizingOnly {
		if strings.Contains(app, p) {
			t.Errorf("cmd/counters links %s", strings.TrimSpace(p))
		}
		if !strings.Contains(tool, p) {
			t.Errorf("cmd/counters-sizing does not link %s: the test is not checking what it says", strings.TrimSpace(p))
		}
	}
}
