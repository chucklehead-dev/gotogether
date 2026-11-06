package library

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// Exercise the actual HTTP answer, both recorders and both generated bindings.
// A count alone cannot distinguish wrong identities, order or multiplicity.
func TestQueryListsPreserveLoanIdentity(t *testing.T) {
	qtest.RequireQuint(t)
	tier := gristtest.NewTier(t, App, gristtest.TierConfig{
		Local:     map[*grist.Dependency]grist.Handlers{Mailer: nil},
		LoseEvery: -1, DependencyLoseEvery: -1,
	})
	tier.Start("node")
	ctx := tier.Clients()
	env := func(id string) grist.Envelope {
		return grist.Envelope{ID: id, Tenant: "t1", Caller: "reader"}
	}
	tier.Do(ctx, env("join"), "join", Join{Member: "a"})
	tier.Do(ctx, env("stock-x"), "add-book", AddBook{Book: "x", Copies: 2})
	tier.Do(ctx, env("stock-y"), "add-book", AddBook{Book: "y", Copies: 1})
	for i, book := range []string{"x", "y", "x"} {
		tier.Do(ctx, env(fmt.Sprint("borrow-", i)), "borrow", Borrow{Member: "a", Book: book})
	}
	probe := func(id, member string, want []string) {
		t.Helper()
		args, err := json.Marshal(MemberLoans{Member: member})
		if err != nil {
			t.Fatal(err)
		}
		raw, _, err := tier.Client.Query(ctx, env("answer-"+id), "member-loans", args)
		if err != nil {
			t.Fatal(err)
		}
		var got LoansView
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Count != len(want) || !slices.Equal(got.Books, want) {
			t.Fatalf("member %s: actual HTTP answer %s, want books %v", member, raw, want)
		}
		// Do records the client's observed answer as well as the store's.
		tier.Do(ctx, env("recorded-"+id), "member-loans", MemberLoans{Member: member})
	}
	probe("full", "a", []string{"x", "y", "x"})
	probe("empty", "b", []string{})
	tier.Do(ctx, env("return-x"), "return-book", ReturnBook{Member: "a", Book: "x"})
	probe("returned-first-copy", "a", []string{"y", "x"})
	tier.Do(ctx, env("return-y"), "return-book", ReturnBook{Member: "a", Book: "y"})
	probe("returned-y", "a", []string{"x"})
	tier.Settle(nil)
	tier.Check(commits, calls)
	steps := tier.Steps()
	for _, test := range []struct{ name, books string }{
		{"identity", `["x","wrong-book","x"]`},
		{"order", `["y","x","x"]`},
		{"multiplicity", `["x","y","y"]`},
		{"empty", `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := slices.Clone(steps)
			recorders := map[string]bool{}
			for i, step := range changed {
				if step.Action != "memberLoans" || step.Obs["books"] != `["x","y","x"]` {
					continue
				}
				changed[i].Obs = maps.Clone(step.Obs)
				changed[i].Obs["books"] = test.books
				recorders[step.Recorder] = true
			}
			if len(recorders) < 2 {
				t.Fatalf("control did not change both store and client list observations: %v", recorders)
			}
			qtest.Validate(t, commits, changed).Expect(t, qtest.Mismatch("memberLoans"))
			qtest.Validate(t, calls, changed, qtest.Linearize()).Expect(t, qtest.Mismatch("memberLoans"))
		})
	}
}
