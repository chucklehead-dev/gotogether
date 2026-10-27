package counters

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // the real tier's driver

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// realMailer is a third party's mailer over HTTP, the real tier's
// stand-in for a mail provider: it takes 20 ms to send (nothing is lost,
// so every welcome is notified, but not at once, so a settle recorded
// before the processors caught up is seen) and answers {} from a handler
// of its own, not grist's: it records nothing of the run, as a real
// provider records nothing in the app's trace. The welcome's caller steps
// (mailerSend) are the mail's only record.
func realMailer(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond) //qenv:ok the real tier's mailer, outside any simulation
		w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// realTier is counters' real tier under cfg (gristtest.Tier): its bank
// the team's own (NewBank) behind grist.ServeCalls on a loopback server
// of its own, recording its payments into its own step file and losing
// every cfg.DependencyLoseEvery-th answer (default every fifth) after the
// payment was made (TierConfig.Serve), the nodes calling it through
// grist.OverHTTP; its mailer a third party's over HTTP (realMailer,
// grist.OverHTTP), which records nothing. The real variant
// (counters.real.binding.yaml) judges it: every val of the default
// binding but the mailer's site claim, which no step of this tier can
// judge.
func realTier(t *testing.T, cfg gristtest.TierConfig) *gristtest.Tier {
	mailer := grist.OverHTTP(realMailer(t), nil)
	cfg.Serve = map[*grist.Dependency]grist.Handlers{Bank: NewBank()}
	cfg.Deps = func(ctx context.Context, st grist.Store) context.Context {
		return StoreDep.With(Mailer.With(ctx, mailer), st)
	}
	return gristtest.NewTier(t, App, cfg)
}

// retireAll has n clients each create a counter of its own in tenant t1
// and retire it (the creator's own retirement, its total 0): n payouts,
// so a bank losing every n-th answer loses the last's.
func retireAll(tier *gristtest.Tier, phase, n int) {
	var wg sync.WaitGroup
	for c := 1; c <= n; c++ {
		wg.Go(func() {
			ctx := qobs.Thread(tier.Clients())
			actor := fmt.Sprint("client", c)
			counter, id := fmt.Sprintf("r%d-%d", phase, c), fmt.Sprintf("p%d-c%d", phase, c)
			tier.Do(ctx, grist.Envelope{ID: id + "-create", Tenant: "t1", Actor: actor}, "create-counter", CreateCounter{Counter: counter, Name: counter})
			tier.Do(ctx, grist.Envelope{ID: id + "-retire", Tenant: "t1", Actor: actor}, "retire-counter", RetireCounter{Counter: counter})
		})
	}
	wg.Wait()
}

