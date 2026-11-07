package qtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestUnknownBudgetClaimRetainsRun(t *testing.T) {
	cleanProfileEnv(t)
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	t.Setenv("GOTOGETHER_EVIDENCE", t.TempDir())
	RequireQuint(t)
	corpus := t.TempDir()
	sys := System{Binding: "../validate/testdata/account.yaml", Opts: []Option{LinearizeBudget(1)}, Run: func(c qenv.Chooser) error {
		c.Choose("diagnostic", 2)
		ctx := qenv.ContextOf(c)
		qobs.Record(ctx, "deposit", nil, "amount", 60, "obs.balance", 60)
		a := qobs.Invoke(ctx, "withdraw", "amount", 50)
		b := qobs.Invoke(ctx, "withdraw", "amount", 50)
		d := qobs.Invoke(ctx, "deposit", "amount", 1)
		d.Done(nil, "obs.balance", 61)
		a.Done(nil, "obs.balance", 10)
		b.Done(nil, "obs.balance", -40)
		return nil
	}}
	x := sys.Explore(t, ExploreConfig{Seeds: 2, Corpus: corpus})
	before, err := json.Marshal(x.Evidence())
	if err != nil {
		t.Fatal(err)
	}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB ends this goroutine like testing.T
		defer close(done)
		x.Expect(f, Conforms())
	}()
	<-done
	msg := strings.Join(f.errs, "\n")
	for _, want := range []string{"2 of 2 runs have no verdict", "linearize budget", "seed 1", "step log:", "the order judged", "saved to the corpus:", "reproduce:", "navigate:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in unknown diagnostic:\n%s", want, msg)
		}
	}
	if !f.failed {
		t.Fatal("unknown conformance passed")
	}
	after, err := json.Marshal(x.Evidence())
	if err != nil || string(before) != string(after) {
		t.Fatalf("retaining diagnostics changed evidence: %v\nbefore %s\nafter %s", err, before, after)
	}
	evidenceFiles, err := filepath.Glob(filepath.Join(evidenceDir(t.Name()), "x*.json"))
	if err != nil || len(evidenceFiles) != 1 {
		t.Fatalf("machine-readable evidence files %v: %v", evidenceFiles, err)
	}
	evidenceData, err := os.ReadFile(evidenceFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	var evidence Evidence
	if err := json.Unmarshal(evidenceData, &evidence); err != nil || evidence.Runs != 2 || evidence.Conform != 0 || evidence.FailingBy["no verdict (linearize budget)"] != 2 {
		t.Fatalf("unknown machine-readable judgment changed: %+v, %v", evidence, err)
	}
	for _, r := range x.Runs {
		if decidedFailing([]*Run{r}) || r.Report.Actors[0].Conforms != validate.NotEvaluated || Rejects("withdraw").value(r.Report) != unknown {
			t.Fatalf("unknown run became a model refutation: %s", r.Report.Summary())
		}
	}
	files, err := filepath.Glob(filepath.Join(corpus, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("corpus entries %v: %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var entry corpusEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Seed != 1 || entry.Exploration != x.ref() || !reflect.DeepEqual(entry.Choices, x.Runs[0].Choices) || len(entry.Choices) == 0 {
		t.Fatalf("wrong unknown run retained: %+v", entry)
	}
	var history string
	for line := range strings.SplitSeq(msg, "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "step log: "); ok {
			history = path
		}
	}
	data, err = os.ReadFile(history)
	if err != nil {
		t.Fatalf("retained history: %v", err)
	}
	steps, err := qtrace.ReadJSONL(strings.NewReader(string(data)))
	if err != nil || len(steps) != len(x.Runs[0].Recorded) {
		t.Fatalf("retained history has %d steps: %v", len(steps), err)
	}
	// A single retained entry can be replayed without repeating the campaign;
	// it still has no verdict at the same search bound.
	rerun(t)
	t.Setenv("GOTOGETHER_RUN", x.ref()+":corpus="+filepath.Base(files[0]))
	replayed := sys.Explore(t, ExploreConfig{Seeds: 2, Corpus: corpus})
	if len(replayed.Runs) != 1 || replayed.Runs[0].origin.kind != "corpus" ||
		!reflect.DeepEqual(replayed.Runs[0].Choices, entry.Choices) ||
		replayed.Runs[0].Report.Actors[0].Conforms != validate.NotEvaluated {
		t.Fatalf("retained unknown run did not replay alone: %+v", replayed.Runs)
	}
}

func TestUnknownDiagnosticSelectsClaimResponsibleRun(t *testing.T) {
	for _, kind := range []string{"all", "none"} {
		t.Run(kind, func(t *testing.T) {
			// The first run lacks a model verdict but its lifecycle is known.
			// Only the second run's lost tail leaves this claim unknown.
			first := &validate.Report{Actors: []*validate.Result{{Actor: "unjudged-model", Conforms: validate.NotEvaluated, Settled: kind == "all"}}}
			second := &validate.Report{Actors: []*validate.Result{{Actor: "lost-tail", Conforms: validate.Pass, Lost: []qtrace.Lost{{Recorder: "cut"}}}}}
			claim := All(Settles())
			if kind == "none" {
				claim = None(Settles())
			}
			x := &Exploration{test: "TestClaimUnknown", id: 1, dir: t.TempDir(), corpus: t.TempDir(), Runs: []*Run{
				{origin: origin{kind: "seed", seed: 11}, Report: first, Choices: []qenv.Choice{{Kind: "diagnostic", N: 2}}},
				{origin: origin{kind: "seed", seed: 22}, Report: second, Choices: []qenv.Choice{{Kind: "diagnostic", N: 3}}},
			}}
			v := x.verdict(claim, x.reports())
			if v.ok {
				t.Fatal("unknown lifecycle claim passed")
			}
			msg := v.say(t)
			if !strings.Contains(msg, "seed 22") || strings.Contains(msg, "seed 11") {
				t.Fatalf("described an unrelated unknown run:\n%s", msg)
			}
			files, err := filepath.Glob(filepath.Join(x.corpus, "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("corpus entries %v: %v", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var entry corpusEntry
			if err := json.Unmarshal(data, &entry); err != nil || entry.Seed != 22 {
				t.Fatalf("retained wrong claim witness: %+v, %v", entry, err)
			}
		})
	}
}
