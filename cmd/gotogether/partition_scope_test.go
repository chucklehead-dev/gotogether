package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestValidatePartitionScopeCounts(t *testing.T) {
	skip.Quint(t)
	for _, fail := range []bool{false, true} {
		name := "pass"
		if fail {
			name = "fail"
		}
		t.Run(name, func(t *testing.T) {
			steps := []qtrace.Step{{Actor: "ledger", Recorder: "r", Seq: 1, Action: "open", Args: map[string]any{"acct": "a"}}, {Actor: "ledger", Recorder: "r", Seq: 2, Action: "open", Args: map[string]any{"acct": "b"}}}
			if fail {
				steps = append(steps, qtrace.Step{Actor: "ledger", Recorder: "r", Seq: 3, Action: "open", Args: map[string]any{"acct": "b"}})
			}
			path := filepath.Join(t.TempDir(), "ledger.jsonl")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			err = qtrace.WriteJSONL(f, steps)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"-binding", "../../validate/testdata/ledger.yaml", "-dir", t.TempDir(), path}
			wantCode := 0
			if fail {
				wantCode = 1
			}
			for _, asJSON := range []bool{false, true} {
				argv := append([]string(nil), args...)
				if asJSON {
					argv = append([]string{"-json"}, argv...)
				}
				code, out, err := validated(argv)
				if code != wantCode || err != nil {
					t.Fatalf("exit %d %v: %s", code, err, out)
				}
				if !asJSON {
					if !strings.Contains(string(out), "2 partitions of 1 actor") || strings.Contains(string(out), "2 actors") {
						t.Fatalf("misleading judgment counts: %s", out)
					}
					continue
				}
				var doc validate.Document
				if err := json.Unmarshal(out, &doc); err != nil {
					t.Fatal(err)
				}
				counts := doc.Receipt.Counts()
				if counts.Actors != 1 || counts.Judgments != 2 || counts.Partitions != 2 || len(doc.Receipt.PartitionInstances) != 1 {
					t.Fatalf("incorrect JSON scope: %s", out)
				}
				for _, actor := range doc.Receipt.Actors {
					if actor.SourceActor != "ledger" || actor.PartitionInfo == nil || actor.PartitionInfo.Instance != 1 {
						t.Fatalf("partition missing source scope: %+v", actor)
					}
				}
			}
		})
	}
}

func TestPartitionScopeMixedSummary(t *testing.T) {
	skip.Quint(t)
	checker, err := validate.NewChecker("../../validate/testdata/ledger.yaml", validate.Options{Dir: t.TempDir(), NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	report, err := checker.Check(context.Background(), []qtrace.Step{
		{Actor: "ledger", Recorder: "r", Seq: 1, Action: "open", Args: map[string]any{"acct": "a"}},
		{Actor: "ledger", Recorder: "r", Seq: 2, Action: "open", Args: map[string]any{"acct": "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	results := append(report.Actors, &validate.Result{Actor: "service", SourceActor: "service", Conforms: validate.Pass})
	want := "3 judgments (1 actor trace, 2 partitions; 2 source actors)"
	if got := passSummary(results); !strings.Contains(got, want) {
		t.Fatalf("mixed judgments mislabeled: %s", got)
	}
	if got := failSummary(results); !strings.Contains(got, "0 of "+want) {
		t.Fatalf("mixed denominator mislabeled: %s", got)
	}
	if got := passSummary(results[2:]); !strings.HasPrefix(got, "1 actor conforms") {
		t.Fatalf("unpartitioned wording changed: %s", got)
	}
}

func TestRepeatedSourceActorJudgmentsAreNotNewActors(t *testing.T) {
	results := []*validate.Result{{Actor: "ledger", SourceActor: "ledger", Conforms: validate.Pass}, {Actor: "ledger", SourceActor: "ledger", Conforms: validate.Pass}}
	if got := passSummary(results); !strings.Contains(got, "2 actor judgments for 1 source actor conform") {
		t.Fatalf("duplicate identities: %s", got)
	}
}
