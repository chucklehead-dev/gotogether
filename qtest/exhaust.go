package qtest

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A Bound limits an exhaustive search (System.Exhaust) to the runs that
// stray from the nominal run in few choices. Option 0 of every qenv
// choice is nominal (the oldest task runs, no fault happens), so a run is
// its deviations: the choices made otherwise. A schedule choice made
// otherwise is a delay, a fault choice a fault.
type Bound struct {
	// Delays is the most schedule choices a run makes otherwise.
	Delays int
	// Faults is the most fault choices (a nemesis's crash among them) a
	// run makes otherwise.
	Faults int
	// Without are fault kinds that stay nominal, as reports print them
	// ("nemesis", "rpc.payments"), as in qenv.Without.
	Without []string
	// MaxRuns stops the search after this many runs (default 1000). A
	// search it cuts short fails the test, naming how many runs it left,
	// unless Partial.
	MaxRuns int
	// Partial accepts a search MaxRuns cuts short: a sample of the runs
	// within the bound, the fewest deviations first, which Evidence calls
	// truncated. Its passing claims are then about the runs it got to.
	Partial bool
	// Parallel runs this many at once (default GOMAXPROCS).
	Parallel int
}

// search is what an exhaustive exploration searched: its bound, and the
// runs it had left when MaxRuns cut it short.
type search struct {
	bound Bound
	left  int
}

// A deviation is a choice made otherwise than nominal: the index of the
// choice in its run, and the option taken.
type deviation struct{ at, value int }

// schedule is a run's deviations as GOTOGETHER_RUN's schedule= gives them and
// reports print them: "17:1,40:2", "" for the nominal run.
func schedule(ds []deviation) string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = fmt.Sprintf("%d:%d", d.at, d.value)
	}
	return strings.Join(parts, ",")
}

func parseSchedule(s string) ([]deviation, error) {
	var ds []deviation
	if s == "" {
		return nil, nil
	}
	for _, p := range strings.Split(s, ",") {
		a, v, ok := strings.Cut(p, ":")
		at, err1 := strconv.Atoi(a)
		value, err2 := strconv.Atoi(v)
		if !ok || err1 != nil || err2 != nil || at < 0 || value < 1 || len(ds) > 0 && at <= ds[len(ds)-1].at {
			return nil, fmt.Errorf("GOTOGETHER_RUN's schedule %q: want choice:option pairs in increasing choice order, as a report's reproduce line gives them", s)
		}
		ds = append(ds, deviation{at, value})
	}
	return ds, nil
}

// forcing makes the choices a schedule forces and every other one
// nominal. It has no entropy (qenv.EntropyOf), so a simulation outside
// qenv's (gosim's) asks for each choice, which the search can then vary.
type forcing struct {
	ds  []deviation
	i   int // the next choice's index
	k   int // the next deviation
	err error
}

// Choose implements qenv.Chooser.
func (f *forcing) Choose(kind string, n int) int {
	i := f.i
	f.i++
	if f.k >= len(f.ds) || f.ds[f.k].at != i {
		return 0
	}
	v := f.ds[f.k].value
	f.k++
	if v >= n {
		if f.err == nil {
			f.err = fmt.Errorf("qenv: schedule forces option %d at choice %d (%s), which has %d options: the run is not deterministic "+
				"(a map iterated unsorted, a goroutine or clock outside qenv) or the code changed since the schedule was found", v, i, kind, n)
		}
		return 0
	}
	return v
}

// Err is why the run could not take its schedule.
func (f *forcing) Err() error {
	if f.err == nil && f.k < len(f.ds) {
		return fmt.Errorf("qenv: the run ended after %d choices, before its schedule's choice %d: the run is not deterministic "+
			"or the code changed since the schedule was found", f.i, f.ds[f.k].at)
	}
	return f.err
}

// Exhaust runs the system once for every run within b: the nominal run,
// then each run with one deviation, then two, up to b's Delays and
// Faults, each checked against the model like an exploration's runs (the
// same claims and Evidence). Runs are told apart by their schedules, not
// seeds: a failing run's report gives its schedule, which
// GOTOGETHER_RUN runs alone. Runs with the same steps are checked by
// quint once.
//
// The search is stateless: each run replays its schedule from the start,
// which needs a deterministic system (qenv's rule for every run): its
// nominal run is made twice, and a difference fails the search, as an
// exploration's first seed does. It has
// no partial-order reduction: two schedules that only reorder independent
// tasks are both run, and both checked unless their steps are the same. A
// choice's options all count alike: a task among ten other than the
// oldest is one delay, a crash of any process one fault. A search MaxRuns
// cuts short fails the test unless the bound is Partial.
func (s System) Exhaust(t testing.TB, b Bound) *Exploration {
	t.Helper()
	if b.MaxRuns <= 0 {
		b.MaxRuns = 1000
	}
	if b.Parallel <= 0 {
		b.Parallel = runtime.GOMAXPROCS(0)
	}
	off := qenv.Rates{Kinds: map[string]float64{}}
	for _, k := range b.Without {
		off = off.Set(k, 1)
	}
	e := begin(t, s.Binding, s.opts(), s.Run, b.Parallel)
	x := e.x
	x.search = &search{bound: b}
	src := &scheduleSource{b: b, off: off, search: x.search}
	if spec, ok := runSpecFor(t, x.ref()); ok {
		if spec.kind != "schedule" {
			t.Fatalf("gotogether: GOTOGETHER_RUN names a %s, but %s is an exhaustive search: want :schedule=<choices>", spec.kind, x.ref())
		}
		x.alone = &spec
		ds, err := parseSchedule(spec.value)
		if err != nil {
			t.Fatalf("gotogether: %v", err)
		}
		src.alone = ds
	}
	e.known = append(e.known, knownCheck{"Bound.Without kind", b.Without})
	e.run(src)
	if x.search.left > 0 && !b.Partial && x.alone == nil {
		t.Errorf("gotogether: %s: the search stopped at MaxRuns (%d runs) with at least %d runs within its bound left, "+
			"so it proves nothing of them: raise MaxRuns, or lower Delays or Faults, or set Bound.Partial to accept a sample", x.ref(), len(x.Runs), x.search.left)
	}
	return x
}

