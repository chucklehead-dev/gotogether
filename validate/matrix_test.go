package validate

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// The verdict-path matrix: every kind of verdict a binding can ask for,
// judged by every consumer of a report. The bug most often found by the
// reviews was a verdict kind some consumer never judged (an eventual val
// the search's confirmation ignored, a within the key left out, a cut
// hidden search read as a rejection). Each feature is a pair of runs that
// differ only in what the feature judges: the passing one must pass and
// the failing one fail for the feature's reason, in every consumer; a
// consumer that cannot judge a feature says why (na).

// A matrixFeature is one kind of verdict, as a pair of runs.
type matrixFeature struct {
	name, binding string
	pass, fail    []qtrace.Step
	opt           SearchOptions // the search the feature needs, if any
	// failed reports whether rep fails for the feature's reason
	failed func(c *Checker, rep *Report) bool
}

// A matrixConsumer judges a run, as one of the report's consumers does.
type matrixConsumer struct {
	name  string
	judge func(t *testing.T, c *Checker, f matrixFeature, steps []qtrace.Step) *Report
	// na are the features it cannot judge, and why
	na map[string]string
}

// verdictFields are Binding's fields that make a kind of verdict, each
// the matrix's feature (or features) that cover it; otherFields are
// those that make none. A field in neither fails the matrix: a new kind of
// verdict needs its row.
// Read-only.
var (
	verdictFields = map[string][]string{
		"Actions":     {"conformance"},
		"Invariants":  {"invariant"},
		"Eventually":  {"eventual"},
		"Within":      {"within"},
		"Hidden":      {"hidden"},
		"HiddenDepth": {"hidden"},
		"Snapshot":    {"snapshot"},
		"Resume":      {"resume"},
	}
	otherFields = map[string]string{
		"Spec": "the model's file", "Module": "the model's module", "Constants": "the instance", "Intern": "naming",
		"Outcomes": "templates' values", "Restart": "an inferred step, judged as conformance", "Defs": "templates' helpers",
		"Antecedents": "vacuity, qtest's report", "Complete": "the CLI's exit (validate -require-settle), not the report", "Simulate": "the model alone (gotogether simulate)", "Dir": "the file's place",
	}
)

