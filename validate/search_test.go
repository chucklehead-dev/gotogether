package validate

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// On small random traces of two recorders, with pending calls and maybe a
// settle, resolutions enumerates every placement a brute force allows,
// once each, the fewest effects first: a call takes effect after every
// step surely recorded before its invocation, and before every step
// surely recorded after a settle that followed it (the system was quiet
// there), whatever order the actor's steps are in; surely: on one
// recorder by seq, across recorders by more than RealTimeSlack.
func TestResolutionsAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	t0 := time.Unix(0, 0)
	compared, settled, crossed := 0, 0, 0
	for trial := range 2000 {
		recs := []string{"a", "b"}
		seq := map[string]uint64{}
		var steps, pending, settles []qtrace.Step
		at := 0
		for range 1 + r.IntN(6) {
			at += r.IntN(3) // equal times: concurrent across recorders
			rec := recs[r.IntN(2)]
			seq[rec]++
			s := qtrace.Step{Action: "op", Recorder: rec, Seq: seq[rec], Time: t0.Add(time.Duration(at) * 500 * time.Microsecond)}
			switch r.IntN(6) {
			case 0:
				s.Invocation = true
				pending = append(pending, s)
			case 1:
				s.Settled = true
				settles = append(settles, s)
			default:
				steps = append(steps, s)
			}
		}
		if len(pending) == 0 || len(pending) > 2 {
			continue
		}
		// the actor's order, as a search gets it: Reconstruct's
		all := append(append(slices.Clone(steps), pending...), settles...)
		traces, _ := qtrace.Reconstruct(all)
		if len(traces) != 1 {
			t.Fatalf("trial %d: %d traces", trial, len(traces))
		}
		tr := traces[0]
		steps, pending = tr.Steps, tr.Pending
		// the settles a search is given: those no other recorder worked past
		// (times 0.5 ms apart, so within RealTimeSlack and beyond it)
		settles = settlesOf(all)
		got, cut, crossing := resolutions(tr, settles, 1<<20)
		compared++
		if len(settles) > 0 {
			settled++
		}
		if cut {
			t.Fatalf("trial %d: cut", trial)
		}
		// brute force, from the definitions: a step surely precedes another
		// on its recorder by seq, across recorders by more than the slack
		// of the machines' clocks; a call takes effect after every step
		// surely before its invocation, and before every step surely after
		// a settle surely after it (the system was quiet then)
		slack := qtrace.RealTimeSlack
		surely := func(a, b qtrace.Step) bool {
			if a.Recorder == b.Recorder {
				return a.Seq < b.Seq
			}
			return b.Time.Sub(a.Time) > slack
		}
		allowed := func(p qtrace.Step, pos int) bool {
			for j, s := range steps {
				if surely(s, p) && pos <= j {
					return false
				}
				for _, st := range settles {
					if surely(p, st) && surely(st, s) && pos > j {
						return false
					}
				}
			}
			return true
		}
		var want []placement
		var walk func(i int, cur placement)
		walk = func(i int, cur placement) {
			if i == len(pending) {
				want = append(want, slices.Clone(cur))
				return
			}
			walk(i+1, append(cur, -1))
			for pos := 0; pos <= len(steps); pos++ {
				if allowed(pending[i], pos) {
					walk(i+1, append(cur, pos))
				}
			}
		}
		walk(0, nil)
		if len(want) == 1 {
			crossed++ // no call could take effect anywhere
		}
		// a call crosses exactly when no allowed placement gives it an effect
		for i := range pending {
			never := !slices.ContainsFunc(want, func(p placement) bool { return p[i] >= 0 })
			if never != slices.Contains(crossing, i) {
				t.Fatalf("trial %d: call %d never takes effect %v, reported crossing %v", trial, i, never, crossing)
			}
		}
		key := func(ps []placement) []string {
			var out []string
			for _, p := range ps {
				out = append(out, fmt.Sprint(p))
			}
			slices.Sort(out)
			return out
		}
		if !slices.Equal(key(got), key(want)) {
			t.Fatalf("trial %d: steps %v, pending %v, settles %v:\nresolutions %v\nbrute force %v", trial, show(steps), show(pending), show(settles), key(got), key(want))
		}
		effects := func(p placement) int {
			n := 0
			for _, pos := range p {
				if pos >= 0 {
					n++
				}
			}
			return n
		}
		for i := 1; i < len(got); i++ {
			if effects(got[i]) < effects(got[i-1]) {
				t.Fatalf("trial %d: %v before %v: not the fewest effects first", trial, got[i-1], got[i])
			}
		}
	}
	t.Logf("%d traces compared, %d with a quiet settle, %d whose calls could take effect nowhere", compared, settled, crossed)
}

