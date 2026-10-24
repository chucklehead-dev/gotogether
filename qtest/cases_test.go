package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// later schedules the last task offered: race's withdrawal runs first.
type later struct{}

func (later) Choose(kind string, n int) int {
	if qenv.IsFault(kind) {
		return 0
	}
	return n - 1
}

// Judge runs a system once and checks it, outside any exploration: the
// test's explorations keep their numbers.
func TestJudge(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	if r := sys.Judge(t, qenv.Nominal{}); r.Err != nil || !r.OK() {
		t.Errorf("the nominal run: %v %v", r.Err, r.Report.Summary())
	}
	r := sys.Judge(t, later{})
	if r.Err != nil || r.OK() || !Rejects("withdraw").Holds(r) || Conforms().Holds(r) {
		t.Errorf("the withdrawal first: %v %s", r.Err, r.Report.Summary())
	}
	if x := sys.Explore(t, ExploreConfig{Seeds: 1, Chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }}); x.ref() != t.Name()+"#1" {
		t.Errorf("Judge took an exploration's number: %s", x.ref())
	}
}

// CheckCases refuses what a check of cases ignores, and with no binding
// what only a check of the model uses.
func TestCheckCasesRefuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		binding string
		opt     Option
		want    string
	}{
		{"../validate/testdata/account.yaml", Linearize(), "Linearize() does nothing in w: a case's steps are checked as it records them, in that order"},
		{"../validate/testdata/account.yaml", LinearizeByKey("amount"), "LinearizeByKey() does nothing in w: a case's steps are checked as it records them, in that order"},
		{"../validate/testdata/account.yaml", SkipIfEmpty(), "SkipIfEmpty() does nothing in w"},
		{"", Constants(map[string]string{"LIMIT": "3"}), "Constants() does nothing in w: no case is checked against the model"},
	} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
			defer close(done)
			CheckCases(f, "w", c.binding, c.opt)
		}()
		<-done
		if all := strings.Join(f.errs, "\n"); !strings.Contains(all, c.want) {
			t.Errorf("errors %q, want %q", all, c.want)
		}
	}
}

// A run's ID is how GOTOGETHER_RUN names it after its exploration.
func TestRunID(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		r    Run
		want string
	}{
		{Run{origin: origin{kind: "seed", seed: 17}}, "seed=17"},
		{Run{origin: origin{kind: "schedule", value: "17:1", seed: 3}}, "schedule=17:1"},
		{Run{origin: origin{kind: "schedule", seed: 1}}, "schedule="}, // an exhaustive search's nominal run
		{Run{origin: origin{kind: "corpus", value: "/tmp/corpus/TestX/f.json", seed: 1}}, "corpus=f.json"},
		{Run{}, ""},
	} {
		if got := c.r.ID(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.r, got, c.want)
		}
	}
}
