package gristtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/internal/gen"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// recTB records a helper's failures instead of failing the test: Fatal
// ends the helper's goroutine (run).
type recTB struct {
	testing.TB
	msgs []string
	logs []string
}

func (r *recTB) Helper() {}
func (r *recTB) Errorf(f string, a ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(f, a...))
}
func (r *recTB) Fatalf(f string, a ...any) { r.Errorf(f, a...); runtime.Goexit() }
func (r *recTB) Fatal(a ...any)            { r.msgs = append(r.msgs, fmt.Sprint(a...)); runtime.Goexit() }
func (r *recTB) FailNow()                  { runtime.Goexit() }
func (r *recTB) Log(a ...any)              { r.logs = append(r.logs, fmt.Sprint(a...)) }
func (r *recTB) Logf(f string, a ...any)   { r.logs = append(r.logs, fmt.Sprintf(f, a...)) }

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
	files, err := gen.Starter(a)
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
	g, err := gen.Generate(a, ".")
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
	if msgs := run(t, func(tb testing.TB) { gristtest.Generated(tb, a) }); len(msgs) != 1 || !strings.Contains(msgs[0], "model.definition_missing") || !strings.Contains(msgs[0], "noteNeeds") {
		t.Errorf("a missing hole: %v, want one failure naming it", msgs)
	}
}

// modelFixture installs the same handwritten starter and generated artifacts
// that a new app's model command produces, without executing Quint.
func modelFixture(t *testing.T, a *grist.App) map[string][]byte {
	t.Helper()
	files, err := gen.Starter(a)
	if err != nil {
		t.Fatal(err)
	}
	for path, contents := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := gen.Generate(a, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Write("."); err != nil {
		t.Fatal(err)
	}
	for path, contents := range g.Files {
		files[path] = contents
	}
	return files
}

func breakModelFixture(t *testing.T, files map[string][]byte) map[string][]byte {
	t.Helper()
	broken := map[string][]byte{
		"notes.binding.yaml":    []byte("# deliberately stale generated binding\n"),
		"model/notes_rules.qnt": []byte(strings.Replace(string(files["model/notes_rules.qnt"]), "pure def noteNeeds", "pure def noteNeedz", 1)),
		"model/notes.qnt":       []byte(strings.ReplaceAll(string(files["model/notes.qnt"]), "import notesRules.*", "// missing direct import notesRules.*")),
	}
	for path, contents := range broken {
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return broken
}

func TestGeneratedReportsAllFindingsWithoutWriting(t *testing.T) {
	t.Chdir(t.TempDir())
	a := app()
	files := modelFixture(t, a)
	broken := breakModelFixture(t, files)
	msgs := run(t, func(tb testing.TB) { gristtest.Generated(tb, a) })
	if len(msgs) != 1 {
		t.Fatalf("want one aggregate failure, got %v", msgs)
	}
	for _, want := range []string{"model.generated_stale", "notes.binding.yaml", "model.definition_missing", "noteNeeds", "model.import_missing", "notesRules"} {
		if !strings.Contains(msgs[0], want) {
			t.Fatalf("finding %q hidden by another failure:\n%s", want, msgs[0])
		}
	}
	for path, before := range broken {
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Fatalf("ordinary preflight changed %s: %v", path, err)
		}
	}
}

func TestGeneratedUpdateAppliesOnlyMechanicalChanges(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("GRIST_UPDATE", "1")
	a := app()
	files := modelFixture(t, a)
	broken := breakModelFixture(t, files)
	msgs := run(t, func(tb testing.TB) { gristtest.Generated(tb, a) })
	if len(msgs) != 1 || !strings.Contains(msgs[0], "model.definition_missing") || !strings.Contains(msgs[0], "model.import_missing") {
		t.Fatalf("generation hid unresolved handwritten work: %v", msgs)
	}
	if strings.Contains(msgs[0], "model.generated_stale") {
		t.Fatalf("applied mechanical change still reported stale:\n%s", msgs[0])
	}
	binding, err := os.ReadFile("notes.binding.yaml")
	if err != nil || string(binding) != string(files["notes.binding.yaml"]) {
		t.Fatalf("update did not repair generated binding: %v", err)
	}
	for _, path := range []string{"model/notes.qnt", "model/notes_rules.qnt"} {
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(broken[path]) {
			t.Fatalf("update overwrote handwritten %s: %v", path, err)
		}
	}
}

func TestGeneratedUpdateRemovesObsoleteGeneratedVariant(t *testing.T) {
	t.Chdir(t.TempDir())
	a := app()
	files := modelFixture(t, a)
	obsolete := "notes.retired.binding.yaml"
	if err := os.WriteFile(obsolete, files["notes.calls.binding.yaml"], 0o644); err != nil {
		t.Fatal(err)
	}
	if msgs := run(t, func(tb testing.TB) { gristtest.Generated(tb, a) }); len(msgs) != 1 || !strings.Contains(msgs[0], "model.generated_obsolete") {
		t.Fatalf("obsolete generated variant was ignored: %v", msgs)
	}
	t.Setenv("GRIST_UPDATE", "1")
	gristtest.Generated(t, a)
	if _, err := os.Stat(obsolete); !os.IsNotExist(err) {
		t.Fatalf("obsolete generated variant remains after update: %v", err)
	}
}

func TestGeneratedUpdateRetainsModelInputReviewWarning(t *testing.T) {
	t.Chdir(t.TempDir())
	files := modelFixture(t, app())
	// This fixture represents a never-deployed catalog. Discard its lock to
	// permit the event's shape change without inventing an upcaster decision.
	if err := os.Remove(gen.LockFile); err != nil {
		t.Fatal(err)
	}
	a := grist.New("notes")
	type expanded struct {
		Item string `json:"item" grist:"tag"`
		Note string `json:"note"`
	}
	ev := grist.NewEvent[expanded](a, "noted")
	grist.NewCommand(a, "note", func(_ *grist.Reads, c note) ([]grist.Event, error) {
		return []grist.Event{ev.Of(expanded{Item: c.Item})}, nil
	}, grist.Authorize(grist.Authenticated))
	t.Setenv("GRIST_UPDATE", "1")
	rec := &recTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		gristtest.Generated(rec, a)
	}()
	<-done
	if len(rec.msgs) > 0 {
		t.Fatalf("mechanical update failed: %v", rec.msgs)
	}
	if !strings.Contains(strings.Join(rec.logs, "\n"), "event noted has fields") {
		t.Fatalf("regeneration hid unresolved model-input review: %v", rec.logs)
	}
	rules, err := os.ReadFile("model/notes_rules.qnt")
	if err != nil || string(rules) != string(files["model/notes_rules.qnt"]) {
		t.Fatalf("generation changed handwritten rules instead of preserving review warning: %v", err)
	}
}

// Answered fails a command a client was told about whose store-side step
// is missing or says otherwise, and passes when every answer has its step.
func TestAnswered(t *testing.T) {
	a := app()
	step := func(actor, id, outcome string) qtrace.Step {
		return qtrace.Step{Action: "note", Actor: a.Name + "/" + actor, Outcome: outcome, Args: map[string]any{"id": id}}
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
