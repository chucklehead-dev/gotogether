package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"

	tea "charm.land/bubbletea/v2"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/qnav/internal/session"
	"github.com/chucklehead-dev/gotogether/validate"
)

// requireQuint skips a test that needs quint when it is not on PATH.
func requireQuint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
}

// root is the repository's root, from this package's directory.
const root = "../../.."

// navOf is a model navigator of binding from init.
func navOf(t *testing.T, binding string) *validate.Navigator {
	t.Helper()
	c, err := validate.NewChecker(filepath.Join(root, binding), validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	n, err := c.Nav(validate.NavStart{})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A driver feeds a navigator messages as a program would, running the
// commands that fetch nodes (and not the filter's cursor blink beyond
// its first tick), and keeps its screens.
type driver struct {
	t *testing.T
	m *Model
}

func drive(t *testing.T, c Config) *driver {
	t.Helper()
	c.NoColor = true
	d := &driver{t, New(c)}
	d.send(tea.WindowSizeMsg{Width: 100, Height: 50})
	d.run(d.m.Init())
	return d
}

// run runs cmd, feeding its message back while it fetches nodes.
func (d *driver) run(cmd tea.Cmd) {
	d.t.Helper()
	for cmd != nil {
		ch := make(chan tea.Msg, 1)
		go func() { ch <- cmd() }() //qenv:ok a command is the program's goroutine
		select {
		case msg := <-ch:
			if _, ok := msg.(loaded); !ok {
				return // a blink: the screen does not wait on it
			}
			_, cmd = d.m.Update(msg)
		case <-time.After(2 * time.Minute):
			d.t.Fatal("a command did not return")
		}
	}
}

func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	_, cmd := d.m.Update(msg)
	d.run(cmd)
}

// keys presses each key: a name (enter, down, backspace, esc) or a
// character.
func (d *driver) keys(ks ...string) *driver {
	d.t.Helper()
	named := map[string]rune{"enter": tea.KeyEnter, "down": tea.KeyDown, "up": tea.KeyUp, "backspace": tea.KeyBackspace, "left": tea.KeyLeft, "esc": tea.KeyEscape}
	for _, k := range ks {
		if code, ok := named[k]; ok {
			d.send(tea.KeyPressMsg{Code: code})
			continue
		}
		for _, r := range k {
			d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	return d
}

func (d *driver) screen() string { return d.m.Render() }

// golden compares screens with testdata/<name>.golden
// (GOTOGETHER_UPDATE_GOLDEN=1 rewrites it).
func golden(t *testing.T, name string, screens ...string) {
	t.Helper()
	got := strings.Join(screens, "\n\n========\n\n") + "\n"
	p := filepath.Join("testdata", name+".golden")
	if os.Getenv("GOTOGETHER_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := os.ReadFile(p)
	if got != string(want) {
		t.Errorf("the screens changed (GOTOGETHER_UPDATE_GOLDEN=1 rewrites %s, if they should):\n%s", p, got)
	}
}

// The limiter, from init: two admits (the state's change marked), admit
// disabled with why at the limit, back to the node before (its cursor
// where it was, on the branch taken).
func TestLimiter(t *testing.T) {
	t.Parallel()
	requireQuint(t)
	d := drive(t, Config{Title: "model limiter", Model: navOf(t, "validate/testdata/limiter.yaml")})
	init := d.screen()
	afterA := d.keys("enter").screen()
	moved := d.keys("down").screen()
	afterB := d.keys("enter").screen()
	back := d.keys("backspace").screen()
	golden(t, "limiter", init, afterA, afterB, back)
	if back != moved {
		t.Errorf("back is not the node before:\n%s", back)
	}
	if !strings.Contains(afterB, `* admitted = Set("a", "b")`) || strings.Contains(init, "* admitted") {
		t.Errorf("the changed variable is not marked, or marked at init:\n%s", afterB)
	}
	if !strings.Contains(afterB, "admitted.size() < LIMIT is false") {
		t.Errorf("no why for the disabled admit:\n%s", afterB)
	}
}

// The filter keeps the branches its text matches, and enter takes the
// one under the cursor among them.
func TestFilter(t *testing.T) {
	t.Parallel()
	requireQuint(t)
	d := drive(t, Config{Title: "model limiter", Model: navOf(t, "validate/testdata/limiter.yaml")})
	s := d.keys("/", `"c"`).screen()
	if !strings.Contains(s, `› admit("c")`) || strings.Contains(s, `admit("a")`) {
		t.Errorf("filtered:\n%s", s)
	}
	s = d.keys("enter", "enter").screen()
	if !strings.Contains(s, `* admitted = Set("c")`) {
		t.Errorf("the filtered branch taken:\n%s", s)
	}
}

// f folds the ticks model's five single ticks into one move, to where
// fire is enabled too; at a node of two branches it folds nothing and
// says so; back returns to init.
func TestFold(t *testing.T) {
	t.Parallel()
	requireQuint(t)
	d := drive(t, Config{Title: "model ticks", Model: navOf(t, "validate/testdata/ticks.yaml")})
	init := d.screen()
	folded := d.keys("f").screen()
	again := d.keys("f").screen()
	golden(t, "ticks_fold", init, folded, again)
	if !strings.Contains(folded, "* c = 5") || !strings.Contains(folded, "folded 5 transitions") {
		t.Errorf("not folded to c = 5:\n%s", folded)
	}
	if !strings.Contains(again, "nothing to fold") {
		t.Errorf("a decision folded:\n%s", again)
	}
	if back := d.keys("backspace").screen(); back != init {
		t.Errorf("back from the fold is not init:\n%s", back)
	}
}

// Colors are the terminal's own sixteen, and none without them
// (NO_COLOR): the markers alone say what the colors do.
func TestNoColor(t *testing.T) {
	t.Parallel()
	requireQuint(t)
	nav := navOf(t, "validate/testdata/limiter.yaml")
	colored := New(Config{Title: "model limiter", Model: nav})
	plain := New(Config{Title: "model limiter", Model: nav, NoColor: true})
	for _, m := range []*Model{colored, plain} {
		msg := m.Init()()
		m.Update(msg)
	}
	if !strings.Contains(colored.Render(), "\x1b[") {
		t.Errorf("no styles with colors")
	}
	if strings.Contains(plain.Render(), "\x1b[") {
		t.Errorf("styles under NO_COLOR:\n%q", plain.Render())
	}
}

// hello's noDedupe mutant's run (its TestMutant exploration's seed 1
// fails): opened at fail, a session of hello's test binary built with the
// mutant's overlay (GOTOGETHER_MUTANT, as the run's navigate line sets it). The failing node's screen
// (the cursor on the run's option), the run's option taken (the
// duplicate's second apply, which the model rejects), back, another
// option, and a save without a corpus says it is not saved.
func TestHelloRun(t *testing.T) {
	requireQuint(t)
	if testing.Short() {
		t.Skip("compiles hello's test binary")
	}
	pkg := filepath.Join(root, "examples", "hello")
	if _, err := os.Stat(pkg); err != nil {
		t.Skip("no examples/hello beside the module")
	}
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	t.Setenv("GOTOGETHER_MUTANT", "noDedupe")
	s, err := session.Start(pkg, "TestMutant#1:seed=1")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := drive(t, Config{Title: s.Info.Run, Run: s, Node: "fail"})
	fail := d.screen()
	took := d.keys("enter").screen()
	back := d.keys("backspace").screen()
	other := d.keys("up", "enter").screen()
	saved := d.keys("s").screen()
	golden(t, "hello_run", fail, took, back, other, saved)
	if back != fail {
		t.Errorf("back is not the failing node:\n%s", back)
	}
	if !strings.Contains(fail, "rejects apply") || !strings.Contains(fail, "› 2 counter.handle") {
		t.Errorf("the failing node, its cursor on the run's option:\n%s", fail)
	}
	if !strings.Contains(took, "steps so far (2)") || !strings.Contains(took, "rejects") {
		t.Errorf("the run's option: the duplicate applied, rejected:\n%s", took)
	}
	if !strings.Contains(saved, "not saved") {
		t.Errorf("a save without a corpus:\n%s", saved)
	}
}

// fakeRun answers nodes from a table, by id.
type fakeRun map[string]*navproto.Node

func (f fakeRun) Node(req navproto.Request) (*navproto.Node, error) {
	id := req.Node
	if id == "" {
		id = "@0"
	}
	return f[id], nil
}

// A run node marks the model's variables changed since the node before
// it, and none at the first.
func TestRunDiff(t *testing.T) {
	t.Parallel()
	state := func(count string) []navproto.ActorState {
		return []navproto.ActorState{{Actor: "default", State: []navproto.NamedValue{{Name: "applied", Value: "Set(1)"}, {Name: "count", Value: count}}}}
	}
	run := fakeRun{
		"@0": {Run: "TestX#1:seed=1", ID: "@0", Receipt: receipt.Summary{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked}, Model: state("1"), Choice: &navproto.Choice{Kind: "sched.task", N: 2, Options: []string{"a", "b"}},
			Branches: []navproto.Branch{{Option: 0, Name: "a", Child: "@1"}, {Option: 1, Name: "b", Child: "0:1@1"}}},
		"@1": {Run: "TestX#1:seed=1", ID: "@1", Receipt: receipt.Summary{Status: receipt.Pass, Scope: "collected_history", OtherExecutions: receipt.NotChecked}, Model: state("2")},
	}
	d := drive(t, Config{Title: "TestX#1:seed=1", Run: run})
	first := d.screen()
	next := d.keys("enter").screen()
	if strings.Contains(first, "* ") {
		t.Errorf("a change marked at the first node:\n%s", first)
	}
	if !strings.Contains(next, "* count = 2") || strings.Contains(next, "* applied") {
		t.Errorf("count changed, applied not:\n%s", next)
	}
}

// counters' blind mutant's run (tapes/testdata/blind.jsonl, recorded
// from its exploration's seed 1): the model rejects its fifth call, a
// create that committed blind. Opened at the failing step, the screen
// says why in grist's words (a concurrent append), and the filter finds
// what the model allows there instead (showCounter).
func TestGristRejection(t *testing.T) {
	t.Parallel()
	requireQuint(t)
	bind := filepath.Join(root, "examples", "counters", "counters.binding.yaml")
	if _, err := os.Stat(bind); err != nil {
		t.Skip("no examples/counters beside the module")
	}
	c, err := validate.NewChecker(bind, validate.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	f, err := input.Read("../../tapes/testdata/blind.jsonl", input.Unknown)
	if err != nil {
		t.Fatal(err)
	}
	nav, title, why, err := Failing(c, validate.NavStart{Steps: f.Steps, At: -1})
	if err != nil {
		t.Fatal(err)
	}
	d := drive(t, Config{Title: "model counters: " + title, Model: nav, Why: why})
	s := d.screen()
	filtered := d.keys("/", "showCounter", "enter").screen()
	golden(t, "blind", s, filtered)
	if !strings.Contains(filtered, "› showCounter(") {
		t.Errorf("filtered:\n%s", filtered)
	}
	if !strings.Contains(s, "before call 5") || !strings.Contains(s, "a concurrent append") || !strings.Contains(s, "conditionHolds(log, readPos, reads_) is false") {
		t.Errorf("the failing step's why:\n%s", s)
	}
}
