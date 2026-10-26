package qnemesis

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
)

// calls is a log of the fakes' calls, timed.
type calls struct {
	mu    sync.Mutex
	start time.Time
	log   []call
}

type call struct {
	what string
	at   time.Duration // since start
	end  time.Time     // when the call returned
}

func (c *calls) add(what string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.log = append(c.log, call{what, now.Sub(c.start), now})
}

func (c *calls) names() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, x := range c.log {
		out = append(out, x.what)
	}
	return out
}

func (c *calls) of(what string) call {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.log {
		if x.what == what {
			return x
		}
	}
	return call{at: -1}
}

// fakeTarget is a Target that logs its calls.
type fakeTarget struct {
	name  string
	c     *calls
	mu    sync.Mutex
	alive bool
	inc   int
}

func (f *fakeTarget) Kill(context.Context) error {
	f.mu.Lock()
	f.alive = false
	f.mu.Unlock()
	f.c.add("kill " + f.name)
	return nil
}
func (f *fakeTarget) Start(context.Context) error {
	f.mu.Lock()
	f.alive = true
	f.inc++
	f.mu.Unlock()
	f.c.add("start " + f.name)
	return nil
}
func (f *fakeTarget) Pause(context.Context) error  { f.c.add("pause " + f.name); return nil }
func (f *fakeTarget) Resume(context.Context) error { f.c.add("resume " + f.name); return nil }
func (f *fakeTarget) Alive() bool                  { f.mu.Lock(); defer f.mu.Unlock(); return f.alive }
func (f *fakeTarget) Incarnation() int             { f.mu.Lock(); defer f.mu.Unlock(); return f.inc }

// fakeNet is a Network of fixed pairs that logs its calls.
type fakeNet struct {
	c     *calls
	pairs [][2]string
}

func (n *fakeNet) Cut(_ context.Context, a, b string) error {
	n.c.add("cut " + a + "." + b)
	return nil
}
func (n *fakeNet) Heal(_ context.Context, a, b string) error {
	n.c.add("heal " + a + "." + b)
	return nil
}
func (n *fakeNet) Latency(_ context.Context, a, b string, d, j time.Duration) error {
	n.c.add(fmt.Sprintf("latency %s.%s %v±%v", a, b, d, j))
	return nil
}
func (n *fakeNet) Loss(_ context.Context, a, b string, tox float64, timeout time.Duration) error {
	n.c.add(fmt.Sprintf("loss %s.%s %v %v", a, b, tox, timeout))
	return nil
}
func (n *fakeNet) Clear(_ context.Context, a, b string) error {
	n.c.add("clear " + a + "." + b)
	return nil
}
func (n *fakeNet) Pairs() [][2]string { return n.pairs }

// fakes is a nemesis over targets a, b, c (running) linked a-b, a-c,
// b-c, recording into memory.
func fakes() (*Nemesis, *calls, *qobs.MemorySink) {
	c := &calls{start: time.Now()}
	mem := &qobs.MemorySink{}
	ts := map[string]Target{}
	for _, name := range []string{"a", "b", "c"} {
		ts[name] = &fakeTarget{name: name, c: c, alive: true, inc: 1}
	}
	n := &Nemesis{Targets: ts, Net: &fakeNet{c: c, pairs: [][2]string{{"a", "b"}, {"a", "c"}, {"b", "c"}}}, Rec: qobs.NewRecorder(mem, qobs.WithID("nemesis"))}
	return n, c, mem
}

// Faults happen in Script's partial order: a fault waits for its time,
// for the steps before it, for what it Follows and for its kind's earlier
// faults, and for nothing else.
func TestScheduleOrder(t *testing.T) {
	t.Parallel()
	n, c, _ := fakes()
	var steps atomic.Int64
	n.Steps = func() int { return int(steps.Load()) }
	s := Schedule{
		{Kind: "nemesis.crash.a", At: 80 * time.Millisecond},  // 0
		{Kind: "nemesis.pause.b", Follows: []int{0}},          // 1: after 0
		{Kind: "nemesis.crash.b"},                             // 2: at once
		{Kind: "nemesis.crash.a"},                             // 3: after 0, its kind's
		{Kind: "nemesis.partition.a.b", After: 3},             // 4: after 3 steps
		{Kind: "nemesis.heal.a.b", At: 10 * time.Millisecond}, // 5: on its own
	}
	var stepsAt time.Duration
	c.start = time.Now()
	go func() {
		time.Sleep(150 * time.Millisecond)
		stepsAt = time.Since(c.start)
		steps.Store(3)
	}()
	if err := n.Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(c.names(), ", ")
	want := "kill b, start b, heal a.b, kill a, start a, pause b, kill a, start a, cut a.b"
	if got != want {
		t.Fatalf("calls %s;\nwant %s", got, want)
	}
	if at := c.of("kill a").at; at < 80*time.Millisecond {
		t.Errorf("crash a at %v, before its At", at)
	}
	if at := c.of("cut a.b").at; at < stepsAt || stepsAt == 0 {
		t.Errorf("the partition at %v, before the steps it waits for (%v)", at, stepsAt)
	}
	if r := n.Remaining(); len(r) != 0 {
		t.Errorf("remaining %v", r)
	}
}