func show(steps []qtrace.Step) []string {
	var out []string
	for _, s := range steps {
		out = append(out, fmt.Sprintf("%s%d@%d", s.Recorder, s.Seq, s.Time.UnixMilli()))
	}
	return out
}

// Five concurrent deposits that observe nothing, then a withdrawal no
// order allows: proving it takes every order of the deposits by prefix,
// but every prefix of the same deposits has one balance, so by model
// state one prefix a set of deposits suffices: 146 orders tried, the
// guided search's among them, where without the states it took 456.
func TestLinearizeMergesPrefixesOfOneState(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	if testing.Short() {
		t.Skip("searches a few hundred orders with quint")
	}
	c, err := NewChecker("testdata/blind.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * 10 * time.Millisecond) }
	var steps []qtrace.Step
	for i, amount := range []int64{1, 2, 4, 8, 16} {
		rec := fmt.Sprint("c", i)
		steps = append(steps, qtrace.Step{Action: "deposit", Recorder: rec, Seq: 1, Thread: rec, Actor: "acct", Process: "p",
			Invoked: at(0), Time: at(10), Args: map[string]any{"amount": amount}, Obs: map[string]any{}})
	}
	steps = append(steps, qtrace.Step{Action: "withdraw", Recorder: "w", Seq: 1, Thread: "w", Actor: "acct", Process: "p",
		Invoked: at(20), Time: at(30), Args: map[string]any{"amount": int64(100)}, Obs: map[string]any{"balance": int64(0)}})
	s, err := c.Search(steps, SearchOptions{Linearize: true, Budget: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var msg string
	for _, i := range s.issues {
		if i.Kind == "linearize" {
			msg = i.Msg
		}
	}
	var tried int
	i := strings.Index(msg, "conformed in ")
	if i < 0 || strings.Contains(msg, "no verdict") {
		t.Fatalf("want a proof of no order: %s", msg)
	}
	if _, err := fmt.Sscanf(msg[i+len("conformed in "):], "%d", &tried); err != nil {
		t.Fatalf("want a proof of no order: %s", msg)
	}
	if tried >= 250 {
		t.Errorf("%d orders tried, want the prefixes of one state merged (146; 456 unmerged): %s", tried, msg)
	}
	t.Log(msg)
}

// A pending call recorded before a settle on its recorder, but timed after
// another recorder's step that its settle precedes: its bounds cross,
// which only clocks that disagree make, and resolutions says so (the call
// can have had no effect).
func TestResolutionsCrossing(t *testing.T) {
	ms := func(n int) time.Time { return time.Unix(0, 0).Add(time.Duration(n) * time.Millisecond) }
	p := qtrace.Step{Action: "op", Recorder: "b", Seq: 1, Time: ms(100), Invocation: true}
	st := qtrace.Step{Action: "settled", Recorder: "b", Seq: 2, Time: ms(0), Settled: true}
	s := qtrace.Step{Action: "op", Recorder: "a", Seq: 1, Time: ms(50)}
	got, _, crossing := resolutions(&qtrace.Trace{Steps: []qtrace.Step{s}, Pending: []qtrace.Step{p}}, []qtrace.Step{st}, 100)
	if !slices.Equal(crossing, []int{0}) || len(got) != 1 || got[0][0] != -1 {
		t.Errorf("placements %v, crossing %v: want only no effect, and the call crossing", got, crossing)
	}
}

// On small histories of a binding that infers restarts, interns ids and
// sizes a constant from the history, the complete search alone (merging
// prefixes of one state, its candidates planned within the history) finds
// an order exactly when one of the orders real time allows, each checked
// whole, conforms. A merge by state alone once proved no order where a
// restart's process told two prefixes apart (the T2c review's B1), and
// ids interned in each candidate's order named one state two ways (B2).
func TestCompleteSearchAgainstWholeOrders(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	if testing.Short() {
		t.Skip("checks every order of each history with quint")
	}
	hooks.CompleteSearchOnly.Store(true)
	defer hooks.CompleteSearchOnly.Store(false)
	c, err := NewChecker("testdata/procs.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	at := func(n int) time.Time { return time.Unix(0, 0).Add(time.Duration(n) * 10 * time.Millisecond) }
	r := rand.New(rand.NewPCG(9, 10))
	step := func(rec, action, proc string, from, to int) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: rec, Seq: 1, Thread: rec, Actor: "x", Process: proc,
			Invoked: at(from), Time: at(to), Args: map[string]any{}, Obs: map[string]any{}}
	}
	procs := []string{"P", "Q"}
	yes, no := 0, 0
	// the reviewer's case first: inc@P, inc@P, inc@Q, then look@P after one crash
	fixed := true
	for trial := 0; trial < 16; trial++ {
		var steps []qtrace.Step
		if fixed {
			for i, p := range []string{"P", "P", "Q"} {
				s := step(fmt.Sprint("c", i), "inc", p, 0, 10)
				s.Args["id"] = fmt.Sprint("id", i)
				steps = append(steps, s)
			}
			l := step("l", "look", "P", 20, 30)
			l.Obs["count"], l.Obs["crashes"], l.Obs["seen"] = int64(3), int64(1), int64(3)
			steps = append(steps, l)
			fixed = false
		} else {
			n := 2 + r.IntN(2)
			for i := range n {
				s := step(fmt.Sprint("c", i), "inc", procs[r.IntN(2)], 0, 10)
				s.Args["id"] = fmt.Sprint("id", r.IntN(2))
				steps = append(steps, s)
			}
			l := step("l", "look", procs[r.IntN(2)], 20, 30)
			l.Obs["count"], l.Obs["crashes"], l.Obs["seen"] = int64(n), int64(r.IntN(3)), int64(1+r.IntN(2))
			steps = append(steps, l)
		}
		// every order of the incs, the look last, each checked whole
		var orders [][]qtrace.Step
		var perm func(rest, cur []qtrace.Step)
		perm = func(rest, cur []qtrace.Step) {
			if len(rest) == 0 {
				o := append(slices.Clone(cur), steps[len(steps)-1])
				for i := range o {
					o[i].Recorder, o[i].Thread, o[i].Seq = "whole", "whole", uint64(i+1)
				}
				orders = append(orders, o)
				return
			}
			for i := range rest {
				perm(append(slices.Clone(rest[:i]), rest[i+1:]...), append(slices.Clone(cur), rest[i]))
			}
		}
		perm(steps[:len(steps)-1], nil)
		reps, errs := c.CheckBatch(t.Context(), orders, 0)
		want := false
		for i := range orders {
			if errs[i] != nil {
				t.Fatalf("trial %d: %v", trial, errs[i])
			}
			// conformance alone: one order's steps on one thread show issues
			// (processes overlapping) that a search reports apart
			want = want || reps[i].Actors[0].Conforms == Pass
		}
		s, err := c.Search(steps, SearchOptions{Linearize: true, Budget: 1000})
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		got, msg := false, ""
		for _, i := range s.issues {
			if i.Kind == "linearize" {
				got, msg = strings.Contains(i.Msg, "conforms ("), i.Msg
			}
		}
		if got != want {
			t.Fatalf("trial %d: the complete search says %q; some whole order conforms: %v", trial, msg, want)
		}
		if want {
			yes++
		} else {
			no++
		}
	}
	if yes == 0 || no == 0 {
		t.Errorf("%d histories with an order, %d without: the property needs both", yes, no)
	}
}

// Two variables of one short name (a::x, b::x) keep their qualifiers in
// an annotated ITF: stripped, one overwrote the other, whichever Go's map
// order wrote last (and two states told apart by it read as one).
func TestAnnotateKeepsCollidingNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.itf.json")
	itf := `{"vars":["a::x","b::x","m::y"],"states":[{"a::x":1,"b::x":2,"m::y":3}]}`
	if err := os.WriteFile(path, []byte(itf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := annotateITF(path, &binding.Generated{}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	for _, want := range []string{`"a::x":1`, `"b::x":2`, `"y":3`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("annotated %s: want %s", out, want)
		}
	}
}

// A history real time orders alone (each step completed before the next
// was invoked) is not searched: its one order is the history's, which the
// caller checks. No quint process runs for it, so it needs no quint here.
func TestLinearizeForcedOrderIsNotSearched(t *testing.T) {
	c, err := NewChecker("testdata/blind.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Skip(err) // the binding loads its model with quint
	}
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * 10 * time.Millisecond) }
	var steps []qtrace.Step
	for i, amount := range []int64{1, 2, 4} {
		rec := fmt.Sprint("c", i)
		steps = append(steps, qtrace.Step{Action: "deposit", Recorder: rec, Seq: 1, Thread: rec, Actor: "acct", Process: "p",
			Invoked: at(20 * i), Time: at(20*i + 10), Args: map[string]any{"amount": amount}, Obs: map[string]any{}})
	}
	s, err := c.Search(steps, SearchOptions{Linearize: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.issues) == 0 || !strings.Contains(s.issues[0].Msg, "real time allows one order of its 3 steps") {
		t.Fatalf("issues %v, want the one order taken without a search", s.issues)
	}
	if _, err := os.Stat(filepath.Join(c.Options.Dir, "linearize")); err == nil {
		t.Errorf("the search wrote quint modules for a history with one order")
	}
}

// The Go evaluator decides a search as quint alone does: the same order,
// the same verdict. When it is wrong, quint's verdict stands: an evaluator
// that rejects every order finds none, which quint alone decides; one
// that accepts every order finds one quint rejects, a disagreement the
// search says before it searches again with quint.
func TestEvaluatorAgreesAndIsConfirmed(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * 10 * time.Millisecond) }
	var steps []qtrace.Step
	for i, amount := range []int64{1, 2, 4} {
		rec := fmt.Sprint("c", i)
		steps = append(steps, qtrace.Step{Action: "deposit", Recorder: rec, Seq: 1, Thread: rec, Actor: "acct", Process: "p",
			Invoked: at(0), Time: at(10), Args: map[string]any{"amount": amount}, Obs: map[string]any{}})
	}
	steps = append(steps, qtrace.Step{Action: "withdraw", Recorder: "w", Seq: 1, Thread: "w", Actor: "acct", Process: "p",
		Invoked: at(20), Time: at(30), Args: map[string]any{"amount": int64(7)}, Obs: map[string]any{"balance": int64(0)}})
	search := func(mode string) *Search {
		t.Setenv("GOTOGETHER_EVAL", mode)
		c, err := NewChecker("testdata/blind.yaml", Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		s, err := c.Search(steps, SearchOptions{Linearize: true})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	q, g := search("quint"), search("go")
	if len(q.issues) == 0 || len(g.issues) == 0 || q.issues[0].Msg != g.issues[0].Msg {
		t.Fatalf("quint: %v\nthe evaluator: %v", q.issues, g.issues)
	}
	if !strings.Contains(g.issues[0].Msg, "conforms") {
		t.Fatalf("want an order found: %v", g.issues)
	}
	defer func() { hooks.TamperEval.Store(0) }()
	hooks.TamperEval.Store(1)
	if w := search("go"); !strings.Contains(w.issues[0].Msg, "conforms") || !strings.Contains(w.issues[0].Msg, "does not confirm") {
		t.Errorf("an evaluator rejecting every order: %v, want its proof contradicted and quint's verdict", w.issues)
	}
	hooks.TamperEval.Store(2)
	steps[3].Obs["balance"] = int64(99) // no order leaves 99
	if w := search("go"); !strings.Contains(w.issues[0].Msg, "disagree") || !strings.Contains(w.issues[0].Msg, "no order") {
		t.Errorf("an evaluator accepting every order: %v, want the disagreement said and quint's verdict", w.issues)
	}
	// a right verdict of no order is confirmed by its proof, in one batch:
	// quint searches nothing again, and says what it says alone
	hooks.TamperEval.Store(0)
	q = search("quint")
	before := searchedAgain.Load()
	g = search("go")
	if again := searchedAgain.Load() - before; again != 0 || g.issues[0].Msg != q.issues[0].Msg || !strings.Contains(g.issues[0].Msg, "no order") {
		t.Errorf("searched again %d times; quint: %v\nthe evaluator: %v", again, q.issues, g.issues)
	}
}

// The evaluator judges a candidate as quint's replay of it reports: the
// same failure index (an expect, a disabled action, an invariant with
// invariants on, an eventual val at a settle, a restart's failure at the
// last step), for every order and prefix of random histories of bindings
// with invariants, an eventual val, restarts, interning and a constant
// computed from the steps.
func TestJudgeAgreesWithQuint(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	if testing.Short() {
		t.Skip("checks a few hundred candidates with quint")
	}
	at := func(n int) time.Time { return time.Unix(0, 0).Add(time.Duration(n) * 10 * time.Millisecond) }
	r := rand.New(rand.NewPCG(11, 12))
	histories := map[string]func() []qtrace.Step{
		"testdata/funds.binding.yaml": func() []qtrace.Step {
			var steps []qtrace.Step
			for i := range 2 + r.IntN(2) {
				a := []string{"deposit", "withdraw"}[r.IntN(2)]
				rec := fmt.Sprint("c", i)
				steps = append(steps, qtrace.Step{Action: a, Recorder: rec, Seq: 1, Thread: rec, Actor: "x", Process: "p", Invoked: at(0), Time: at(10),
					Args: map[string]any{"amount": int64(1 + r.IntN(60))}, Obs: map[string]any{"balance": int64(r.IntN(120))}})
			}
			if r.IntN(2) == 0 {
				steps = append(steps, qtrace.Step{Action: "settled", Settled: true, Recorder: "s", Seq: 1, Thread: "s", Actor: "x", Process: "p", Time: at(20)})
			}
			return steps
		},
		"testdata/funds.binding.yaml#drained": func() []qtrace.Step {
			// a conforming order that settles with nothing funded
			d := qtrace.Step{Action: "deposit", Recorder: "a", Seq: 1, Thread: "a", Actor: "x", Process: "p", Invoked: at(0), Time: at(10),
				Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}
			w := qtrace.Step{Action: "withdraw", Recorder: "b", Seq: 1, Thread: "b", Actor: "x", Process: "p", Invoked: at(0), Time: at(10),
				Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(0)}}
			return []qtrace.Step{d, w, {Action: "settled", Settled: true, Recorder: "s", Seq: 1, Thread: "s", Actor: "x", Process: "p", Time: at(20)}}
		},
		"testdata/procs.yaml": func() []qtrace.Step {
			var steps []qtrace.Step
			for i := range 2 + r.IntN(2) {
				rec := fmt.Sprint("c", i)
				steps = append(steps, qtrace.Step{Action: "inc", Recorder: rec, Seq: 1, Thread: rec, Actor: "x", Process: []string{"P", "Q"}[r.IntN(2)],
					Invoked: at(0), Time: at(10), Args: map[string]any{"id": fmt.Sprint("id", r.IntN(2))}, Obs: map[string]any{}})
			}
			steps = append(steps, qtrace.Step{Action: "look", Recorder: "l", Seq: 1, Thread: "l", Actor: "x", Process: []string{"P", "Q"}[r.IntN(2)],
				Invoked: at(20), Time: at(30), Args: map[string]any{}, Obs: map[string]any{"count": int64(r.IntN(4)), "crashes": int64(r.IntN(2)), "seen": int64(1 + r.IntN(2))}})
			return steps
		},
	}
	compared := 0
	for path, gen := range histories {
		c, err := NewChecker(strings.Split(path, "#")[0], Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		ev := evaluatorFor(c)
		if ev.err != nil {
			t.Fatalf("%s: %v", path, ev.err)
		}
		for trial := range 6 {
			traces, _ := qtrace.Reconstruct(gen())
			tr := traces[0]
			// every order of the steps, and every prefix of the first
			var orders [][]qtrace.Step
			var perm func(rest, cur []qtrace.Step)
			perm = func(rest, cur []qtrace.Step) {
				if len(rest) == 0 {
					o := slices.Clone(cur)
					for i := range o {
						o[i].Recorder, o[i].Thread, o[i].Seq, o[i].Call = "linearized:x", "linearized", uint64(i+1), 0
					}
					orders = append(orders, o)
					return
				}
				for i := range rest {
					perm(append(slices.Clone(rest[:i]), rest[i+1:]...), append(slices.Clone(cur), rest[i]))
				}
			}
			perm(tr.Steps, nil)
			for k := 1; k < len(orders[0]); k++ {
				orders = append(orders, orders[0][:k])
			}
			for _, inv := range []bool{false, true} {
				q, _ := quintChecker(within(c, tr), tr, "x", inv)(orders)
				for i, o := range settledAs(tr, orders) {
					f, _, err := ev.judge(within(c, tr), tr, o, len(orders[i]), inv)
					if err != nil {
						t.Fatalf("%s trial %d: the evaluator refused: %v", path, trial, err)
					}
					if f != q[i] {
						t.Errorf("%s trial %d, invariants %v, order %v: the evaluator fails at %d, quint at %d", path, trial, inv, actions(orders[i]), f, q[i])
					}
					compared++
				}
			}
		}
	}
	t.Logf("%d candidates compared", compared)
}

func actions(o []qtrace.Step) []string {
	var out []string
	for _, s := range o {
		out = append(out, fmt.Sprintf("%s%v%v@%s", s.Action, s.Args, s.Obs, s.Process))
	}
	return out
}

// Two prefixes of one model state merge only when their extensions render
// alike: the processes' numbering ({{.process}} numbers them by first
// appearance) is part of the key, not only the last one restarts read.
func TestMergeKeyHoldsTheProcessNumbering(t *testing.T) {
	ch := &Checker{Binding: &binding.Binding{Restart: "restart"}}
	at := func(action, process string) qtrace.Step { return qtrace.Step{Action: action, Process: process} }
	p1 := []qtrace.Step{at("x", "p1"), at("y", "p2"), at("z", "p2")}
	p2 := []qtrace.Step{at("y", "p2"), at("x", "p1"), at("z", "p2")}
	if withProcess(ch, "s", p1) == withProcess(ch, "s", p2) {
		t.Errorf("prefixes that number their processes otherwise share a key: %q", withProcess(ch, "s", p1))
	}
	if withProcess(ch, "", p1) != "" {
		t.Errorf("a candidate with no state has a key")
	}
}

// A proof is confirmed when quint rejects every dropped prefix and gives
// each merged pair one state; contradicted when it accepts a dropped
// prefix, rejects a merged one or gives a pair two states; unconfirmed
// when it wrote no state.
func TestConfirmsAProof(t *testing.T) {
	a, b, c := []qtrace.Step{{Action: "a"}}, []qtrace.Step{{Action: "b"}}, []qtrace.Step{{Action: "c"}}
	proof := &qtrace.Proof{Rejected: [][]qtrace.Step{a}, Merged: [][2][]qtrace.Step{{b, c}}}
	quint := func(fa int, sb, sc string) func([][]qtrace.Step) ([]int, []string) {
		return func(orders [][]qtrace.Step) ([]int, []string) {
			if len(orders) != 3 {
				t.Fatalf("%d orders in the batch, want 3", len(orders))
			}
			fs, st := []int{fa, -1, -1}, []string{"", sb, sc}
			if sb == "rejected" {
				fs[1], st[1] = 0, ""
			}
			return fs, st
		}
	}
	for _, x := range []struct {
		name    string
		quint   func([][]qtrace.Step) ([]int, []string)
		verdict int
	}{
		{"agrees", quint(0, "s", "s"), confirmed},
		{"accepts a dropped prefix", quint(-1, "s", "s"), contradicted},
		{"two states", quint(0, "s", "t"), contradicted},
		{"rejects a merged prefix", quint(0, "rejected", "s"), contradicted},
		{"no state", quint(0, "", ""), unconfirmed},
	} {
		if got := confirms(proof, x.quint); got != x.verdict {
			t.Errorf("%s: %d, want %d", x.name, got, x.verdict)
		}
	}
	if confirms(&qtrace.Proof{}, nil) != contradicted {
		t.Errorf("an empty proof confirmed")
	}
}

// With ConfirmByCheck, an order or a resolution the Go evaluator finds
// costs the search no quint process: the caller's check confirms it. When
// that check fails where the evaluator was wrong, Confirm searches again
// with quint alone, and says so; when it holds, Confirm does nothing.
func TestConfirmByCheck(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	defer func() { hooks.TamperEval.Store(0) }()
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * 10 * time.Millisecond) }
	deposits := func(balance int64) []qtrace.Step {
		var steps []qtrace.Step
		for i, amount := range []int64{1, 2, 4} {
			rec := fmt.Sprint("c", i)
			steps = append(steps, qtrace.Step{Action: "deposit", Recorder: rec, Seq: 1, Thread: rec, Actor: "acct", Process: "p",
				Invoked: at(0), Time: at(10), Args: map[string]any{"amount": amount}, Obs: map[string]any{}})
		}
		return append(steps, qtrace.Step{Action: "withdraw", Recorder: "w", Seq: 1, Thread: "w", Actor: "acct", Process: "p",
			Invoked: at(20), Time: at(30), Args: map[string]any{"amount": int64(7)}, Obs: map[string]any{"balance": balance}})
	}
	pending := func(balance int64) []qtrace.Step {
		ms := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
		return []qtrace.Step{
			{Action: "deposit", Recorder: "c", Seq: 1, Time: ms(1), Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
			{Action: "withdraw", Recorder: "c", Seq: 2, Time: ms(2), Invocation: true, Args: map[string]any{"amount": int64(3)}},
			{Action: "deposit", Recorder: "c", Seq: 3, Time: ms(3), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}},
		}
	}
	for _, x := range []struct {
		name, binding, dir string
		opt                SearchOptions
		steps              func(int64) []qtrace.Step
		good, bad          int64 // a balance some order explains, and one none does
		none               string
	}{
		{"linearize", "testdata/blind.yaml", "linearize", SearchOptions{Linearize: true}, deposits, 0, 99, "no order"},
		{"resolve", "testdata/account.yaml", "resolve", SearchOptions{ResolvePending: true}, pending, 12, 99, "no resolution"},
	} {
		t.Run(x.name, func(t *testing.T) {
			opt := x.opt
			opt.ConfirmByCheck = true
			run := func(steps []qtrace.Step) (*Checker, *Search, *Report) {
				c, err := NewChecker(x.binding, Options{Dir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				s, err := c.Search(steps, opt)
				if err != nil {
					t.Fatal(err)
				}
				rep, err := c.Check(context.Background(), s.Steps)
				if err != nil {
					t.Fatal(err)
				}
				return c, s, rep
			}
			hooks.TamperEval.Store(0)
			c, s, rep := run(x.steps(x.good))
			if _, err := os.Stat(filepath.Join(c.Options.Dir, x.dir)); err == nil {
				t.Errorf("the search ran quint for an order the evaluator found")
			}
			if again, err := s.Confirm(rep); err != nil || again != nil || !rep.OK() {
				t.Errorf("a right order: again %v, err %v, report OK %v", again, err, rep.OK())
			}
			// an evaluator that accepts every candidate: the first, which the
			// check fails, and quint, which searches again
			hooks.TamperEval.Store(2)
			c, s, rep = run(x.steps(x.bad))
			again, err := s.Confirm(rep)
			if err != nil || again == nil {
				t.Fatalf("a wrong order the check failed: again %v, err %v", again, err)
			}
			msgs := fmt.Sprint(again.issues)
			if !strings.Contains(msgs, "does not conform for quint") || !strings.Contains(msgs, x.none) {
				t.Errorf("searched again: %s, want the evaluator's error said and quint's verdict", msgs)
			}
			if rep, err = c.Check(context.Background(), again.Steps); err != nil || rep.OK() {
				t.Errorf("the search again's order passes (err %v)", err)
			}
		})
	}
}
