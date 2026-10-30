package qtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/validate/navproto"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// navAt navigates the run spec names (GOTOGETHER_RUN) at node (and
// follow) through an exhaustive search of race, as `gotogether nav -run`
// does in a test binary, and returns the node it wrote.
func navAt(t *testing.T, spec, node, follow string) *navproto.Node {
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
	var n navproto.Node
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
		if n.Receipt.Status != "pass" || len(n.Steps) != 0 || len(n.Tasks) != 2 {
			t.Errorf("the nominal run from it: %+v", n)
		}
	})
	t.Run("branch", func(t *testing.T) {
		n := navAt(t, "TestNavRun/branch#1:schedule=", "start", "1")
		again := navAt(t, "TestNavRun/branch#2:schedule=", "0:1@1", "")
		if n.ID != "0:1@1" || n.Receipt.Reason != "rejects withdraw" {
			t.Fatalf("the withdrawer first: %+v", n)
		}
		for _, node := range []*navproto.Node{n, again} {
			if node.Profile == nil || node.Profile.Chooser != "navigation" || node.Profile.Rates != nil || node.Profile.Seeds != nil || node.Profile.Exhaust != nil || node.Profile.Parallel != 1 {
				t.Fatalf("node claims a sampling policy it does not execute: %+v", node.Profile)
			}
			if node.Execution == nil || node.Execution.Run != node.Run {
				t.Fatal("observed limits lost the run identity")
			}
			node.Profile.ID = ""
			node.Profile.Replay = strings.ReplaceAll(node.Profile.Replay, node.Run, "run")
			node.Execution.Run = ""
			node.Run = "" // exploration identities differ; the effective policies must agree
		}
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
		if c.Receipt.Status != "pass" || len(c.Model) != 1 || len(c.Model[0].State) == 0 {
			t.Errorf("its nominal branch: %+v", c)
		}
	})
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }

// depositors are four tasks depositing in turn: three scheduling
// choices (of four, three and two runnable tasks), every run conforming.
func depositors(c qenv.Chooser) error {
	s := qenv.New(c)
	defer s.Close()
	balance := 0
	for i := range 4 {
		s.Spawn(fmt.Sprintf("d%d", i), func(ctx context.Context) {
			balance += 10
			qobs.Record(ctx, "deposit", nil, "amount", 10, "obs.balance", balance)
		})
	}
	return s.Run(time.Second)
}

// serveAt runs a navigation session (GOTOGETHER_NAV=serve) of the run
// spec names in system run, its requests reqs, and returns its lines.
func serveAt(t testing.TB, spec string, run func(qenv.Chooser) error, reqs ...string) []navproto.Response {
	t.Helper()
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.jsonl"), filepath.Join(dir, "out.jsonl")
	if err := os.WriteFile(in, []byte(strings.Join(reqs, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOTOGETHER_RUN", spec)
	t.Setenv("GOTOGETHER_NAV", "serve")
	t.Setenv("GOTOGETHER_NAV_IN", in)
	t.Setenv("GOTOGETHER_NAV_OUT", out)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: run}
	sys.Exhaust(t, Bound{Delays: 1, Parallel: 1}).Expect(t, Conforms())
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var lines []navproto.Response
	for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var r navproto.Response
		if err := jsonUnmarshal([]byte(l), &r); err != nil {
			t.Fatalf("a session's line %q: %v", l, err)
		}
		lines = append(lines, r)
	}
	return lines
}

// A session (GOTOGETHER_NAV=serve) answers a request a line after a line
// naming the run: a node by its id and by following its branches are one
// node; a bad request is an error and the session goes on; a fold
// advances through the choices the base run made nominally to its next
// decision, or (off its path, where every later choice is nominal) to
// the run's end, saying how many it folded.
func TestNavServe(t *testing.T) {
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	RequireQuint(t)
	rs := serveAt(t, "TestNavServe#1:schedule=0:1,2:1", depositors,
		`{"node":"start"}`,
		`{"node":"start","follow":[1],"take":0}`,
		`{"node":"0:1@2"}`,
		`{"node":"@1","fold":true}`,
		`{"node":"nowhere"}`,
		`{"node":"0:2@1","fold":true}`,
		`{"node":"@0","fold":true}`,
	)
	if len(rs) != 8 || rs[0].Session == nil || rs[0].Session.Choices != 3 || !strings.HasSuffix(rs[0].Session.Run, ":schedule=0:1,2:1") {
		t.Fatalf("the session's lines: %+v", rs)
	}
	start := rs[1].Node
	if start == nil || start.ID != "@0" || start.Choice == nil || start.Choice.N != 4 || start.Choice.Taken != 1 {
		t.Fatalf("its start: %+v", rs[1])
	}
	a, _ := jsonMarshal(rs[2].Node)
	b, _ := jsonMarshal(rs[3].Node)
	if rs[2].Node == nil || rs[2].Node.ID != "0:1@2" || string(a) != string(b) {
		t.Errorf("a node by following and by its id differ:\n%s\n%s", a, b)
	}
	folded := rs[4].Node
	if folded == nil || folded.ID != "0:1@2" || folded.Folded != 1 || folded.Choice == nil || folded.Choice.Taken != 1 {
		t.Errorf("@1 folded to the base run's next decision, at 2: %+v", rs[4])
	}
	if rs[5].Error == "" || !strings.Contains(rs[5].Error, `node "nowhere"`) {
		t.Errorf("a bad request: %+v", rs[5])
	}
	end := rs[6].Node
	if end == nil || end.ID != "0:2@3" || end.Folded != 2 || end.Choice != nil || len(end.Steps) != 4 || end.Receipt.Status != "pass" {
		t.Errorf("off the base path a fold runs to the end: %+v", rs[6])
	}
	if n := rs[7].Node; n == nil || n.ID != "@0" || n.Folded != 0 {
		t.Errorf("a node at a decision folds nothing: %+v", rs[7])
	}
}

// A session's base run is made twice: a system whose runs differ fails
// the test and says so on the session's first line, since no node of it
// would replay.
func TestNavServeNondeterministic(t *testing.T) {
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	RequireQuint(t)
	f := &failTB{TB: t}
	calls := 0
	run := func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		calls++
		n := calls
		s.Spawn("client", func(ctx context.Context) {
			qobs.Record(ctx, "deposit", nil, "amount", n, "obs.balance", n)
		})
		return s.Run(time.Second)
	}
	var rs []navproto.Response
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		rs = serveAt(f, "TestNavServeNondeterministic#1:schedule=", run, `{"node":"start"}`)
	}()
	<-done
	if len(rs) == 0 || rs[0].Session == nil || len(rs[0].Session.Notes) == 0 || !strings.Contains(rs[0].Session.Notes[0], "not deterministic") {
		t.Errorf("the session's first line: %+v", rs)
	}
	if all := strings.Join(f.errs, "\n"); !strings.Contains(all, "ran twice made two runs") {
		t.Errorf("errors %q, want the difference named", f.errs)
	}
}