// A fault with For is undone after it; what is recorded keeps the check
// fair: a cut once in effect (after the call), a heal before the call.
func TestForUndoesAndRecordingOrder(t *testing.T) {
	t.Parallel()
	n, c, mem := fakes()
	if err := n.Run(context.Background(), Schedule{{Kind: "nemesis.partition.a.b", For: 50 * time.Millisecond}}); err != nil {
		t.Fatal(err)
	}
	cut, heal := c.of("cut a.b"), c.of("heal a.b")
	if heal.at < cut.at+50*time.Millisecond {
		t.Fatalf("healed at %v, cut at %v: want For (50ms) between", heal.at, cut.at)
	}
	steps := mem.Steps()
	if len(steps) != 2 || steps[0].Action != "partition" || steps[1].Action != "heal" || steps[0].Args["a"] != "a" || steps[0].Args["b"] != "b" {
		t.Fatalf("recorded %+v; want partition(a, b), heal(a, b)", steps)
	}
	if steps[0].Time.Before(cut.end) {
		t.Errorf("the partition was recorded (%v) before the cut was in effect (%v)", steps[0].Time, cut.end)
	}
	if !steps[1].Time.Before(heal.end) {
		t.Errorf("the heal was recorded (%v) after the network healed (%v)", steps[1].Time, heal.end)
	}
}

// Stop settles: every link the nemesis cut healed (an isolate's too),
// every degraded one cleared, the paused resumed, the down started, each
// recorded as a fault's end; and it ends a Run still waiting.
func TestStopSettles(t *testing.T) {
	t.Parallel()
	n, c, mem := fakes()
	ctx := context.Background()
	s := Schedule{
		{Kind: "nemesis.isolate.c"},
		{Kind: "nemesis.latency.a.b", Arg: 100 * time.Millisecond},
		{Kind: "nemesis.pause.a"},
		{Kind: "nemesis.crash.b", For: time.Hour},
		{Kind: "nemesis.crash.a", At: time.Hour},
	}
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx, s) }()
	for n.Targets["b"].Alive() {
		time.Sleep(time.Millisecond)
	}
	if err := n.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := strings.Join(c.names(), ", ")
	want := "cut a.c, cut b.c, latency a.b 100ms±50ms, pause a, kill b, heal a.c, heal b.c, clear a.b, resume a, start b"
	if got != want {
		t.Fatalf("calls %s;\nwant %s", got, want)
	}
	var rec []string
	for _, st := range mem.Steps() {
		rec = append(rec, fmt.Sprintf("%s %v", st.Action, st.Args))
	}
	if want := "isolate map[node:c] crash map[node:b] heal map[a:a b:c] heal map[a:b b:c]"; strings.Join(rec, " ") != want {
		t.Errorf("recorded %s;\nwant %s", strings.Join(rec, " "), want)
	}
	if r := n.Remaining(); len(r) != 1 || r[0].Kind != "nemesis.crash.a" {
		t.Errorf("remaining %v; want the crash of a an hour in", r)
	}
}

// Random draws one fault per interval, each undone after its For, before
// the next begins; a seed draws the same schedule every time.
func TestRandom(t *testing.T) {
	t.Parallel()
	mix := Mix{Kinds: append(Kinds("crash", "a", "b"), append(Kinds("partition", "b", "a", "c"), "nemesis.latency.a.b", "nemesis.pause.c")...), Faults: 12, Latency: 5 * time.Millisecond}
	if got := Kinds("partition", "b", "a", "c"); !reflect.DeepEqual(got, []string{"nemesis.partition.a.b", "nemesis.partition.b.c", "nemesis.partition.a.c"}) {
		t.Fatalf("Kinds(partition): %v", got)
	}
	s := Random(7, mix, 20*time.Millisecond, 10*time.Millisecond)
	if !reflect.DeepEqual(s, Random(7, mix, 20*time.Millisecond, 10*time.Millisecond)) {
		t.Fatal("one seed, two schedules")
	}
	if reflect.DeepEqual(s, Random(8, mix, 20*time.Millisecond, 10*time.Millisecond)) {
		t.Fatal("two seeds, one schedule")
	}
	kinds := map[string]bool{}
	for i, f := range s {
		kinds[f.Kind] = true
		if f.At != time.Duration(i)*20*time.Millisecond || f.For != 10*time.Millisecond || i > 0 && !reflect.DeepEqual(f.Follows, []int{i - 1}) {
			t.Fatalf("fault %d: %+v", i, f)
		}
		if strings.HasPrefix(f.Kind, "nemesis.latency.") && f.Arg != 5*time.Millisecond {
			t.Fatalf("latency without the mix's: %+v", f)
		}
	}
	if len(kinds) < 3 {
		t.Fatalf("12 faults of %d kinds: %v", len(kinds), kinds)
	}
	n, c, _ := fakes()
	var journal strings.Builder
	n.Journal = &journal
	if err := n.Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	j, err := ReadJournal(strings.NewReader(journal.String()))
	if err != nil {
		t.Fatal(err)
	}
	// each fault's start, then its end, then the next's start
	if len(j.Entries) != 2*len(s) {
		t.Fatalf("%d journal entries for %d faults (%v)", len(j.Entries), len(s), c.names())
	}
	for k, e := range j.Entries {
		if e.Index != k/2 || (e.Event == "start") != (k%2 == 0) {
			t.Fatalf("journal entry %d: %+v; want fault %d's %s", k, e, k/2, map[bool]string{true: "start", false: "end"}[k%2 == 0])
		}
	}
	for _, bad := range []func(){
		func() { Random(1, mix, time.Second, time.Second) },
		func() { Random(1, Mix{Faults: 1}, time.Second, time.Millisecond) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			bad()
		}()
	}
}