// scheduleSource is every schedule within b, breadth first by the number
// of deviations (a search MaxRuns cuts short has tried every run with
// fewer), a level at a time, or the one schedule a run alone names. How
// many within b were left when MaxRuns cut the search short goes in
// search.left. The nominal run is made again for the determinism check:
// a schedule replays only on a deterministic system.
type scheduleSource struct {
	b      Bound
	off    qenv.Rates
	search *search
	alone  []deviation // GOTOGETHER_RUN's schedule, run alone

	started bool
	level   [][]deviation
	n, last int // runs planned so far, and in the last level
}

func (s *scheduleSource) next(done []*Run) []planned {
	switch {
	case !s.started && s.alone != nil:
		s.started = true
		return []planned{s.plan(s.alone, 0)}
	case !s.started:
		s.started, s.level = true, [][]deviation{nil}
	case s.alone != nil:
		return nil
	default:
		var next [][]deviation
		for i, r := range done[len(done)-s.last:] {
			if r.Err == nil {
				next = append(next, children(s.level[i], r.Choices, s.b, s.off)...)
			}
		}
		if s.search.left > 0 { // the level MaxRuns cut short: the rest of it, and these, are left
			s.search.left += len(next)
			return nil
		}
		s.level = next
	}
	if len(s.level) == 0 {
		return nil
	}
	room := s.b.MaxRuns - s.n
	if room <= 0 {
		s.search.left = len(s.level)
		return nil
	}
	if len(s.level) > room {
		s.search.left = len(s.level) - room
		s.level = s.level[:room]
	}
	out := make([]planned, len(s.level))
	for i, ds := range s.level {
		out[i] = s.plan(ds, s.n+i)
	}
	s.n, s.last = s.n+len(out), len(out)
	return out
}

// plan is the k-th run, on schedule ds.
func (s *scheduleSource) plan(ds []deviation, k int) planned {
	p := planned{
		origin:  origin{kind: "schedule", value: schedule(ds), seed: uint64(k + 1)},
		chooser: func() qenv.Chooser { return &forcing{ds: ds} },
	}
	if k == 0 {
		p.again = func(*Run) qenv.Chooser { return &forcing{ds: ds} }
	}
	return p
}

// children are the schedules one deviation further than ds, within b: a
// choice after ds's last made otherwise, in each of its other options.
// Branching only after the last deviation reaches each schedule once.
func children(ds []deviation, choices []qenv.Choice, b Bound, off qenv.Rates) [][]deviation {
	delays, faults := 0, 0
	for _, d := range ds {
		if d.at < len(choices) && qenv.IsFault(choices[d.at].Kind) {
			faults++
		} else {
			delays++
		}
	}
	from := 0
	if len(ds) > 0 {
		from = ds[len(ds)-1].at + 1
	}
	var out [][]deviation
	for at := from; at < len(choices); at++ {
		ch := choices[at]
		if ch.Kind == "entropy" {
			continue
		}
		if qenv.IsFault(ch.Kind) {
			if faults >= b.Faults || off.Of(ch.Kind) == 1 {
				continue
			}
		} else if delays >= b.Delays {
			continue
		}
		for v := 1; v < ch.N; v++ {
			out = append(out, append(slices.Clip(ds), deviation{at, v}))
		}
	}
	return out
}

// A Search is an exhaustive search's extent, in its Evidence.
type Search struct {
	Delays   int  `json:"delays"`
	Faults   int  `json:"faults"`
	Complete bool `json:"complete"`
	Left     int  `json:"left,omitempty"` // runs within the bound MaxRuns left untried: at least these (the next level's are not yet known)
}

// deviations are the run's choices made otherwise than nominal, schedule
// choices and faults alike: an exhaustive search's run, by kind.
func (r *Run) deviations() []qenv.Choice {
	var out []qenv.Choice
	for _, c := range r.Choices {
		if c.Kind != "entropy" && c.Value != 0 {
			out = append(out, c)
		}
	}
	return out
}
