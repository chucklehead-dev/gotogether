package library

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// The bindings, generated from the App's catalog (`go run ./cmd/library
// model`): the store's side and the clients'.
const (
	commits = "library.binding.yaml"
	calls   = "library.calls.binding.yaml"
)

// faulty crashes the node at 2 in 100 scheduling steps and loses the
// answer of 15 in 100 commits (with CommitFaults), every other fault at
// the default rate (requests and answers lost, duplicated and late).
var faulty = qenv.DefaultRates.Set("nemesis", 0.02).Set("store.library.commit", 0.15)

// withMailer gives each simulation a mailer of its own, reached by the
// receipts processor's effect through its ctx.
func withMailer(s *qenv.Sim, st grist.Store) func(context.Context) context.Context {
	m := NewMailer()
	return func(ctx context.Context) context.Context { return MailerDep.With(ctx, m) }
}

// system is the app under cfg, judged by binding, at faulty rates.
func system(binding string, cfg gristsim.Config, opts ...qtest.Option) qtest.System {
	cfg.Setup = withMailer
	return qtest.System{Binding: binding, Rates: faulty, Opts: opts, Mutant: gristtest.Mutant(),
		Run: func(c qenv.Chooser) error { return gristsim.Run(c, App, cfg) }}
}

// seeds is n, or short under -short.
func seeds(n, short int) int {
	if testing.Short() {
		return short
	}
	return n
}

// of picks one of xs.
func of[T any](pick func(int) int, xs ...T) T { return xs[pick(len(xs))] }

// gen are the workload's arguments: members a and b join (c never does),
// books x and y get 0 to 2 copies (0 refused), borrows and returns of any
// member and book (a the likeliest), and the loans of each.
var gen = map[string]func(pick func(int) int) any{
	"join":         func(p func(int) int) any { return Join{Member: of(p, "a", "b")} },
	"add-book":     func(p func(int) int) any { return AddBook{Book: of(p, "x", "y"), Copies: p(3)} },
	"borrow":       func(p func(int) int) any { return Borrow{Member: of(p, "a", "a", "b", "c"), Book: of(p, "x", "y")} },
	"return-book":  func(p func(int) int) any { return ReturnBook{Member: of(p, "a", "a", "b", "c"), Book: of(p, "x", "y")} },
	"member-loans": func(p func(int) int) any { return MemberLoans{Member: of(p, "a", "b", "c")} },
}

// opened is the library as a prologue leaves it before the clients start:
// a and b members, one copy each of x and y, and an addition refused as
// invalid. A borrow needs a join and an
// addition before it, which random sends mostly lacked (the first
// TestFaithful's runs never committed one).
var opened = []gristsim.Call{
	{Name: "join", Args: Join{Member: "a"}}, {Name: "join", Args: Join{Member: "b"}},
	{Name: "add-book", Args: AddBook{Book: "x", Copies: 1}}, {Name: "add-book", Args: AddBook{Book: "y", Copies: 1}},
	{Name: "add-book", Args: AddBook{Book: "z", Copies: 0}}, // refused: invalid, which random sends seldom are
}

// contended is one tenant, three clients of five operations each after the
// opening, borrows weighted two to one (a name listed twice in Send).
var contended = gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 5, CommitFaults: true, Gen: gen, Prologue: opened,
	Send: []string{"join", "add-book", "borrow", "borrow", "return-book", "member-loans"}}

// TestFaithful: through commit faults, node crashes and a lossy API, every
// run conforms at the store and settles (every borrow's receipt sent), the
// commit faults happened, and the runs took every block of the catalog by
// each of its outcomes but the limit refusal, which three borrows of one
// member in fifteen random operations seldom reach (TestGreedy claims it).
func TestFaithful(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(commits, contended).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 4)}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.library.commit").Faulted(),
			gristtest.Exercised(App, "borrow.limit"))
}

// TestFaithfulAtClients: the same runs judged where the clients saw them,
// linearized: a borrow applied twice would show as a count no order
// explains.
func TestFaithfulAtClients(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(calls, contended, qtest.Linearize()).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 3)}).
		Expect(t, qtest.Conforms())
}

