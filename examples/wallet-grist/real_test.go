package walletgrist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite" // the real tier's driver

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// The real tier, judged by the same bindings (examples/counters'
// TestRealHTTP, without the node's kill): the app on a SQLite file
// (sqlstore) served over real HTTP (grist.NewHandler behind an
// httptest.Server), one node (App.Start: the relay and the post poller,
// goroutines) whose ledger is in process and records its posts to the
// node's recorder, three clients calling the API at once (grist/client),
// every step written to one PerStep file and validated with
// wallet.binding.yaml and, linearized, wallet.calls.binding.yaml. Every
// fifth command's answer is lost after it was served: its client retries
// under the same Idempotency-Key and the marker answers. The harness
// waits for the post processor and the relay before it records the
// settle, so allPosted and postedApplied are judged; and every answer a
// client got has its store-side step, with the same outcome.
func TestRealHTTP(t *testing.T) {
	qtest.RequireQuint(t)
	dir := t.TempDir()
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(filepath.Join(dir, "wallet.db"), 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	st, err := sqlstore.Open(ctx, db, sqlstore.SQLite, sqlstore.Options{Name: App.Name, Poll: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	const tenant = "t1"
	if err := st.CreateTenant(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "steps.jsonl")
	sink, err := qobs.FileSink(path, qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	deps := WithDeps(ctx, Deps{Ledger: &LocalLedger{}})
	clientsCtx := qobs.WithRecorder(deps, qobs.NewRecorder(sink, qobs.WithID("clients")))

	relay, err := grist.NewRelay(st)
	if err != nil {
		t.Fatal(err)
	}
	rec := qobs.NewRecorder(sink, qobs.WithID("node"))
	nctx, stop := context.WithCancel(qobs.WithRecorder(deps, rec))
	defer stop()
	if err := App.Start(nctx, st, grist.WithRelay(relay)); err != nil {
		t.Fatal(err)
	}
	api, err := grist.NewHandler(App, st, grist.ActorFromHeader("X-Actor"), grist.Middleware(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(qobs.WithRecorder(r.Context(), rec)))
		})
	}))
	if err != nil {
		t.Fatal(err)
	}
	var served, lost, replayed atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && served.Add(1)%5 == 0 {
			api.ServeHTTP(httptest.NewRecorder(), r)
			lost.Add(1)
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		api.ServeHTTP(w, r)
		if w.Header().Get(grist.ReplayedHeader) == "true" {
			replayed.Add(1)
		}
	}))
	defer srv.Close()

	cl := &client.Client{App: App, URL: srv.URL, ActorHeader: "X-Actor", RetryDelay: 10 * time.Millisecond}
	var wg sync.WaitGroup
	for c := 1; c <= 3; c++ {
		wg.Go(func() {
			ctx := qobs.Thread(clientsCtx) // one thread per goroutine
			r := rand.New(rand.NewPCG(1, uint64(c)))
			for op := 1; op <= 10; op++ {
				env := grist.Envelope{ID: fmt.Sprintf("c%d-%d", c, op), Tenant: tenant, Actor: fmt.Sprint("client", c)}
				var cmd string
				var args any
				switch r.IntN(3) {
				case 0:
					cmd, args = "transfer", Transfer{Src: stranger[r.IntN(4)], Dest: held3[r.IntN(3)], Amount: r.IntN(5)}
				case 1:
					cmd, args = "deposit", Deposit{Account: held3[r.IntN(3)], Amount: 1 + r.IntN(3)}
				default:
					cmd, args = "read", Read{Account: held3[r.IntN(3)]}
				}
				b, _ := json.Marshal(args)
				err := cl.Do(ctx, env, cmd, b)
				var rej *grist.Rejected
				if err != nil && !errors.As(err, &rej) && !errors.Is(err, grist.ErrConflict) {
					t.Errorf("%s %s: %v", cmd, env.ID, err)
				}
			}
		})
	}
	wg.Wait()
	quiet(t, st, relay, tenant)
	for i, a := range held3 { // the probe: every account, on the settled system
		b, _ := json.Marshal(Read{Account: a})
		if err := cl.Do(clientsCtx, grist.Envelope{ID: fmt.Sprint("probe-", i), Tenant: tenant, Actor: "probe"}, "read", b); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(10 * time.Millisecond) // past RealTimeSlack after the last step
	qobs.Settled(clientsCtx)
	stop()
	if err := relay.Err(); err != nil {
		t.Fatal(err)
	}
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
	traces, _ := qtrace.Reconstruct(steps)
	answered(t, traces)
	if lost.Load() == 0 || replayed.Load() == 0 {
		t.Errorf("%d answers lost, %d retries answered from their key's marker: want both", lost.Load(), replayed.Load())
	}
	t.Logf("%d steps written; %d answers lost, %d retries replayed", len(steps), lost.Load(), replayed.Load())
	qtest.Validate(t, commits, steps).Expect(t, qtest.Conforms(), qtest.Settles())
	qtest.Validate(t, calls, steps, qtest.Linearize()).Expect(t, qtest.Conforms())
}

// answered checks every command a client was answered against the
// store's side: the store recorded its step, once, with the outcome the
// client was told (a refusal commits no event, so neither binding sees
// its store-side step missing; the client's answer names it).
func answered(t *testing.T, traces []*qtrace.Trace) {
	t.Helper()
	atStore, atClients := map[string]string{}, map[string]string{}
	for _, tr := range traces {
		for _, s := range tr.Steps {
			id, _ := s.Args["id"].(string)
			if id == "" || s.Action != "transfer" && s.Action != "deposit" {
				continue
			}
			side := atStore
			if strings.HasSuffix(s.Actor, "/clients") {
				side = atClients
			}
			if prev, dup := side[id]; dup {
				t.Errorf("command %s has two steps on one side (%s, %s)", id, prev, s.Outcome)
			}
			side[id] = s.Outcome
		}
	}
	if len(atClients) == 0 {
		t.Fatal("no client was answered")
	}
	for id, outcome := range atClients {
		if got, ok := atStore[id]; !ok || got != outcome {
			t.Errorf("command %s: the client was told %s, the store recorded %q (found %v)", id, outcome, got, ok)
		}
	}
}

// quiet waits until the post processor has handled every move and the
// relay has recorded every committed step, twice in a row.
func quiet(t *testing.T, st grist.Store, relay *grist.Relay, tenant string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for calm := 0; calm < 2; {
		if time.Now().After(deadline) {
			t.Fatal("the node did not catch up within 30 s")
		}
		time.Sleep(20 * time.Millisecond)
		done := true
		last, _, err := st.Read(ctx, tenant, grist.Selector{Types: []string{"moved"}, Reverse: true, Limit: 1}, 0)
		if err != nil {
			t.Fatal(err)
		}
		cp, err := st.Checkpoint(ctx, tenant, "post")
		if err != nil {
			t.Fatal(err)
		}
		if len(last) > 0 && cp < last[0].Pos {
			done = false
		}
		if err := relay.Err(); err != nil {
			t.Fatal(err)
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
