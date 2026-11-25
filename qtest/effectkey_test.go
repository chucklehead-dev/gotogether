package qtest

import (
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// sends records account.yaml's deposit and withdraw, with a mail effect
// (which account.yaml compares with nothing) before or after each as the
// chooser says: runs of one model history whose effect records fall
// elsewhere in their recorders' seqs.
func sends(withdraw int) func(c qenv.Chooser) error {
	return func(c qenv.Chooser) error {
		ctx := qenv.ContextOf(c)
		step := func(action string, amount, balance int) {
			before := c.Choose("test.effectFirst", 2) == 1
			if before {
				qobs.Effect(ctx, "mail.send", "amount", amount)
			}
			qobs.Record(ctx, action, nil, "amount", amount, "obs.balance", balance)
			if !before {
				qobs.Effect(ctx, "mail.send", "amount", amount)
			}
		}
		step("deposit", 50, 50)
		step("withdraw", withdraw, 50-withdraw)
		return nil
	}
}

// A binding that compares no effects judges runs of one model history
// once, wherever their effect records fall: the doubles' effect records
// made every such run a quint replay of its own (twophase's mutants, 60
// replays where 47 had been). Each run's report still names its own
// failing step.
func TestEffectRecordsShareVerdictsOfBlindBindings(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	for _, c := range []struct {
		name     string
		withdraw int
		claim    Claim
	}{
		{"conforms", 20, Conforms()},
		{"rejects", 60, All(Rejects("withdraw"))},
	} {
		t.Run(c.name, func(t *testing.T) {
			x := System{Binding: "../validate/testdata/account.yaml", Run: sends(c.withdraw)}.Explore(t, ExploreConfig{Seeds: 12})
			x.Expect(t, c.claim)
			raw := map[string]bool{}
			for _, r := range x.Runs {
				raw[qtrace.Key(r.Steps)] = true
			}
			if len(raw) < 2 {
				t.Fatalf("every run's effect records fell in one place (%d raw keys): the test shows nothing", len(raw))
			}
			if x.Cached < len(x.Runs)-1 {
				t.Errorf("%d of %d runs took a cached verdict, %d raw keys: runs that differ only in their effect records were judged apart", x.Cached, len(x.Runs), len(raw))
			}
			for i, r := range x.Runs {
				for _, a := range r.Report.Actors {
					fs := []*validate.Failure{a.Failure}
					for _, inv := range a.Invariants {
						fs = append(fs, inv.Failure)
					}
					for _, f := range fs {
						if f == nil || f.Step == nil {
							continue
						}
						found := false
						for _, s := range r.Steps {
							if s.Recorder == f.Step.Recorder && s.Seq == f.Step.Seq && s.Action == f.Step.Action {
								found = true
							}
						}
						if !found {
							t.Errorf("run %d: its failing step %s (seq %d) is not one of its own steps", i, f.Step.Action, f.Step.Seq)
						}
					}
				}
			}
		})
	}
}

// A binding that compares effects keys them: runs whose effects differ
// are two verdicts.
func TestEffectBlindOnlyWithoutEffects(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	for path, want := range map[string]bool{
		"../validate/testdata/account.yaml": true,
		"testdata/pay.yaml":                 false,
	} {
		if got := newEffectBlind(checker(t, path, validate.Options{})).on; got != want {
			t.Errorf("%s: effect-blind %v, want %v", path, got, want)
		}
	}
}

// The key's view drops effect records and renumbers seqs past them, so a
// seq no record has stays a gap; anything else keys the run whole.
func TestEffectBlindView(t *testing.T) {
	t.Parallel()
	st := func(seq uint64, action, effect string) qtrace.Step {
		return qtrace.Step{Recorder: "r", Seq: seq, Action: action, Effect: effect}
	}
	seqs := func(steps []qtrace.Step) string {
		out := ""
		for _, s := range steps {
			out += fmt.Sprintf("%s%d ", s.Action, s.Seq)
		}
		return out
	}
	on := effectBlind{on: true}
	for _, c := range []struct {
		name         string
		steps        []qtrace.Step
		key, rebased string
	}{
		{"after", []qtrace.Step{st(1, "a", ""), st(2, "", "e"), st(3, "b", "")}, "a1 b2 ", "a1 b3 "},
		{"before", []qtrace.Step{st(1, "", "e"), st(2, "a", ""), st(3, "b", "")}, "a1 b2 ", "a2 b3 "},
		{"a gap stays one", []qtrace.Step{st(1, "a", ""), st(2, "", "e"), st(4, "b", "")}, "a1 b3 ", "a1 b4 "},
		{"no seqs", []qtrace.Step{st(0, "a", ""), st(0, "", "e")}, "a0 0 ", "a0 0 "},
		{"lost", []qtrace.Step{st(1, "a", ""), st(2, "", "e"), {Recorder: "r", Seq: 3, Lost: "tail"}}, "a1 2 3 ", "a1 2 3 "},
	} {
		key, rebased := on.view(c.steps)
		if seqs(key) != c.key || seqs(rebased) != c.rebased {
			t.Errorf("%s: key %q, rebased %q; want %q, %q", c.name, seqs(key), seqs(rebased), c.key, c.rebased)
		}
	}
	pending := []qtrace.Step{{Recorder: "r", Seq: 1, Action: "pay", Invocation: true}, st(2, "", "e")}
	if key, _ := on.view(pending); len(key) != 2 {
		t.Errorf("a pending invocation's run is keyed whole (its report keeps its effects): %d steps", len(key))
	}
	if key, _ := (effectBlind{}).view([]qtrace.Step{st(1, "a", ""), st(2, "", "e")}); len(key) != 2 {
		t.Errorf("off, the key has the effect records")
	}
}

// readsSeq finds every way a template reaches a step's seq.
func TestReadsSeq(t *testing.T) {
	t.Parallel()
	for src, want := range map[string]bool{
		"":                                    false,
		"{{.amount}}":                         false,
		"balance == {{.obs.balance}}":         false,
		"{{.step.action}}":                    false,
		`{{index .args "amount"}}`:            false,
		"{{.seq}}":                            true,
		"{{.step.seq}}":                       true,
		"{{.step}}":                           true,
		"{{.}}":                               true,
		"{{$}}":                               true,
		"{{$.seq}}":                           true,
		`{{index .step "seq"}}`:               true,
		"{{if .ok}}{{.seq}}{{end}}":           true,
		"{{with .args}}{{.amount}}{{end}}":    false,
		"{{with .step}}{{.}}{{end}}":          true,
		"{{quint .amount}} {{.step.seq | f}}": true,
		"{{.unclosed":                         true,
	} {
		if got := readsSeq(src); got != want {
			t.Errorf("readsSeq(%q) = %v, want %v", src, got, want)
		}
	}
}
