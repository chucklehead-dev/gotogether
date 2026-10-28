package qtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// accountRun is a run that records both sides of an account service:
// each commit at the store (actor "acct": what committed, obs.committed)
// and each client's call where the client sees it (actor "acct.clients":
// Invoke before the request, Done with the balance it was told). lie
// makes the service tell a deposit's client the balance before it.
func accountRun(lie bool) func(c qenv.Chooser) error {
	return func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		st := qenv.NewStore(s, "db", qenv.Serializable)
		st.Load("balance", int64(0))
		op := func(ctx context.Context, action string, amount int64) (told int64) {
			_ = qenv.Retry(ctx, st, 10, func(tx qenv.Txn) error {
				v, _, err := tx.GetForUpdate("balance")
				if err != nil {
					return err
				}
				b := v.(int64)
				nb := b + amount
				if action == "withdraw" {
					nb = b - amount
				}
				if nb < 0 {
					told = b
					return nil // refused: nothing committed, nothing recorded at the store
				}
				tx.Put("balance", nb)
				tx.OnCommit(func() {
					qobs.Record(ctx, action, nil, "actor", "acct", "amount", amount, "obs.committed", nb)
				})
				told = nb
				if lie && action == "deposit" {
					told = b
				}
				return nil
			})
			return told
		}
		for i := range 2 {
			s.Spawn("client", func(ctx context.Context) {
				ctx = qobs.Thread(ctx)
				for k, action := range []string{"deposit", "withdraw", "deposit"} {
					amount := int64(10*(i+1) + k) // a withdrawal never overdraws: each client deposits first
					if action == "withdraw" {
						amount = 1
					}
					call := qobs.Invoke(ctx, action, "actor", "acct.clients", "amount", amount)
					call.Done(nil, "obs.balance", op(ctx, action, amount))
				}
			})
		}
		if err := s.Run(time.Minute); err != nil {
			return err
		}
		return s.Settle(time.Minute)
	}
}

// One run, two bindings: the commits' (actors: ['!*.clients']) and the
// clients' (actors: ['*.clients'], with Linearize). Each checks its side
// alone, so both conform for the faithful service; for a service that
// lies to its clients the commits still conform and the clients' calls
// are a mismatch. Without the filter a binding plans the other side's
// steps, which lack its fields, and no run is judged.
func TestTwoBindingsOneRun(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	cfg := ExploreConfig{Seeds: 4}
	commits := System{Binding: "testdata/account-commits.yaml", Run: accountRun(false)}
	calls := commits.WithBinding("testdata/account-clients.yaml", Linearize())
	if len(commits.Opts) != 0 {
		t.Fatalf("WithBinding changed the system's own options: %d", len(commits.Opts))
	}
	commits.Explore(t, cfg).Expect(t, All(Conforms()))
	calls.Explore(t, cfg).Expect(t, All(Conforms()))

	commits.Run, calls.Run = accountRun(true), accountRun(true)
	commits.Explore(t, cfg).Expect(t, All(Conforms()))
	calls.Explore(t, cfg).Expect(t, Some(Mismatch("deposit")))

	// the filter off: the commits' binding meets the clients' steps,
	// which do not carry what it reads
	spec, _ := filepath.Abs("../validate/testdata/account.qnt")
	plain := filepath.Join(t.TempDir(), "plain.yaml")
	text, _ := os.ReadFile("testdata/account-commits.yaml")
	text = []byte(strings.NewReplacer("spec: ../../validate/testdata/account.qnt", "spec: "+spec, "actors: ['!*.clients']\n", "").Replace(string(text)))
	if err := os.WriteFile(plain, text, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		System{Binding: plain, Run: accountRun(false)}.Explore(f, cfg).Expect(f, All(Conforms()))
	}()
	<-done
	if all := strings.Join(f.errs, "\n"); !strings.Contains(all, `reads the step's field "obs.committed", which the step does not have`) {
		t.Errorf("without actors, the commits' binding: %q", f.errs)
	}
}

// ShortSeeds is the number of runs under -short, Seeds otherwise.
func TestShortSeeds(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	sys := System{Binding: "testdata/account-commits.yaml", Run: accountRun(false)}
	want := 3
	if testing.Short() {
		want = 2
	}
	if x := sys.Explore(t, ExploreConfig{Seeds: 3, ShortSeeds: 2}); len(x.Runs) != want {
		t.Errorf("%d runs, want %d (short %v)", len(x.Runs), want, testing.Short())
	}
	if x := sys.Explore(t, ExploreConfig{Seeds: 3}); len(x.Runs) != 3 {
		t.Errorf("without ShortSeeds: %d runs, want Seeds' 3", len(x.Runs))
	}
}
