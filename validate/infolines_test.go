package validate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// The Linearize hint is for a failing actor whose own steps are a
// client's history (invocation times, no domain clock): not for one
// whose steps a domain clock orders (a store's commits, a processor's
// calls, placed where they took effect), nor for one beside another
// actor's client history (grist's store-side binding, its clients' calls
// another binding's).
func TestLinearizeHintOnlyWhereItApplies(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/account.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * time.Millisecond) }
	step := func(actor, rec, action string, amount, balance int64, invoked, done int, order bool) qtrace.Step {
		s := qtrace.Step{Action: action, Recorder: rec, Seq: 1, Thread: rec, Actor: actor, Process: "p", Time: at(done),
			Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
		if invoked >= 0 {
			s.Invoked = at(invoked)
		}
		if order {
			s.Order = map[string]int64{"lamport": int64(done)}
		}
		return s
	}
	// a withdrawal returns before the deposit that covers it took effect
	client := func(actor string, order bool) []qtrace.Step {
		return []qtrace.Step{step(actor, actor+"w", "withdraw", 5, 0, 0, 10, order), step(actor, actor+"d", "deposit", 5, 5, 0, 20, order)}
	}
	hinted := func(steps []qtrace.Step) (bool, string) {
		j, err := c.Judge(context.Background(), steps, SearchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if j.Report.OK() {
			t.Fatalf("want a failing run:\n%s", j.Report.Summary())
		}
		for _, a := range j.Report.Actors {
			for _, i := range a.Issues {
				if i.Kind == "linearize" {
					return true, j.Report.Summary()
				}
			}
		}
		return false, j.Report.Summary()
	}
	if ok, sum := hinted(client("acct", false)); !ok {
		t.Errorf("a client's failing history: want the hint\n%s", sum)
	}
	if ok, sum := hinted(client("acct", true)); ok {
		t.Errorf("steps a domain clock orders: no hint\n%s", sum)
	}
	// the failing actor records where its steps take effect; another
	// actor's (passing) steps are a client history
	store := []qtrace.Step{step("store", "s", "withdraw", 5, 0, -1, 10, false)}
	other := []qtrace.Step{step("clients", "cd", "deposit", 5, 5, 0, 10, false)}
	if ok, sum := hinted(append(store, other...)); ok {
		t.Errorf("another actor's client history: no hint for the store\n%s", sum)
	}
}

// The info naming the actors a binding's actors: patterns leave out is
// in the report's Issues, not in its Summary: every run of a two-sided
// system has them (a grist run's clients on each store-side report).
func TestActorsLeftOutNotInSummary(t *testing.T) {
	t.Parallel()
	b := &binding.Binding{Actors: []string{"!*.clients"}}
	steps := []qtrace.Step{
		{Action: "a", Recorder: "r", Seq: 1, Actor: "t/1", Process: "p"},
		{Action: "a", Recorder: "c", Seq: 1, Actor: "t/1.clients", Process: "p"},
	}
	_, issues := b.Reconstruct(steps)
	rep := &Report{Issues: issues, Actors: []*Result{{Actor: "t/1", Conforms: Fail, Failure: &Failure{Kind: "disabled", Index: 0}}}}
	if len(issues) != 1 || issues[0].Kind != "actors" {
		t.Fatalf("the issue is the data's: %v", issues)
	}
	if sum := rep.Summary(); strings.Contains(sum, "left out") {
		t.Errorf("the report names the actors left out:\n%s", sum)
	}
}

// A report of one unnamed actor says "the run", not "actor default",
// which read as a setting to change; named actors keep their names.
func TestReportSummaryOneActor(t *testing.T) {
	one := &Report{Actors: []*Result{{Actor: "default", Steps: 3, Conforms: Pass}}}
	if s := one.Summary(); !strings.HasPrefix(s, "the run: 3 steps\n") {
		t.Errorf("one actor: %q", s)
	}
	two := &Report{Actors: []*Result{{Actor: "default", Steps: 3, Conforms: Pass}, {Actor: "n2", Steps: 1, Conforms: Pass}}}
	if s := two.Summary(); !strings.HasPrefix(s, "actor default: 3 steps\n") || !strings.Contains(s, "actor n2: 1 steps\n") {
		t.Errorf("two actors: %q", s)
	}
}