// The real tier, judged by the real variant of the bindings
// (gristtest.Tier, realTier): the app
// on SQLite (a temp file, sqlstore) served over real HTTP, a node
// (App.Start: the relay, the tenant watcher and the pollers, goroutines),
// four clients calling the API at once over TCP (grist/client: each call
// recorded where the client sees it, <tenant>/clients, retried under its
// Idempotency-Key), every step written to one PerStep file and validated
// with counters.real.binding.yaml (the store's side: the default
// binding's vals but the mailer's site claim, the mailer being a third
// party's) and counters.calls.binding.yaml (the clients', linearized), as
// a simulation's are. Every fifth command's answer is lost after the
// command was served: its client retries under the same key, the marker
// answers (Idempotent-Replayed). Halfway through the first phase the first
// node is killed: its sink dies at that instant (whatever its relay had
// not recorded is lost with it, as with kill -9), its goroutines are
// stopped, and a second node takes the server's traffic, with a relay of
// its own that records the step log from its first row (its copies of
// the first node's records are Reconstruct's duplicates) and every row
// the first never recorded. The second phase creates new counters, whose
// welcomes only the second node sends. The third retires two counters:
// two payouts through the bank, a server of its own behind
// grist.ServeCalls (its payments recorded into its own step file,
// validated with the nodes'), which loses every second answer after
// paying it (the fewest that give the site claim both an answer to
// compare with the bank's record and a lost one): that payout's
// outcome is unknown (grist.unknown, at most once: never paid again), and
// the bank paid each key once. The tier then waits for the
// processors and the relay, probes every counter over the API and
// records the settle, so the eventual val (every counter welcomed) is
// judged; and every answer a client got is checked against the store's
// side (gristtest.Answered: a refusal the store did not record would pass
// both bindings).
func TestRealHTTP(t *testing.T) {
	qtest.RequireQuint(t)
	tenants := []string{"t1", "t2"}
	tier := realTier(t, gristtest.TierConfig{Tenants: tenants, DependencyLoseEvery: 2})
	// clients runs four clients of 8 operations each on counters, names
	// from names; after the first client's half, kill runs (if set)
	clients := func(phase int, counters, names []string, kill func()) {
		var wg sync.WaitGroup
		for c := 1; c <= 4; c++ {
			wg.Go(func() {
				ctx := qobs.Thread(tier.Clients()) // one thread per goroutine
				r := rand.New(rand.NewPCG(uint64(phase), uint64(c)))
				for op := 1; op <= 8; op++ {
					if op == 5 && c == 1 && kill != nil {
						kill()
					}
					env := grist.Envelope{ID: fmt.Sprintf("p%d-c%d-%d", phase, c, op), Actor: fmt.Sprint("client", c)}
					env.Tenant = tenants[r.IntN(len(tenants))]
					counter, name := counters[r.IntN(len(counters))], names[r.IntN(len(names))]
					switch r.IntN(3) {
					case 0:
						tier.Do(ctx, env, "create-counter", CreateCounter{Counter: counter, Name: name})
					case 1:
						tier.Do(ctx, env, "increment-counter", IncrementCounter{Counter: counter, By: 1 + r.IntN(3)})
					default:
						tier.Do(ctx, env, "show-counter", ShowCounter{Counter: counter})
					}
				}
			})
		}
		wg.Wait()
	}

	first := tier.Start("node1")
	clients(1, []string{"a", "b", "c"}, []string{"a", "b"}, func() {
		// the first relay has recorded some row (on a loaded machine it may
		// lag every commit so far, and the second relay's copies would then
		// be no copies): within a second
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && !relayedSome(t, tier, first); {
			time.Sleep(5 * time.Millisecond) //qenv:ok polling the real tier
		}
		first.Kill()
		tier.Start("node2") // its relay records the step log from its first row
	})
	behind, err := first.Relay.Behind(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if behind == 0 {
		t.Fatal("the killed node's relay recorded every row: the second node's relay has nothing of its own to record, and this run proves nothing of it")
	}
	clients(2, []string{"d", "e", "f"}, []string{"c", "d"}, nil)
	retireAll(tier, 3, 2)
	tier.Settle(func(ctx context.Context) { // the probe: every counter, on the settled system
		for _, tn := range tenants {
			for i, c := range []string{"a", "b", "c", "d", "e", "f"} {
				tier.Do(ctx, grist.Envelope{ID: fmt.Sprint("probe-", i), Tenant: tn, Actor: "probe"}, "show-counter", ShowCounter{Counter: c})
			}
		}
	})
	traces := tier.Check(onReal, calls)
	paidOnce(t, tier, 2)
	forgedReceipt(t, tier.Steps())
	unknown, _, err := tier.Store.Read(context.Background(), "t1", grist.Selector{Types: []string{"grist.unknown"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 1 {
		t.Errorf("%d grist.unknown outcomes: want the payout whose answer the bank lost", len(unknown))
	}

	steps := tier.Steps()
	relayed := map[string]int{}
	for _, s := range steps {
		if s.Recorder == grist.RecorderID(App.Name, s.Actor) {
			relayed[s.Actor]++
		}
	}
	if relayed["t1"] == 0 || relayed["t2"] == 0 {
		t.Fatalf("relayed steps by tenant %v: want both tenants' commits, from the step log", relayed)
	}
	kept := 0
	for _, tr := range traces {
		for _, s := range tr.Steps {
			if s.Recorder == grist.RecorderID(App.Name, s.Actor) {
				kept++
			}
		}
	}
	if kept >= relayed["t1"]+relayed["t2"] {
		t.Errorf("%d relayed records, %d kept: want the second node's copies of the first's rows dropped as duplicates", relayed["t1"]+relayed["t2"], kept)
	}
	t.Logf("%d relayed (%d kept after the second relay's copies); the killed node's relay was %d rows behind", relayed["t1"]+relayed["t2"], kept, behind)
}

// forgedReceipt validates the real tier's steps with the ok payment's
// receipt as its caller saw it changed, as a
// transport that decoded the bank's answer wrongly would record it: the
// site claim everyPaymentAnswerServed fails, the caller's answer no
// longer the bank's served record (its one violation: it is not
// vacuous), and nothing else does (the store's side is the faithful
// app's).
func forgedReceipt(t *testing.T, steps []qtrace.Step) {
	t.Helper()
	// every step (a recorder's seqs span the tenants: a step left out
	// would be a gap, and no eventual val judged)
	var doctored []qtrace.Step
	forged := false
	for _, s := range steps {
		if s.Action == "bankPay" && !s.Invocation && s.Outcome == "ok" && !forged {
			s.Args = maps.Clone(s.Args)
			s.Args["ref"] = "forged"
			forged = true
		}
		doctored = append(doctored, s)
	}
	if !forged {
		t.Fatal("no payment answered ok: the site claim compares nothing")
	}
	qtest.Validate(t, onReal, doctored).Expect(t, qtest.Violates(validate.EventuallyName("everyPaymentAnswerServed")))
}

// paidOnce checks the bank's own step file: n payments, each key applied
// once (a retry would be a replay), and none of them in the nodes' files.
func paidOnce(t *testing.T, tier *gristtest.Tier, n int) {
	t.Helper()
	served := map[string]int{}
	for _, s := range tier.DependencySteps(Bank) {
		if s.Action == "bankPayServed" {
			served[s.Args["key"].(string)]++
		}
	}
	if len(served) != n {
		t.Errorf("the bank's file has %d keys paid: want %d", len(served), n)
	}
	for k, c := range served {
		if c != 1 {
			t.Errorf("key %s paid %d times by the bank", k, c)
		}
	}
	total := 0
	for _, s := range tier.Steps() {
		if s.Action == "bankPayServed" {
			total++
		}
	}
	if total != len(served) {
		t.Errorf("%d bankPayServed steps in the run, %d in the bank's file: want the bank's file the only one recording payments", total, len(served))
	}
}

// lostThenKilled is the lost-answer-and-crash shape over HTTP: one client
// creates counter a and retires it; the bank makes the payment, and its
// answer is lost (a 502) as the node is killed (OnLostAnswer: the node's
// sink dies at that instant), so the payout's completion is lost with
// the node, its call pending (at most once: never made again), and the
// bank's step file holds the payment's only record. A second node then
// takes over and the tier settles.
func lostThenKilled(t *testing.T) *gristtest.Tier {
	killed := make(chan struct{})
	var once sync.Once
	var tier *gristtest.Tier
	tier = realTier(t, gristtest.TierConfig{LoseEvery: -1, DependencyLoseEvery: 1,
		OnLostAnswer: func(d *grist.Dependency, call string) {
			once.Do(func() {
				tier.Current().Kill()
				close(killed)
			})
		}})
	tier.Start("node1")
	ctx := qobs.Thread(tier.Clients())
	tier.Do(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Actor: "client1"}, "create-counter", CreateCounter{Counter: "a", Name: "a"})
	tier.Do(ctx, grist.Envelope{ID: "k2", Tenant: "t1", Actor: "client1"}, "retire-counter", RetireCounter{Counter: "a"})
	select {
	case <-killed:
	case <-time.After(30 * time.Second): //qenv:ok the real tier's own deadline
		t.Fatal("the bank was not called within 30 s")
	}
	tier.Start("node2")
	tier.Settle(nil)
	return tier
}

// pendingPayment checks the shape lostThenKilled makes: the payout's call
// pending (invoked, never completed) and the bank's record the payment's
// only one.
func pendingPayment(t *testing.T, tier *gristtest.Tier) {
	t.Helper()
	traces, _ := qtrace.Reconstruct(tier.Steps())
	pending, completed, served := 0, 0, 0
	for _, tr := range traces {
		for _, s := range tr.Pending {
			if s.Action == "bankPay" {
				pending++
			}
		}
		for _, s := range tr.Steps {
			switch s.Action {
			case "bankPay":
				completed++
			case "bankPayServed":
				served++
			}
		}
	}
	if pending != 1 || completed != 0 || served != 1 {
		t.Errorf("the payout's call: %d pending, %d completed; %d served records: want it pending and the bank's record the only one", pending, completed, served)
	}
}

// The lost-answer-and-crash shape over HTTP (lostThenKilled): the bank's
// own step file is the payment's only record, and the run conforms to
// the real variant (the site's request judged against payoutBankPay:
// the wrongAmount mutant, in this shape, rejects bankPayServed).
func TestRealLostThenKilled(t *testing.T) {
	qtest.RequireQuint(t)
	tier := lostThenKilled(t)
	pendingPayment(t, tier)
	paidOnce(t, tier, 1)
	// the store's side alone (not Tier.Check, which validates the
	// clients' side too: here one client's two calls in sequence, made
	// before the kill, which TestRealHTTP's four clients cover)
	traces, _ := qtrace.Reconstruct(tier.Steps())
	gristtest.Answered(t, App, traces)
	qtest.Validate(t, onReal, tier.Steps()).Expect(t, qtest.Conforms(), qtest.Settles())
	// the default binding judges the mailer's site claim too, which needs
	// the mailer's records: a third party's mailer leaves none, so the
	// welcome's answer is one no site gave. A verdict on the tier, not on
	// the app (it holds whatever the code does), and why the real tier is
	// judged by its own variant (docs/grist-instrumentation.md §5,
	// decision 4); it is also the claim's one violation, the evidence that
	// it is not vacuous (an answer without its site's record fails it)
	qtest.Validate(t, commits, tier.Steps()).Expect(t, qtest.Violates(validate.EventuallyName("everyMailAnswerServed")))
}

// relayedSome reports whether n's relay has recorded a row of the step
// log: fewer rows behind than the log holds.
func relayedSome(t *testing.T, tier *gristtest.Tier, n *gristtest.Node) bool {
	ctx := context.Background()
	total := int64(0)
	for _, tn := range []string{"t1", "t2"} {
		c, err := tier.Store.(store.StepLog).StepCount(ctx, tn)
		if err != nil {
			t.Error(err)
			return true
		}
		total += c
	}
	behind, err := n.Relay.Behind(ctx)
	return err != nil || behind < total
}
