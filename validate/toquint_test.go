package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A run whose steps carry records, sums, sets of tuples and maps of
// lists, checked by a binding that gives no templates for them (toQuint
// by the model's types) and compares the model's log and read set with
// the recorded ones: the faithful run conforms, and one whose recorded
// log differs from what its batches appended is a mismatch.
func TestTypedFieldsChecked(t *testing.T) {
	t.Parallel()
	needQuint(t)
	c, err := NewChecker("testdata/typed.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	type read struct {
		Typ  string      `json:"typ"`
		Tags [][2]string `json:"tags"`
	}
	run := func(lastLog string) []qtrace.Step {
		sink := &qobs.MemorySink{}
		r := qobs.NewRecorder(sink, qobs.WithActor("t1"))
		batch := func(events ...any) string {
			b, _ := json.Marshal(map[string]any{"events": events, "at": 5, "cause": nil})
			return string(b)
		}
		rs := []read{{"counter-created", [][2]string{{"name", "x"}}}}
		r.Record(context.Background(), "commit", nil,
			"batch", batch(map[string]any{"tag": "Created", "counter": "c1", "name": "x"}),
			"rs", rs, "tally", map[string][]int{"c1": {1}}, "pair", []any{1, true},
			"nested", `{"a": {"b": []}}`, "n", 1,
			"obs.log", `[{"Created": {"counter": "c1", "name": "x"}}]`, "obs.reads", rs)
		r.Record(context.Background(), "commit", nil,
			"batch", batch(map[string]any{"tag": "Renamed", "value": map[string]any{"counter": "c1", "name": "y"}}),
			"rs", []read{}, "tally", map[string][]int{}, "pair", []any{2, false},
			"nested", `{"a": {"b": [[3, "z"]]}}`, "n", 2,
			"obs.log", lastLog, "obs.reads", rs)
		return sink.Steps()
	}
	rep, err := c.Check(context.Background(), run(`[{"Created": {"counter": "c1", "name": "x"}}, {"Renamed": {"counter": "c1", "name": "y"}}]`))
	if err != nil || !rep.OK() {
		t.Fatalf("the faithful run: %v\n%s", err, rep.Summary())
	}
	rep, err = c.Check(context.Background(), run(`[{"Created": {"counter": "c1", "name": "x"}}, {"Renamed": {"counter": "c1", "name": "x"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if f := rep.Actors[0].Failure; rep.OK() || f == nil || f.Kind != "state" || f.Index != 1 {
		t.Fatalf("a wrong recorded log: want a mismatch at step 1, got\n%s", rep.Summary())
	}
}

// A binding whose actors match none of a run's has no verdict, and says
// which actors the run had.
func TestActorsMatchNone(t *testing.T) {
	t.Parallel()
	needQuint(t)
	dir := t.TempDir()
	spec, _ := filepath.Abs("testdata/account.qnt")
	path := filepath.Join(dir, "b.yaml")
	if err := os.WriteFile(path, []byte("spec: "+spec+"\nmodule: account\nconstants: { LIMIT: '100' }\nactors: ['*.clients']\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := NewChecker(path, Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	sink := &qobs.MemorySink{}
	qobs.NewRecorder(sink, qobs.WithActor("acct")).Record(context.Background(), "deposit", nil, "amount", 1)
	rep, err := c.Check(context.Background(), sink.Steps())
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || len(rep.Actors) != 0 || !strings.Contains(fmt.Sprint(rep.Issues), "the binding checks the actors *.clients; the run's are all other bindings' (acct)") {
		t.Fatalf("got %s %v", rep.Summary(), rep.Issues)
	}
}
