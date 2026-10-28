package qtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
)

// navAt navigates the run spec names (GOTOGETHER_RUN) at node (and
// follow) through an exhaustive search of race, as `gotogether nav -run`
// does in a test binary, and returns the node it wrote.
func navAt(t *testing.T, spec, node, follow string) *validate.RunNode {
	t.Helper()
	out := filepath.Join(t.TempDir(), "node.json")
	t.Setenv("GOTOGETHER_RUN", spec)
	t.Setenv("GOTOGETHER_NAV", node)
	t.Setenv("GOTOGETHER_NAV_FOLLOW", follow)
	t.Setenv("GOTOGETHER_NAV_OUT", out)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	sys.Exhaust(t, Bound{Delays: 1}).Expect(t, Conforms())
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var n validate.RunNode
	if err := jsonUnmarshal(b, &n); err != nil {
		t.Fatal(err)
	}
	return &n
}

// The run navigator: a node is a prefix of the run's choices, its
// branches the next choice's options named (the runnable tasks), a
// child the replay with that option forced and nominal after; the node
// shows the steps so far, the tasks, the model's state and the verdict
// of the run it makes. A node replays deterministically, and "fail"
// opens the last choice before the failing step.
func TestNavRun(t *testing.T) {
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	RequireQuint(t)
	t.Run("start", func(t *testing.T) {
		n := navAt(t, "TestNavRun/start#1:schedule=", "start", "")
		if n.ID != "@0" || n.Choice == nil || n.Choice.Kind != "sched.task" || !slices.Equal(n.Choice.Options, []string{"depositor", "withdrawer"}) {
			t.Fatalf("the start node: %+v", n)
		}
		if len(n.Branches) != 2 || n.Branches[0].Child != "@1" || n.Branches[1].Child != "0:1@1" || n.Branches[1].Name != "withdrawer" {
			t.Errorf("its branches: %+v", n.Branches)
		}
		if n.Verdict != "pass" || len(n.Steps) != 0 || len(n.Tasks) != 2 {
			t.Errorf("the nominal run from it: %+v", n)
		}
	})
	t.Run("branch", func(t *testing.T) {
		n := navAt(t, "TestNavRun/branch#1:schedule=", "start", "1")
		again := navAt(t, "TestNavRun/branch#2:schedule=", "0:1@1", "")
		if n.ID != "0:1@1" || n.Verdict != "rejects withdraw" {
			t.Fatalf("the withdrawer first: %+v", n)
		}
		n.Run, again.Run = "", "" // the explorations' refs differ
		a, _ := jsonMarshal(n)
		b, _ := jsonMarshal(again)
		if string(a) != string(b) {
			t.Errorf("a node by its id and by following its branch differ:\n%s\n%s", a, b)
		}
	})
	t.Run("fail", func(t *testing.T) {
		n := navAt(t, "TestNavRun/fail#1:schedule=0:1", "fail", "")
		if n.ID != "@0" || n.Choice == nil || n.Choice.Taken != 1 {
			t.Fatalf("the failing run's node: %+v", n)
		}
		if len(n.Model) != 0 {
			t.Errorf("before any step, no actor: %+v", n.Model)
		}
		// its nominal child passes: the delay is the failure
		c := navAt(t, "TestNavRun/fail#2:schedule=0:1", "fail", "0")
		if c.Verdict != "pass" || len(c.Model) != 1 || len(c.Model[0].State) == 0 {
			t.Errorf("its nominal branch: %+v", c)
		}
	})
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
