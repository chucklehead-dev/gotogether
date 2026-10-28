package validate

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// ledgerLog records ops as one service's serial log of ledger steps:
// each op is an action and its arguments in pairs ("acct", "a", ...).
func ledgerLog(ops ...[]any) []qtrace.Step {
	sink := &qobs.MemorySink{}
	r := qobs.NewRecorder(sink, qobs.WithActor("ledger"))
	for _, op := range ops {
		r.Record(context.Background(), op[0].(string), nil, op[1:]...)
	}
	return sink.Steps()
}

func op(action string, kv ...any) []any { return append([]any{action}, kv...) }

func ledgerChecker(t *testing.T, file string) *Checker {
	t.Helper()
	skip.Quint(t)
	c, err := NewChecker("testdata/"+file, Options{Dir: t.TempDir(), NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A partitioned log reports every failing entity, each with its own
// failure, where the log judged whole stops at its first: a production
// day's report lists all its disagreements. The passing entities are
// one line.
func TestPartitionReportsEveryFailingEntity(t *testing.T) {
	t.Parallel()
	c := ledgerChecker(t, "ledger.yaml")
	steps := ledgerLog(
		op("open", "acct", "a"), op("open", "acct", "b"), op("open", "acct", "c"), op("open", "acct", "d"),
		op("deposit", "acct", "a", "amount", 5),
		op("withdraw", "acct", "b", "amount", 3), // b overdrawn: nonNegative
		op("deposit", "acct", "c", "amount", 2),
		op("open", "acct", "d"), // d opened twice: rejected
		op("withdraw", "acct", "a", "amount", 4),
	)
	rep, err := c.Check(context.Background(), steps)
	if err != nil {
		t.Fatal(err)
	}
	failing := map[string]*Result{}
	for _, a := range rep.Actors {
		if !a.OK() {
			failing[a.Actor] = a
		}
	}
	if len(rep.Actors) != 4 || len(failing) != 2 || failing["ledger acct=b"] == nil || failing["ledger acct=d"] == nil {
		t.Fatalf("want 4 partitions, b and d failing:\n%s", rep.Summary())
	}
	if b := failing["ledger acct=b"]; b.Conforms != Pass || b.Invariants[0].Verdict != Fail || b.Partition != "acct=b" {
		t.Errorf("b: want a violation of nonNegative:\n%s", b.Summary())
	}
	if d := failing["ledger acct=d"]; d.Conforms != Fail || d.Failure == nil || d.Failure.Index != 1 {
		t.Errorf("d: want its second step rejected:\n%s", d.Summary())
	}
	sum := rep.Summary()
	for _, want := range []string{"actor ledger, partitioned by acct: 2 of 4 partitions pass (5 steps)", "actor ledger acct=b: 2 steps", "actor ledger acct=d: 2 steps"} {
		if !strings.Contains(sum, want) {
			t.Errorf("the summary lacks %q:\n%s", want, sum)
		}
	}
	if strings.Contains(sum, "actor ledger acct=a:") {
		t.Errorf("a passing partition printed in full:\n%s", sum)
	}
	// the log judged whole: its first failure (b's) and nothing after
	w := ledgerChecker(t, "ledger_whole.yaml")
	wr, err := w.Check(context.Background(), steps)
	if err != nil {
		t.Fatal(err)
	}
	if wr.OK() || len(wr.Actors) != 1 || wr.Actors[0].Conforms != Fail || wr.Actors[0].Failure.Index != 7 {
		t.Errorf("the whole log: want one actor rejected at d's second open (step 8), its violation by b's withdrawal before it not reported apart:\n%s", wr.Summary())
	}
	// a failing partition is navigated by its name, in the whole's instance
	n, err := c.Nav(NavStart{Steps: steps, Actor: "ledger acct=d", At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.from, "acct=d's state after 1 of its 2 steps") {
		t.Errorf("nav: %s", n.from)
	}
}

// The model's factoring is checked on the run's evaluation, never
// assumed: a step that touches another entity's entry after that
// entity's step wrote it, a step that reads a map whole, and an
// invariant over every entity at once are each refused with the step
// and the access named.
func TestPartitionRefusesWhatDoesNotFactor(t *testing.T) {
	t.Parallel()
	c := ledgerChecker(t, "ledger.yaml")
	base := []([]any){op("open", "acct", "a"), op("open", "acct", "b"), op("deposit", "acct", "a", "amount", 5)}
	for _, tc := range []struct {
		name, file string
		more       [][]any
		want       string
	}{
		{"a transfer", "ledger.yaml", [][]any{op("transfer", "acct", "a", "dest", "b", "amount", 2)},
			`step 4 (transfer, seq 4) of actor ledger acct=a reads bal["b"], which step 2 (open, seq 2) of actor ledger acct=b wrote before it`},
		{"a census", "ledger.yaml", [][]any{op("census", "acct", "a", "n", 2)},
			"actor ledger acct=a, step 4 (census, seq 4): bal read whole (keys): it depends on every entity's state"},
		{"a sum", "ledger_total.yaml", nil, "the vals judged in the start's state: bal read whole (keys)"},
		{"another's entry in a val", "ledger_cross.yaml", nil, `a judged val (an invariant, an eventual val) of actor ledger acct=b reads bal["a"], which step 1 (open, seq 1) of actor ledger acct=a writes`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := c
			if tc.file != "ledger.yaml" {
				ch = ledgerChecker(t, tc.file)
			}
			_, err := ch.Check(context.Background(), ledgerLog(append(base, tc.more...)...))
			if err == nil || !strings.Contains(err.Error(), "the model does not factor by acct") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want the refusal naming %q, got %v", tc.want, err)
			}
		})
	}
	// a step that names no entity
	_, err := c.Check(context.Background(), ledgerLog(op("open", "acct", "a"), op("census", "n", 1)))
	if err == nil || !strings.Contains(err.Error(), "has no acct argument") {
		t.Errorf("a step without the partition's argument: %v", err)
	}
}

// The parts are replayed in the whole log's instance, but for the
// constants no evaluation read: those take the first part's value (a
// day's ACCTS lists every account, which each failing part's quint
// replay typechecked).
func TestPartitionKeepsTheConstantsRead(t *testing.T) {
	t.Parallel()
	c := ledgerChecker(t, "ledger.yaml")
	steps := ledgerLog(op("open", "acct", "a"), op("open", "acct", "b"), op("deposit", "acct", "b", "amount", 7), op("deposit", "acct", "a", "amount", 3))
	traces, _ := reconstruct(c.Binding, steps)
	pt, err := c.partition(traces[0])
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range pt.plans {
		if p.Constants["LIMIT"] != "7" || p.Constants["ACCTS"] != `Set("a")` {
			t.Errorf("part %d: LIMIT (read by deposit: the whole's) %s, ACCTS (read by none: the first part's) %s", i, p.Constants["LIMIT"], p.Constants["ACCTS"])
		}
	}
}

// A partitioned verdict is the whole log's, on random logs small enough
// for the whole to be judged too: where the model is checked to factor,
// the log passes partitioned exactly when it passes whole, and the whole
// log's first failure is its entity's failure; transfers (which make two
// accounts' histories one) are refused when they make it so.
func TestPartitionAgreesWithWhole(t *testing.T) {
	t.Parallel()
	part, whole := ledgerChecker(t, "ledger.yaml"), ledgerChecker(t, "ledger_whole.yaml")
	n := 24
	if testing.Short() {
		n = 10
	}
	rng := rand.New(rand.NewPCG(19, 2026))
	accts := []string{"a", "b", "c"}
	runs := make([][]qtrace.Step, n)
	for i := range runs {
		var ops [][]any
		for _, a := range accts {
			if rng.IntN(6) > 0 { // now and then an account never opened
				ops = append(ops, op("open", "acct", a), op("deposit", "acct", a, "amount", 3))
			}
		}
		for range 3 + rng.IntN(5) {
			a := accts[rng.IntN(len(accts))]
			switch k := rng.IntN(20); {
			case k < 1:
				ops = append(ops, op("open", "acct", a))
			case k < 10:
				ops = append(ops, op("deposit", "acct", a, "amount", 1+rng.IntN(3)))
			case k < 18:
				ops = append(ops, op("withdraw", "acct", a, "amount", 1+rng.IntN(2)))
			default:
				ops = append(ops, op("transfer", "acct", a, "dest", accts[rng.IntN(len(accts))], "amount", 1+rng.IntN(2)))
			}
		}
		rng.Shuffle(len(ops), func(i, j int) { // opens after their deposits, sometimes
			if rng.IntN(len(ops)*3) == 0 {
				ops[i], ops[j] = ops[j], ops[i]
			}
		})
		runs[i] = ledgerLog(ops...)
	}
	// and one whose transfer makes b's partition fail where the whole passes
	runs = append(runs, ledgerLog(op("open", "acct", "a"), op("open", "acct", "b"), op("deposit", "acct", "a", "amount", 3),
		op("transfer", "acct", "a", "dest", "b", "amount", 2), op("withdraw", "acct", "b", "amount", 2)))
	wreps, werrs := whole.CheckBatch(context.Background(), runs, 0)
	preps, perrs := part.CheckBatch(context.Background(), runs, 0)
	refused, agreedPass, agreedFail := 0, 0, 0
	for i := range runs {
		if werrs[i] != nil {
			t.Fatalf("run %d whole: %v", i, werrs[i])
		}
		if perrs[i] != nil {
			if !strings.Contains(perrs[i].Error(), "does not factor") {
				t.Fatalf("run %d partitioned: %v", i, perrs[i])
			}
			refused++
			continue
		}
		w, p := wreps[i], preps[i]
		if w.OK() != p.OK() {
			t.Fatalf("run %d: whole OK %v, partitioned OK %v\nwhole:\n%s\npartitioned:\n%s", i, w.OK(), p.OK(), w.Summary(), p.Summary())
		}
		if w.OK() {
			agreedPass++
			continue
		}
		agreedFail++
		// the whole's first failure, by its step's account
		r := w.Actors[0]
		var at *qtrace.Step
		if r.Conforms == Fail {
			at = r.Failure.Step
		}
		for _, inv := range r.Invariants {
			if inv.Verdict == Fail && inv.Failure != nil && at == nil {
				at = inv.Failure.Step
			}
		}
		if at == nil {
			t.Fatalf("run %d: the whole's failure names no step:\n%s", i, w.Summary())
		}
		name := fmt.Sprintf("ledger acct=%v", at.Args["acct"])
		for _, a := range p.Actors {
			if a.Actor == name && a.OK() {
				t.Errorf("run %d: the whole fails at %s, which partition %s passes:\n%s\n%s", i, at, name, w.Summary(), p.Summary())
			}
		}
	}
	t.Logf("%d runs: %d refused, %d passing both ways, %d failing both ways", len(runs), refused, agreedPass, agreedFail)
	if refused == 0 || agreedPass == 0 || agreedFail == 0 {
		t.Errorf("want runs of each kind: %d refused, %d passing, %d failing", refused, agreedPass, agreedFail)
	}
}

// A day's log judged partitioned in about linear time: 10^4 steps
// (3,334 charges' worth of accounts) within a budget, and 10^5 in the
// nightly (GOTOGETHER_SCALE_STEPS=100000). A log judged whole costs the
// sum of its states' sizes (a forall over every account after every
// step): 10^4 steps of payments took over 20 minutes so (I19).
//
// The Go side's cost alone: quint's audit of a sample of the passes is
// one process however long the log (maxSampledParts), whose time is the
// machine's load more than the log's. Not parallel, for its Setenv.
func TestPartitionAtVolume(t *testing.T) {
	if m := (Options{}).evalMode(); m != "go" {
		t.Skipf("GOTOGETHER_EVAL=%s: quint judges every partition, and the budget is the Go evaluator's (the other partition tests run so)", m)
	}
	t.Setenv("GOTOGETHER_EVAL_SAMPLE", "0")
	c := ledgerChecker(t, "ledger.yaml")
	n, budget := 10_000, 30*time.Second // about 2 s here
	if s := os.Getenv("GOTOGETHER_SCALE_STEPS"); s != "" {
		var err error
		if n, err = strconv.Atoi(s); err != nil {
			t.Fatalf("GOTOGETHER_SCALE_STEPS=%s: %v", s, err)
		}
		budget = max(budget, time.Duration(n/2000)*time.Second) // 100,000 steps: 50 s (about 5 here)
	}
	var ops [][]any
	for i := 0; len(ops) < n; i++ {
		a := fmt.Sprintf("acct-%d", i)
		ops = append(ops, op("open", "acct", a), op("deposit", "acct", a, "amount", 5), op("withdraw", "acct", a, "amount", 3))
	}
	steps := ledgerLog(ops[:n]...)
	start := time.Now()
	rep, err := c.Check(context.Background(), steps)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() || len(rep.Actors) != (n+2)/3 {
		t.Fatalf("want %d passing partitions:\n%.2000s", (n+2)/3, rep.Summary())
	}
	t.Logf("%d steps, %d partitions: %v", n, len(rep.Actors), took)
	if took > budget {
		t.Errorf("%d steps took %v, over the budget of %v", n, took, budget)
	}
}

// A compiled model judges a partitioned log too, with no quint: every
// failing entity, and the factoring checked by the same footprint.
func TestPartitionCompiled(t *testing.T) {
	t.Parallel()
	skip.Quint(t) // to compile
	bp, cp := compiledCopy(t, "ledger", "ledger.qnt")
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir(), NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := c.Check(context.Background(), ledgerLog(op("open", "acct", "a"), op("open", "acct", "b"),
		op("withdraw", "acct", "a", "amount", 1), op("open", "acct", "b"), op("deposit", "acct", "a", "amount", 4)))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Actors) != 2 || rep.Actors[0].OK() || rep.Actors[1].OK() || rep.Actors[0].Invariants[0].Verdict != Fail || rep.Actors[1].Conforms != Fail {
		t.Errorf("want a violating and b rejected, both by the compiled model:\n%s", rep.Summary())
	}
	_, err = c.Check(context.Background(), ledgerLog(op("open", "acct", "a"), op("census", "acct", "a", "n", 1)))
	if err == nil || !strings.Contains(err.Error(), "does not factor by acct") {
		t.Errorf("a census under the compiled model: %v", err)
	}
}
