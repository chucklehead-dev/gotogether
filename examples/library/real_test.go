package library

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

// TestRealHTTP: the real tier (gristtest.Tier), judged by the same
// bindings: the app on a SQLite file served over real HTTP, one node and
// its mailer, two clients calling the API at once (joining, cataloguing,
// borrowing and returning), every fifth command's answer lost after it was
// served (its client retries under the same key, the marker answers). The
// tier waits for the receipts processor and the relay, probes every
// member's loans and records the settle; the steps conform at the store
// (every borrow's receipt sent) and, linearized, at the clients, and every
// answer a client got has its store-side step.
func TestRealHTTP(t *testing.T) {
	qtest.RequireQuint(t)
	// the mailer in process: its default double, recording its sends
	// with the node's steps
	mailer := grist.Local(grist.DefaultHandlers(Mailer))
	tier := gristtest.NewTier(t, App, gristtest.TierConfig{
		Deps: func(ctx context.Context, st grist.Store) context.Context { return Mailer.With(ctx, mailer) }})
	tier.Start("node")
	members := []string{"a", "b"}
	var wg sync.WaitGroup
	for c := 1; c <= 2; c++ {
		wg.Go(func() {
			ctx := qobs.Thread(tier.Clients()) // one thread per goroutine
			r := rand.New(rand.NewPCG(1, uint64(c)))
			env := func(op int) grist.Envelope {
				return grist.Envelope{ID: fmt.Sprintf("c%d-%d", c, op), Tenant: "t1", Actor: fmt.Sprint("client", c)}
			}
			me := members[c-1]
			tier.Do(ctx, env(0), "join", Join{Member: me})
			tier.Do(ctx, env(1), "add-book", AddBook{Book: []string{"x", "y"}[c-1], Copies: 2})
			for op := 2; op <= 12; op++ {
				book := []string{"x", "y"}[r.IntN(2)]
				switch r.IntN(4) {
				case 0:
					tier.Do(ctx, env(op), "member-loans", MemberLoans{Member: me})
				case 1:
					tier.Do(ctx, env(op), "return-book", ReturnBook{Member: me, Book: book})
				default:
					tier.Do(ctx, env(op), "borrow", Borrow{Member: me, Book: book})
				}
			}
		})
	}
	wg.Wait()
	tier.Settle(func(ctx context.Context) {
		for i, m := range members {
			tier.Do(ctx, grist.Envelope{ID: fmt.Sprint("probe-", i), Tenant: "t1", Actor: "probe"}, "member-loans", MemberLoans{Member: m})
		}
	})
	tier.Check(commits, calls)
}