// A fault that cannot happen as written makes Run refuse the schedule,
// doing nothing, each with the fix (qenv's Script.Misnamed's rule).
func TestMisnamed(t *testing.T) {
	t.Parallel()
	n, c, _ := fakes()
	n.Targets["n1"] = &fakeTarget{name: "n1", c: c, alive: true}
	s := Schedule{
		{Kind: "nemesis.crash.n2"},                    // 0: no target, near n1
		{Kind: "fault.nemesis.crash.a"},               // 1
		{Kind: "rpc.payments.response"},               // 2: a double's
		{Kind: "nemesis.crash"},                       // 3: a family
		{Kind: "nemesis.partition.a.n1"},              // 4: no link
		{Kind: "nemesis.crash.a", After: 3},           // 5: no Steps
		{Kind: "nemesis.pause.a", Follows: []int{99}}, // 6
		{Kind: "nemesis.jump.a"},                      // 7: no family here
		{Kind: "nemesis.resume.b", Follows: []int{9}}, // 8
		{Kind: "nemesis.latency.a.b"},                 // 9: no latency
		{Kind: "nemesis.pause.b", Follows: []int{11}}, // 10: a cycle with 11
		{Kind: "nemesis.pause.c", Follows: []int{10}}, // 11
		{Kind: "nemesis.partition.a"},                 // 12: one name
		{Kind: "nemesis.isolate.a.b"},                 // 13: two
		{Kind: "nemesis.heal.c"},                      // 14: fine
	}
	err := n.Run(context.Background(), s)
	if err == nil {
		t.Fatal("a misnamed schedule ran")
	}
	if len(c.names()) != 0 {
		t.Fatalf("a refused schedule made faults: %v", c.names())
	}
	msgs := n.Misnamed()
	for _, want := range []string{
		`fault 0 (nemesis.crash.n2): no target "n2"`, "did you mean n1?",
		`fault 1 (fault.nemesis.crash.a): name its kind without the "fault." prefix ("nemesis.crash.a")`,
		`fault 2 (rpc.payments.response): "rpc.payments.response" is a simulated double's fault`,
		`fault 3 (nemesis.crash): "nemesis.crash" is a family, not a fault`,
		`fault 4 (nemesis.partition.a.n1): no link "a.n1"`, "from Toxiproxy.Link",
		"fault 5 (nemesis.crash.a after 3 steps): After 3 counts the system's steps, and the Nemesis has no Steps",
		"fault 6 (nemesis.pause.a): follows 99, which is not another fault",
		`fault 7 (nemesis.jump.a): "jump" is no nemesis family of a real deployment`,
		"fault 9 (nemesis.latency.a.b): a latency fault's Arg is the latency",
		"schedule faults [10 11] wait on each other",
		"fault 12 (nemesis.partition.a): partition names a pair",
		"fault 13 (nemesis.isolate.a.b): isolate names one target",
	} {
		if !strings.Contains(strings.Join(msgs, "\n"), want) {
			t.Errorf("no message with %q in:\n%s", want, strings.Join(msgs, "\n"))
		}
	}
	for _, m := range msgs {
		if strings.Contains(m, "fault 8 ") || strings.Contains(m, "fault 14 ") {
			t.Errorf("a well-named fault reported: %s", m)
		}
	}
	if !strings.Contains(err.Error(), "makes no fault") {
		t.Errorf("Run's error: %v", err)
	}
	// without a Network, a link's fault says to set one
	n.Net = nil
	_ = n.Run(context.Background(), Schedule{{Kind: "nemesis.partition.a.b"}})
	if m := n.Misnamed(); len(m) != 1 || !strings.Contains(m[0], "set Nemesis.Net") {
		t.Errorf("without a Network: %v", m)
	}
}
