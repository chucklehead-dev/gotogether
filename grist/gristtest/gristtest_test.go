package gristtest_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// recTB records a helper's failures instead of failing the test: Fatal
// ends the helper's goroutine (run).
type recTB struct {
	testing.TB
	msgs []string
}

func (r *recTB) Helper() {}
func (r *recTB) Errorf(f string, a ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(f, a...))
}
func (r *recTB) Fatalf(f string, a ...any) { r.Errorf(f, a...); runtime.Goexit() }
func (r *recTB) Fatal(a ...any)            { r.msgs = append(r.msgs, fmt.Sprint(a...)); runtime.Goexit() }
func (r *recTB) FailNow()                  { runtime.Goexit() }

// run runs f with a recTB, returning its messages.
func run(t *testing.T, f func(tb testing.TB)) []string {
	r := &recTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(r)
	}()
	<-done
	return r.msgs
}

type noted struct {
	Item string `json:"item" grist:"tag"`
}

type note struct {
	Item string `json:"item"`
}

func app() *grist.App {
	a := grist.New("notes")
	ev := grist.NewEvent[noted](a, "noted")
	grist.NewCommand(a, "note", func(r *grist.Reads, c note) ([]grist.Event, error) {
		return []grist.Event{ev.Of(noted{Item: c.Item})}, nil
	}, grist.Authorize(grist.Authenticated))
	return a
}

// The witness passes on the files the catalog generates, and fails, naming
// the file and the fix, on one that differs or a hole the model lacks.
func TestGeneratedWitness(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	a := app()
	files, err := grist.Starter(a)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll("model", 0o755)
	for p, b := range files {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b := files["model/notes_rules.qnt"]
	g, err := grist.Generate(a, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Write("."); err != nil {
		t.Fatal(err)
	}
	gristtest.Generated(t, a) // passes (or skips without quint, after the diff)
	os.WriteFile("notes.binding.yaml", []byte("# by hand\n"), 0o644)
	if msgs := run(t, func(tb testing.TB) { gristtest.Generated(tb, a) }); len(msgs) != 1 || !strings.Contains(msgs[0], "notes.binding.yaml") || !strings.Contains(msgs[0], "regenerate") {
		t.Errorf("an edited binding: %v, want one failure naming it and the fix", msgs)
	}
	g.Write(".")
	os.WriteFile(filepath.Join("model", "notes_rules.qnt"), []byte(strings.Replace(string(b), "pure def noteNeeds", "pure def noteNeedz", 1)), 0o644)
	if msgs := run(t, func(tb testing.TB) { gristtest.Generated(tb, a) }); len(msgs) != 1 || !strings.Contains(msgs[0], "lacks noteNeeds") {
		t.Errorf("a missing hole: %v, want one failure naming it", msgs)
	}
}

// Overlay maps each of a mutant's files onto the package's file of its
// name, and refuses a mutant file that replaces none.
func TestOverlay(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("app.go", []byte("package app\n"), 0o644)
	os.MkdirAll(filepath.Join("testdata", "mutants", "m"), 0o755)
	os.WriteFile(filepath.Join("testdata", "mutants", "m", "app.go"), []byte("package app // mutant\n"), 0o644)
	path := gristtest.Overlay(t, filepath.Join("testdata", "mutants"), "m")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var o struct{ Replace map[string]string }
	if err := json.Unmarshal(b, &o); err != nil {
		t.Fatal(err)
	}
	here, _ := os.Getwd()
	if len(o.Replace) != 1 || o.Replace[filepath.Join(here, "app.go")] != filepath.Join(here, "testdata", "mutants", "m", "app.go") {
		t.Errorf("overlay %v", o.Replace)
	}
	os.WriteFile(filepath.Join("testdata", "mutants", "m", "other.go"), []byte("package app\n"), 0o644)
	if msgs := run(t, func(tb testing.TB) { gristtest.Overlay(tb, filepath.Join("testdata", "mutants"), "m") }); len(msgs) != 1 || !strings.Contains(msgs[0], "replaces no file") {
		t.Errorf("a mutant file replacing none: %v", msgs)
	}
}

// Answered fails a command a client was told about whose store-side step
// is missing or says otherwise, and passes when every answer has its step.
func TestAnswered(t *testing.T) {
	a := app()
	step := func(actor, id, outcome string) qtrace.Step {
		return qtrace.Step{Action: "note", Actor: actor, Outcome: outcome, Args: map[string]any{"id": id}}
	}
	traces := func(steps ...qtrace.Step) []*qtrace.Trace { return []*qtrace.Trace{{Steps: steps}} }
	if msgs := run(t, func(tb testing.TB) {
		gristtest.Answered(tb, a, traces(step("t1/clients", "c1", "ok"), step("t1", "c1", "ok")))
	}); len(msgs) > 0 {
		t.Errorf("every answer has its step: %v", msgs)
	}
	// a call the API denied ran nothing at the store; two tenants' clients
	// may use one id, each answered by its own tenant's step
	if msgs := run(t, func(tb testing.TB) {
		gristtest.Answered(tb, a, traces(step("t1/clients", "c1", "ok"), step("t1", "c1", "ok"), step("t1/clients", "c2", "denied"),
			step("t2/clients", "c1", "ok"), step("t2", "c1", "ok")))
	}); len(msgs) > 0 {
		t.Errorf("a denied call, an id in two tenants: %v", msgs)
	}
	// a key's calls answered alike (the second from its marker), and the
	// key reused with other arguments, refused at the store on each try
	if msgs := run(t, func(tb testing.TB) {
		gristtest.Answered(tb, a, traces(step("t1/clients", "c1", "ok"), step("t1", "c1", "ok"), step("t1/clients", "c1", "ok"),
			step("t1/clients", "c1", "idempotency-key-reused"), step("t1", "c1", "idempotency-key-reused"), step("t1", "c1", "idempotency-key-reused")))
	}); len(msgs) > 0 {
		t.Errorf("a key answered twice, then reused: %v", msgs)
	}
	for name, ts := range map[string][]*qtrace.Trace{
		"missing": traces(step("t1/clients", "c1", "ok")),
		// t1's step of the id does not answer t2's client
		"another tenant's": traces(step("t1/clients", "c1", "ok"), step("t1", "c1", "ok"), step("t2/clients", "c1", "ok")),
		"differs":          traces(step("t1/clients", "c1", "ok"), step("t1", "c1", "full")),
		"twice":            traces(step("t1/clients", "c1", "ok"), step("t1", "c1", "ok"), step("t1", "c1", "ok")),
		// a key's two outcomes: the client told the one the store lacks
		"two outcomes":      traces(step("t1/clients", "c1", "ok"), step("t1", "c1", "ok"), step("t1/clients", "c1", "conflict")),
		"reused unrecorded": traces(step("t1/clients", "c1", "ok"), step("t1", "c1", "ok"), step("t1/clients", "c1", "idempotency-key-reused")),
	} {
		if msgs := run(t, func(tb testing.TB) { gristtest.Answered(tb, a, ts) }); len(msgs) == 0 {
			t.Errorf("%s: Answered passed", name)
		}
	}
}
