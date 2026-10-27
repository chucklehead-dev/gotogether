package counters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // the real tier's driver

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// realMailer is a mailer that sends at once (the real tier's stand-in:
// nothing is lost, so every welcome is notified).
type realMailer struct {
	mu   sync.Mutex
	sent []Mail
}

func (m *realMailer) Send(_ context.Context, key, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, Mail{Key: key, Msg: msg})
	return nil
}

// The real tier, judged by the same binding: the app on SQLite (a temp
// file, sqlstore in its single-writer mode), a node (App.Start: the relay
// and the pollers, goroutines), four clients appending to two tenants at
// once, every step written to a file (qobs.FileSink, PerStep) and
// validated with counters.binding.yaml as a simulation's are. The node is
// stopped halfway and a second one started, with a relay of its own: it
// records the step log again from its first row, records the first node's
// copies make the same records, and Reconstruct keeps one of each. After
// the second phase the harness waits for the processors and the relay to
// catch up, probes every counter, and records the settle, so the eventual
// val (every counter welcomed) is judged.
func TestRealSQLite(t *testing.T) {
	qtest.RequireQuint(t)
	dir := t.TempDir()
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(filepath.Join(dir, "counters.db"), 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	st, err := sqlstore.Open(ctx, db, sqlstore.SQLite, sqlstore.Options{Name: App.Name})
	if err != nil {
		t.Fatal(err)
	}
	tenants := []string{"t1", "t2"}
	for _, tn := range tenants {
		if err := st.CreateTenant(ctx, tn); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "steps.jsonl")
	sink, err := qobs.FileSink(path, qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	rec := qobs.NewRecorder(sink, qobs.WithID("node"))
	run := qobs.WithRecorder(WithDeps(ctx, Deps{Mailer: &realMailer{}, Store: st}), rec)

	// start is a node: its relay (returned, to wait for) and pollers, until stop
	start := func() (relay *grist.Relay, stop func()) {
		relay, err := grist.NewRelay(st)
		if err != nil {
			t.Fatal(err)
		}
		nctx, cancel := context.WithCancel(grist.WithRelay(run, relay))
		if err := App.Start(nctx, st); err != nil {
			t.Fatal(err)
		}
		return relay, cancel
	}
	clients := func(phase int) {
		var wg sync.WaitGroup
		for c := 1; c <= 4; c++ {
			wg.Go(func() {
				r := rand.New(rand.NewPCG(uint64(phase), uint64(c)))
				for op := 1; op <= 8; op++ {
					env := grist.Envelope{ID: fmt.Sprintf("p%d-c%d-%d", phase, c, op), Actor: fmt.Sprint("client", c)}
					env.Tenant = tenants[r.IntN(len(tenants))]
					counter, name := []string{"a", "b", "c"}[r.IntN(3)], []string{"a", "b"}[r.IntN(2)]
					var cmd string
					var args any
					switch r.IntN(3) {
					case 0:
						cmd, args = "create-counter", CreateCounter{Counter: counter, Name: name}
					case 1:
						cmd, args = "increment-counter", IncrementCounter{Counter: counter, By: 1 + r.IntN(3)}
					default:
						b, _ := json.Marshal(ShowCounter{Counter: counter})
						if _, _, err := App.Query(run, st, env, "show-counter", b); err != nil {
							t.Errorf("show %s: %v", counter, err)
						}
						continue
					}
					b, _ := json.Marshal(args)
					for try := 0; ; try++ { // an unknown outcome is retried under its id, as a client must
						_, err := App.Execute(run, st, env, cmd, b)
						var rej *grist.Rejected
						if err == nil || errors.As(err, &rej) || errors.Is(err, grist.ErrConflict) {
							break
						}
						if !errors.Is(err, grist.ErrOutcomeUnknown) && !errors.Is(err, grist.ErrUnavailable) || try == 5 {
							t.Errorf("%s %s: %v", cmd, env.ID, err)
							break
						}
					}
				}
			})
		}
		wg.Wait()
	}

	_, stop := start()
	clients(1)
	stop()
	relay, stop := start() // a second node: its relay records the step log from its first row
	clients(2)
	quiet(t, st, relay, tenants)
	for _, tn := range tenants { // the probe: every counter, on the settled system
		for i, c := range []string{"a", "b", "c"} {
			b, _ := json.Marshal(ShowCounter{Counter: c})
			if _, _, err := App.Query(run, st, grist.Envelope{ID: fmt.Sprint("probe-", i), Tenant: tn, Actor: "probe"}, "show-counter", b); err != nil {
				t.Fatal(err)
			}
		}
	}
	time.Sleep(10 * time.Millisecond) // past RealTimeSlack after the last step
	qobs.Settled(run)
	stop()
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	steps, err := qtrace.ReadJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	relayed := map[string]int{}
	for _, s := range steps {
		if s.Recorder == grist.RecorderID(App.Name, s.Actor) {
			relayed[s.Actor]++
		}
	}
	if relayed["t1"] == 0 || relayed["t2"] == 0 {
		t.Fatalf("relayed steps by tenant %v: want both tenants' commits, from the step log", relayed)
	}
	traces, _ := qtrace.Reconstruct(steps)
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
	t.Logf("%d steps written, %d relayed (%d kept after the second relay's copies)", len(steps), relayed["t1"]+relayed["t2"], kept)
	qtest.Validate(t, commits, steps).Expect(t, qtest.Conforms(), qtest.Settles())
}

// quiet waits until the processors have handled every trigger and the
// relay has recorded every committed step, twice in a row: a real tier's
// settle.
func quiet(t *testing.T, st grist.Store, relay *grist.Relay, tenants []string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for calm := 0; calm < 2; {
		if time.Now().After(deadline) {
			t.Fatal("the node did not catch up within 30 s")
		}
		time.Sleep(20 * time.Millisecond)
		done := true
		for _, tn := range tenants {
			for p, on := range map[string]string{"average": "counter-incremented", "welcome": "counter-created"} {
				last, _, err := st.Read(ctx, tn, grist.Selector{Types: []string{on}, Reverse: true, Limit: 1}, 0)
				if err != nil {
					t.Fatal(err)
				}
				cp, err := st.Checkpoint(ctx, tn, p)
				if err != nil {
					t.Fatal(err)
				}
				if len(last) > 0 && cp < last[0].Pos {
					done = false
				}
			}
		}
		if n, err := relay.Behind(ctx); err != nil || n > 0 {
			done = false
		}
		if done {
			calm++
		} else {
			calm = 0
		}
	}
}
