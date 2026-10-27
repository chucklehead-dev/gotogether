package walletgrist

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"

	_ "modernc.org/sqlite" // the real tier's driver

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The real tier, judged by the same bindings (gristtest.Tier): the app on
// a SQLite file served over real HTTP, one node whose ledger is in
// process and records its posts to the node's recorder, three clients
// calling the API at once (grist/client), every step written to one
// PerStep file and validated with wallet.binding.yaml and, linearized,
// wallet.calls.binding.yaml. Every fifth command's answer is lost after
// it was served: its client retries under the same Idempotency-Key and
// the marker answers. The tier waits for the post processor and the relay
// before it records the settle, so allPosted and postedApplied are
// judged; and every answer a client got has its store-side step, with the
// same outcome.
func TestRealHTTP(t *testing.T) {
	qtest.RequireQuint(t)
	ledger := &LocalLedger{}
	tier := gristtest.NewTier(t, App, gristtest.TierConfig{
		Deps: func(ctx context.Context, _ grist.Store) context.Context { return LedgerDep.With(ctx, ledger) }})
	tier.Start("node")
	var wg sync.WaitGroup
	for c := 1; c <= 3; c++ {
		wg.Go(func() {
			ctx := qobs.Thread(tier.Clients()) // one thread per goroutine
			r := rand.New(rand.NewPCG(1, uint64(c)))
			for op := 1; op <= 10; op++ {
				env := grist.Envelope{ID: fmt.Sprintf("c%d-%d", c, op), Tenant: "t1", Actor: fmt.Sprint("client", c)}
				switch r.IntN(3) {
				case 0:
					tier.Do(ctx, env, "transfer", Transfer{Src: stranger[r.IntN(4)], Dest: held3[r.IntN(3)], Amount: r.IntN(5)})
				case 1:
					tier.Do(ctx, env, "deposit", Deposit{Account: held3[r.IntN(3)], Amount: 1 + r.IntN(3)})
				default:
					tier.Do(ctx, env, "read", Read{Account: held3[r.IntN(3)]})
				}
			}
		})
	}
	wg.Wait()
	tier.Settle(func(ctx context.Context) { // the probe: every account, on the settled system
		for i, a := range held3 {
			tier.Do(ctx, grist.Envelope{ID: fmt.Sprint("probe-", i), Tenant: "t1", Actor: "probe"}, "read", Read{Account: a})
		}
	})
	// three clients' 30 calls at once: a run's order may lie past the
	// default budget's 200 orders (one needed 805; the hand-written model
	// it replaced needed as many)
	tier.Check(commits, calls, qtest.LinearizeBudget(2000))
}