func matrixFeatures(t *testing.T) []matrixFeature {
	at := func(s int) time.Time { return time.Unix(0, 0).Add(time.Duration(s) * time.Second) }
	acct := func(action string, amount, balance int64) qtrace.Step {
		return qtrace.Step{Action: action, Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	seq := func(steps ...qtrace.Step) []qtrace.Step {
		for i := range steps {
			steps[i].Recorder, steps[i].Seq = "r", uint64(i+1)
			if steps[i].Time.IsZero() {
				steps[i].Time = at(i)
			}
		}
		return steps
	}
	settled := qtrace.Step{Action: "settled", Settled: true}
	restore := func(balance int64) qtrace.Step {
		return qtrace.Step{Action: "restore", Args: map[string]any{"b": balance, "n": int64(1)}}
	}
	timed := func(action string, s int) qtrace.Step { return qtrace.Step{Action: action, Time: at(s)} }
	invariant := func(name string) func(*Checker, *Report) bool {
		return func(_ *Checker, rep *Report) bool {
			for _, a := range rep.Actors {
				for _, inv := range a.Invariants {
					if inv.Name == name && inv.Verdict == Fail {
						return true
					}
				}
			}
			return false
		}
	}
	nonconforming := func(_ *Checker, rep *Report) bool { return len(rep.Actors) > 0 && rep.Actors[0].Conforms == Fail }
	ms := func(u int) time.Time { return time.Unix(0, int64(u)*int64(time.Millisecond)) }
	pending := func(balance int64) []qtrace.Step {
		return []qtrace.Step{
			{Action: "deposit", Recorder: "c", Seq: 1, Time: ms(1), Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
			{Action: "withdraw", Recorder: "c", Seq: 2, Time: ms(2), Invocation: true, Args: map[string]any{"amount": int64(3)}},
			{Action: "deposit", Recorder: "c", Seq: 3, Time: ms(3), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": balance}},
		}
	}
	return []matrixFeature{
		{name: "conformance", binding: "testdata/account.yaml",
			pass: seq(acct("deposit", 5, 5)), fail: seq(acct("deposit", 5, 6)), failed: nonconforming},
		{name: "invariant", binding: "testdata/account.yaml",
			pass: seq(acct("deposit", 50, 50)), fail: seq(acct("deposit", 150, 150)), failed: invariant("underLimit")},
		// a settle record alone makes the eventual val judged
		{name: "eventual", binding: "testdata/funds.binding.yaml",
			pass: seq(acct("deposit", 5, 5), acct("withdraw", 5, 0)), fail: seq(acct("deposit", 5, 5), acct("withdraw", 5, 0), settled),
			failed: invariant(EventuallyName("funded"))},
		// the elect's time alone
		{name: "within", binding: "testdata/within.yaml",
			pass: seq(timed("crash", 0), timed("heal", 1), timed("elect", 2)), fail: seq(timed("crash", 0), timed("heal", 1), timed("elect", 5)),
			failed: func(c *Checker, rep *Report) bool { return invariant(c.Binding.Within[0].Name())(c, rep) }},
		// the value read alone: gossiped, or never written
		{name: "hidden", binding: "testdata/gossip.yaml",
			pass: []qtrace.Step{gossipStep(1, "a", 1, nil), gossipStep(2, "b", 0, []int{1})}, fail: []qtrace.Step{gossipStep(1, "a", 1, nil), gossipStep(2, "b", 0, []int{9})},
			failed: nonconforming},
		// the deposit's balance alone: the snapshot's plus 5, or init's
		{name: "snapshot", binding: "testdata/snapshot.yaml",
			pass: seq(restore(50), acct("deposit", 5, 55)), fail: seq(restore(50), acct("deposit", 5, 5)), failed: nonconforming},
		// the withdrawal's balance alone: one a start explains, and one none
		// does, of a log that lost its head
		{name: "resume", binding: "testdata/resume.yaml",
			pass: lostHead([2]int64{5, 8}, [2]int64{-2, 6}), fail: lostHead([2]int64{5, 8}, [2]int64{-2, 7}), failed: nonconforming},
		// the last balance alone: one the pending withdrawal explains, and one
		// nothing does
		{name: "pending", binding: "testdata/account.yaml", pass: pending(12), fail: pending(99), opt: SearchOptions{ResolvePending: true},
			failed: nonconforming},
	}
}

func matrixConsumers() []matrixConsumer {
	noSearch := map[string]string{"pending": "a pending call's effect is a search's (ResolvePending): checked as recorded, it took none"}
	check := func(eval string) func(*testing.T, *Checker, matrixFeature, []qtrace.Step) *Report {
		return func(t *testing.T, c *Checker, _ matrixFeature, steps []qtrace.Step) *Report {
			cc := *c
			cc.Options.Eval = eval
			rep, err := cc.Check(context.Background(), steps)
			if err != nil {
				t.Fatal(err)
			}
			return rep
		}
	}
	judge := func(eval string, linearize bool) func(*testing.T, *Checker, matrixFeature, []qtrace.Step) *Report {
		return func(t *testing.T, c *Checker, f matrixFeature, steps []qtrace.Step) *Report {
			cc := *c
			cc.Options.Eval = eval
			opt := f.opt
			opt.Linearize = opt.Linearize || linearize
			j, err := cc.Judge(context.Background(), steps, opt)
			if err != nil {
				t.Fatal(err)
			}
			return j.Report
		}
	}
	hiddenSearch := "a search with hidden actions is refused: each candidate would be a search of their states"
	resumeSearch := "a search of a log that resumes is refused: each candidate would be a search from the resume's states"
	return []matrixConsumer{
		{name: "Check", judge: check(""), na: noSearch},
		{name: "Check (shadow)", judge: check("shadow"), na: noSearch},
		{name: "CheckBatch", judge: func(t *testing.T, c *Checker, _ matrixFeature, steps []qtrace.Step) *Report {
			// beside another run: a batch's runs share its quint processes
			reps, errs := c.CheckBatch(context.Background(), [][]qtrace.Step{steps, steps[:1]}, 0)
			if errs[0] != nil {
				t.Fatal(errs[0])
			}
			return reps[0]
		}, na: noSearch},
		{name: "Judge", judge: judge("", false)},
		{name: "Judge (quint alone)", judge: judge("quint", false), na: map[string]string{"hidden": "hidden actions are the Go evaluator's alone: quint cannot replay a step whose choices are unknown"}},
		{name: "Judge, linearized", judge: judge("", true), na: map[string]string{"hidden": hiddenSearch, "resume": resumeSearch}},
		{name: "Judge, linearized (quint alone)", judge: judge("quint", true), na: map[string]string{"hidden": hiddenSearch, "resume": resumeSearch}},
	}
}

func TestVerdictMatrix(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	features, consumers := matrixFeatures(t), matrixConsumers()
	// every field of Binding is a kind of verdict the matrix covers, or
	// says why it makes none
	covered := map[string]bool{}
	for _, f := range features {
		covered[f.name] = true
	}
	bt := reflect.TypeFor[binding.Binding]()
	for i := range bt.NumField() {
		name := bt.Field(i).Name
		fs, verdict := verdictFields[name]
		if _, other := otherFields[name]; !verdict && !other {
			t.Errorf("binding.Binding.%s: neither a kind of verdict the matrix covers (verdictFields) nor one that makes none (otherFields): give it its row", name)
		}
		for _, f := range fs {
			if !covered[f] {
				t.Errorf("binding.Binding.%s: no feature %q in the matrix", name, f)
			}
		}
	}
	for _, f := range features {
		c, err := NewChecker(f.binding, Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		// the key: the failing run is never the passing run's verdict
		if c.Key(f.pass) == c.Key(f.fail) {
			t.Errorf("%s: the passing and the failing run have one key: a cache would give one the other's verdict", f.name)
		}
		for _, k := range consumers {
			if why, ok := k.na[f.name]; ok {
				if why == "" {
					t.Errorf("%s by %s: n/a without a reason", f.name, k.name)
				}
				continue
			}
			t.Run(f.name+" by "+k.name, func(t *testing.T) {
				t.Parallel()
				if rep := k.judge(t, c, f, f.pass); !rep.OK() {
					t.Errorf("the passing run fails:\n%s", rep.Summary())
				}
				if rep := k.judge(t, c, f, f.fail); rep.OK() || !f.failed(c, rep) {
					t.Errorf("the failing run does not fail by its %s (OK %v):\n%s", f.name, rep.OK(), rep.Summary())
				}
			})
		}
	}
	// a consumer's n/a names a feature there is
	for _, k := range consumers {
		for name := range k.na {
			if !covered[name] {
				t.Errorf("%s: n/a for %q, no feature of the matrix", k.name, name)
			}
		}
	}
}
