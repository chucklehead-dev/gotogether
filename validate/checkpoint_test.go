package validate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A later snapshot observes the entire replayed state; it cannot reset that
// state or disappear. The wrong-ops case differs only in a variable the normal
// deposit observation does not expose. The reset case's next observation agrees
// with the wrong snapshot, so merely replaying restore would falsely pass it.
func TestSnapshotCheckpoints(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	deposit := func(seq uint64, amount, balance int64) qtrace.Step {
		at := time.Unix(0, int64(seq)*int64(10*time.Millisecond))
		return qtrace.Step{Action: "deposit", Recorder: "r", Thread: "client", Seq: seq, Time: at, Invoked: at.Add(-time.Millisecond), Source: fmt.Sprintf("checkpoints.jsonl:%d", seq), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
	}
	snapshot := func(seq uint64, balance, ops int64) qtrace.Step {
		at := time.Unix(0, int64(seq)*int64(10*time.Millisecond))
		return qtrace.Step{Action: "restore", Recorder: "r", Thread: "client", Seq: seq, Time: at, Invoked: at.Add(-time.Millisecond), Source: fmt.Sprintf("checkpoints.jsonl:%d", seq), Args: map[string]any{"b": balance, "n": ops}}
	}
	cases := []struct {
		name string
		run  []qtrace.Step
		bad  uint64 // source line of the disagreeing checkpoint, or zero
	}{
		{"matching", []qtrace.Step{deposit(1, 5, 5), snapshot(2, 5, 1), deposit(3, 2, 7)}, 0},
		{"wrong balance", []qtrace.Step{deposit(1, 5, 5), snapshot(2, 6, 1), deposit(3, 2, 7)}, 2},
		{"wrong unobserved variable", []qtrace.Step{deposit(1, 5, 5), snapshot(2, 5, 2), deposit(3, 2, 7)}, 2},
		{"must not reset", []qtrace.Step{deposit(1, 5, 5), snapshot(2, 50, 1), deposit(3, 2, 52)}, 2},
		{"lost head then matching", []qtrace.Step{snapshot(3, 50, 3), deposit(4, 5, 55), snapshot(5, 55, 4), deposit(6, 2, 57)}, 0},
		{"lost head then wrong checkpoint", []qtrace.Step{snapshot(3, 50, 3), deposit(4, 5, 55), snapshot(5, 55, 3), deposit(6, 2, 57)}, 5},
	}
	check := func(t *testing.T, report *Report, k int) {
		t.Helper()
		if len(report.Actors) != 1 {
			t.Fatalf("%s: actors %d, want one", cases[k].name, len(report.Actors))
		}
		a := report.Actors[0]
		if cases[k].bad == 0 {
			if !report.OK() || a.Conforms != Pass {
				t.Fatalf("%s: matching checkpoint did not conform:\n%s", cases[k].name, report.Summary())
			}
		} else if a.Conforms != Fail || a.Failure == nil || a.Failure.Step == nil || a.Failure.Step.Action != "restore" || a.Failure.Step.Source != fmt.Sprintf("checkpoints.jsonl:%d", cases[k].bad) {
			t.Fatalf("%s: want conformance failure at restore source line %d, got:\n%s", cases[k].name, cases[k].bad, report.Summary())
		}
		if strings.Contains(report.Summary(), "snapshot not first in its trace: skipped") {
			t.Fatalf("%s: checkpoint was skipped:\n%s", cases[k].name, report.Summary())
		}
		if k >= 4 && !strings.Contains(report.Summary(), "judged from the state recorded by restore at seq 3") {
			t.Fatalf("%s: missing lost-prefix starting-state explanation:\n%s", cases[k].name, report.Summary())
		}
	}
	for _, mode := range []string{"go", "quint", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			c, err := NewChecker("testdata/snapshot.yaml", Options{Dir: t.TempDir(), Eval: mode})
			if err != nil {
				t.Fatal(err)
			}
			var runs [][]qtrace.Step
			for k, cs := range cases {
				runs = append(runs, cs.run)
				t.Run(cs.name, func(t *testing.T) {
					r, err := c.Check(t.Context(), cs.run)
					if err != nil {
						t.Fatal(err)
					}
					check(t, r, k)
					j, err := c.Judge(t.Context(), cs.run, SearchOptions{})
					if err != nil {
						t.Fatal(err)
					}
					check(t, j.Report, k)
					// Reordering checkpoints needs their original role retained
					// through the final replay; this bounded slice refuses it.
					for _, search := range []SearchOptions{{Linearize: true}, {ResolvePending: true}} {
						_, err := c.Judge(t.Context(), cs.run, search)
						checkpointSearchRefused(t, err)
					}
					if mode == "go" {
						n, err := c.Nav(NavStart{Steps: cs.run, At: -1})
						if cs.bad != 0 {
							if err == nil || !strings.Contains(err.Error(), "restore") {
								t.Fatalf("navigation accepted disagreeing checkpoint or lost its identity: %v", err)
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						node, err := n.Node()
						if err != nil {
							t.Fatal(err)
						}
						want := map[string]string{"balance": "7", "ops": "2"}
						if k >= 4 {
							want = map[string]string{"balance": "57", "ops": "5"}
						}
						for _, value := range node.State {
							if v, ok := want[value.Name]; ok {
								if value.Value != v {
									t.Errorf("navigation %s=%s, want %s", value.Name, value.Value, v)
								}
								delete(want, value.Name)
							}
						}
						if len(want) != 0 {
							t.Errorf("navigation omitted state variables: %v", want)
						}
					}
				})
			}
			reports, errs := c.CheckBatch(t.Context(), runs, len(runs))
			for k := range runs {
				if errs[k] != nil {
					t.Fatalf("CheckBatch %s: %v", cases[k].name, errs[k])
				}
				check(t, reports[k], k)
			}
		})
	}
}

// A checkpoint after another recorder lost its tail may reflect a missing
// action. A disagreement there remains incomplete evidence, not a defect proof.
func TestSnapshotCheckpointAfterLostTail(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	at := func(ms int) time.Time { return time.Unix(0, int64(ms)*int64(time.Millisecond)) }
	run := qtrace.MarkLost([]qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Time: at(10), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}},
		{Action: "restore", Recorder: "b", Seq: 1, Time: at(30), Args: map[string]any{"b": int64(10), "n": int64(2)}},
	}, qtrace.Lost{Recorder: "a", After: 1})
	for _, mode := range []string{"go", "quint", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			c, err := NewChecker("testdata/snapshot.yaml", Options{Dir: t.TempDir(), Eval: mode})
			if err != nil {
				t.Fatal(err)
			}
			r, err := c.Check(t.Context(), run)
			if err != nil {
				t.Fatal(err)
			}
			if a := r.Actors[0]; a.Conforms != NotEvaluated || a.Reason() != "no verdict (lost steps)" || r.OK() {
				t.Fatalf("checkpoint after lost steps should be unjudged:\n%s", r.Summary())
			}
		})
	}
}

// The history starts with an observed deposit, so init applies even when an
// ordering search tries the later checkpoint first. Treating that checkpoint as
// a free starting restore would explain the deposit's impossible balance of 55.
func TestSnapshotCheckpointCannotBecomeStart(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	run := []qtrace.Step{
		{Action: "deposit", Recorder: "r", Seq: 1, Time: time.Unix(0, int64(10*time.Millisecond)), Source: "checkpoints.jsonl:1", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(55)}},
		{Action: "restore", Recorder: "r", Seq: 2, Time: time.Unix(0, int64(20*time.Millisecond)), Source: "checkpoints.jsonl:2", Args: map[string]any{"b": int64(50), "n": int64(3)}},
	}
	for _, mode := range []string{"go", "quint", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			c, err := NewChecker("testdata/snapshot.yaml", Options{Dir: t.TempDir(), Eval: mode})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Judge(t.Context(), run, SearchOptions{Linearize: true})
			checkpointSearchRefused(t, err)
		})
	}
}

func checkpointSearchRefused(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "snapshot checkpoint") || !strings.Contains(err.Error(), "without Linearize or ResolvePending") {
		t.Fatalf("want explicit checkpoint history-search refusal and recorded-order fix, got %v", err)
	}
}
