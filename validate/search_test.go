package validate

import (
	"fmt"
	"os/exec"
	"strings"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

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
		got, cut := resolutions(tr, settles, 1<<20)
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
	if _, err := fmt.Sscanf(msg[strings.Index(msg, "conformed in ")+len("conformed in "):], "%d", &tried); err != nil || strings.Contains(msg, "no verdict") {
		t.Fatalf("want a proof of no order: %s", msg)
	}
	if tried >= 250 {
		t.Errorf("%d orders tried, want the prefixes of one state merged (146; 456 unmerged): %s", tried, msg)
	}
	t.Log(msg)
}