// scarce is a and b joining, book x one copy at a time, and borrows of x,
// no faults: borrows outrun the copies. The noCopyCheck mutant's shape.
var scarce = gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 4, Send: []string{"join", "add-book", "borrow"},
	Gen: map[string]func(pick func(int) int) any{
		"join":     func(p func(int) int) any { return Join{Member: of(p, "a", "b")} },
		"add-book": func(p func(int) int) any { return AddBook{Book: "x", Copies: 1} },
		"borrow":   func(p func(int) int) any { return Borrow{Member: of(p, "a", "b"), Book: "x"} },
	}}

// TestScarce: the faithful app at the mutant's shape refuses a borrow with
// no copy free, and some borrow is refused so.
func TestScarce(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(commits, scarce).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(10, 3)}).
		Expect(t, qtest.Conforms(), qtest.None(qtest.Rejects("borrow")), qtest.Takes("borrow").Outcome("unavailable"))
}

// greedy is one member borrowing from three books of three copies each
// (joined and catalogued by a prologue), at ReadCommitted, no faults:
// three clients race for the member's last loans, nine borrows for three.
// The staleLimit mutant's shape. (With the join and the additions among
// the random sends, no run of the first version reached the limit at all:
// Takes(...).Outcome("limit") said so.)
var greedy = gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 3, Isolation: qenv.ReadCommitted,
	Send: []string{"borrow"},
	Prologue: []gristsim.Call{{Name: "join", Args: Join{Member: "a"}},
		{Name: "add-book", Args: AddBook{Book: "x", Copies: 3}}, {Name: "add-book", Args: AddBook{Book: "y", Copies: 3}}, {Name: "add-book", Args: AddBook{Book: "z", Copies: 3}}},
	Gen: map[string]func(pick func(int) int) any{
		"borrow": func(p func(int) int) any { return Borrow{Member: "a", Book: of(p, "x", "y", "z")} },
	}}

// TestGreedy: the faithful app at the mutant's shape holds the limit, and
// some borrow is refused at it.
func TestGreedy(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(commits, greedy).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(10, 3)}).
		Expect(t, qtest.Conforms(), qtest.None(qtest.Rejects("borrow")), qtest.Takes("borrow").Outcome("limit"))
}

// crashAfterMail crashes the node just after the first receipt's mail,
// before its checkpoint: the restarted node mails it again under the same
// key. The noDedupe mutant's shape.
func crashAfterMail(seed uint64) qenv.Chooser {
	return &qenv.Script{Base: qenv.NewSeeded(seed),
		Faults: []qenv.Scripted{{Kind: "grist.receipts.effect", Name: "crashAfter"}}}
}

// lending is a and b borrowing x and y after a prologue that joins them
// and adds two copies of each, no other faults: every run borrows.
var lending = gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 3, Send: []string{"borrow"},
	Prologue: []gristsim.Call{
		{Name: "join", Args: Join{Member: "a"}}, {Name: "join", Args: Join{Member: "b"}},
		{Name: "add-book", Args: AddBook{Book: "x", Copies: 2}}, {Name: "add-book", Args: AddBook{Book: "y", Copies: 2}},
	},
	Gen: map[string]func(pick func(int) int) any{
		"borrow": func(p func(int) int) any { return Borrow{Member: of(p, "a", "b"), Book: of(p, "x", "y")} },
	}}

// TestReceiptOnce: after a crash between a receipt's mail and its
// checkpoint, the mail is sent again and the mailer drops the duplicate:
// every run conforms (one site record per key) and settles (every borrow's
// receipt sent).
func TestReceiptOnce(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(commits, lending).Explore(t, qtest.ExploreConfig{Seeds: seeds(6, 3), Chooser: crashAfterMail}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("grist.receipts.effect").Faulted())
}

// TestGenerated: the model's skeleton and the bindings are what the
// catalog generates (regenerate with `go run ./cmd/library model`).
func TestGenerated(t *testing.T) {
	if gristtest.Mutant() != "" {
		t.Skip("a mutant's binary: its catalog is not the app's")
	}
	gristtest.Generated(t, App)
}
