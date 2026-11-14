package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A run that starts from a recorded snapshot never evaluates init, so a
// choice init makes is no choice of the run's: the run is judged as any
// other (the Go evaluator's pass, no pinned-choice search). golem's first
// use (#144) recorded a snapshot to avoid its init's leader choice and got
// no verdict: "finite pinned choices currently require ... without ...
// snapshot", because init's choice still sent the run to that search.
func TestSnapshotStartIgnoresInitsChoice(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	model := `module leader {
  const REPLICAS: Set[str]
  var leader: str
  var term: int
  action init = { nondet first = REPLICAS.oneOf() all { leader' = first, term' = 0 } }
  action restore(l: str, n: int): bool = all { leader' = l, term' = n }
  action elect(r: str): bool = all { REPLICAS.contains(r), leader' = r, term' = term + 1 }
  action step = { nondet r = REPLICAS.oneOf() elect(r) }
  val termNonNegative = term >= 0
}`
	if err := os.WriteFile(filepath.Join(dir, "leader.qnt"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := `spec: leader.qnt
module: leader
constants: { REPLICAS: 'Set("r1", "r2", "r3")' }
snapshot: restore
actions:
  restore: {}
  elect:
    expect: 'term == {{.obs.term}}'
invariants: [termNonNegative]
`
	bp := filepath.Join(dir, "leader.binding.yaml")
	if err := os.WriteFile(bp, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := NewChecker(t.Context(), bp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	steps := []qtrace.Step{
		{Action: "restore", Args: map[string]any{"l": "r2", "n": int64(4)}},
		{Action: "elect", Args: map[string]any{"r": "r3"}, Obs: map[string]any{"term": int64(5)}},
	}
	for i := range steps {
		steps[i].Actor, steps[i].Recorder, steps[i].Seq = "gw", "rec", uint64(i+1)
	}
	rep, err := c.Check(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	if note := c.Binding.InitChoiceNote(c.Model); note != "" {
		t.Errorf("a binding with a snapshot was warned of init's choice: %s", note)
	}
	if !rep.OK() || len(rep.Actors) != 1 || rep.Actors[0].Conforms != Pass {
		t.Fatalf("a run from a snapshot was not judged:\n%s", rep.Summary())
	}
	if rep.Actors[0].Choices != nil {
		t.Fatalf("a run from a snapshot went to the pinned-choice search for init's choice: %+v", rep.Actors[0].Choices)
	}
	// and the step is still checked: a wrong observation is rejected
	steps[1].Obs["term"] = int64(9)
	rep, err = c.Check(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || rep.Actors[0].Conforms != Fail {
		t.Fatalf("a wrong observation after a snapshot conformed:\n%s", rep.Summary())
	}
}

// Without a snapshot, the binding is told what init's choice costs and how
// to avoid it, naming the choice.
func TestInitChoiceNote(t *testing.T) {
	c := initialChoicesChecker(t, `{ nondet value = Set(-1, 1).oneOf(); all { phase' = 0, chosen' = value } }`, "safe", Options{})
	note := c.Binding.InitChoiceNote(c.Model)
	for _, want := range []string{"initialChoices's init makes a choice of its own (nondet value)", "a quint process", "snapshot:", "make init deterministic"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q: %s", want, note)
		}
	}
	c = initialChoicesChecker(t, `all { phase' = 0, chosen' = 1 }`, "safe", Options{})
	if note := c.Binding.InitChoiceNote(c.Model); note != "" {
		t.Errorf("a deterministic init was noted: %s", note)
	}
}
