// Package qnemesis is a nemesis for real deployments: it crashes,
// restarts and pauses processes and containers, and cuts, heals and
// degrades the links between them (toxiproxy), on a Schedule that keeps
// qenv.Script's names and partial order, and records each fault as a
// model step, so the binding that judges a simulated run judges the real
// one, the faults included.
//
//	n := &qnemesis.Nemesis{Targets: map[string]qnemesis.Target{"n1": p1, "n2": p2}, Net: proxy, Rec: rec,
//		Steps: qnemesis.StepsIn(dir + "/n*.jsonl"), Journal: journal}
//	err := n.Run(ctx, qnemesis.Random(seed, mix, 2*time.Second, time.Second))
//	/* ... */
//	err = n.Stop(ctx) // heal every link, resume the paused, start the down
//
// What is recorded, and when, keeps the check fair: a crash after the
// process is reaped (every step it recorded precedes it), a cut after the
// API call returns (it is in effect), a heal before the call (a binding's
// within bound covers the API's latency), a restart before the start (the
// new incarnation's steps follow it). The nemesis records on its own
// recorder (NewRecorder: WithID("nemesis"), a PerStep step log), ordered
// against the system's steps by wall time: the nemesis and the system on
// one host, whose clock both read, so their steps are comparable within
// qtrace.RealTimeSlack. Across hosts, give the steps order keys.
//
// Not here: clock skew (Go reads the time through the vDSO, which
// libfaketime's LD_PRELOAD does not reach) and failpoints.
package qnemesis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
)

// An Event is a fault, or its undo, as it happened: what a Recording
// turns into a model step.
type Event struct {
	// Kind is the fault's ("nemesis.crash.n1") or its undo's
	// ("nemesis.restart.n1", "nemesis.heal.a.b", "nemesis.resume.n1",
	// "nemesis.clear.a.b").
	Kind string
	// Family is Kind's family: crash, restart, pause, resume, partition,
	// isolate, heal, latency, loss, clear.
	Family string
	// Names are the target, or the pair as the kind names it.
	Names []string
	// Incarnation is a crash's incarnation killed, a restart's started (0:
	// the target does not number them).
	Incarnation int
	// Links are the pairs an isolate cut, or a heal of one name healed.
	Links    [][2]string
	Arg      time.Duration
	Toxicity float64
}

// A Recording maps a family (Event.Family) to the model step its events
// are: an action and its arguments, as qobs.Record takes them ("" records
// nothing). A family it lacks records nothing. raftkv's:
//
//	qnemesis.Recording{
//		"crash":   func(e qnemesis.Event) (string, []any) { return "stop", []any{"node", e.Names[0]} },
//		"isolate": func(e qnemesis.Event) (string, []any) { return "isolate", []any{"node", e.Names[0]} },
//		"heal":    func(qnemesis.Event) (string, []any) { return "heal", nil },
//	}
type Recording map[string]func(Event) (action string, kv []any)

// DefaultRecording is what a Nemesis without a Recording records: a
// crash as crash(node), a partition as partition(a, b), an isolate as
// isolate(node), a heal as heal(a, b), or heal(node) for one name's
// links. Pauses, latency and loss record nothing: a model that has them
// maps them.
func DefaultRecording() Recording {
	pair := func(action string) func(Event) (string, []any) {
		return func(e Event) (string, []any) {
			if len(e.Names) == 1 {
				return action, []any{"node", e.Names[0]}
			}
			p := sorted(e.Names[0], e.Names[1])
			return action, []any{"a", p[0], "b", p[1]}
		}
	}
	return Recording{
		"crash":     func(e Event) (string, []any) { return "crash", []any{"node", e.Names[0]} },
		"isolate":   func(e Event) (string, []any) { return "isolate", []any{"node", e.Names[0]} },
		"partition": pair("partition"),
		"heal":      pair("heal"),
	}
}

// NewRecorder opens the nemesis's step log at path (PerStep: a killed
// test loses none of it) and returns its recorder, id "nemesis", with
// opts after (qobs.WithActor, for a binding's actor), and the sink to
// Close.
func NewRecorder(path string, opts ...qobs.Option) (*qobs.Recorder, *qobs.JSONLSink, error) {
	sink, err := qobs.FileSink(path, qobs.PerStep)
	if err != nil {
		return nil, nil, err
	}
	return qobs.NewRecorder(sink, append([]qobs.Option{qobs.WithID("nemesis"), qobs.WithProcess("nemesis")}, opts...)...), sink, nil
}

// StepsIn counts the complete lines of the step logs matching patterns
// (filepath.Glob's: one log per incarnation, "dir/n*.jsonl"), read anew
// at each call from where the last stopped: a Nemesis's Steps, which a
// fault's After waits for. A torn last line is not counted.
func StepsIn(patterns ...string) func() int {
	type seen struct {
		off   int64
		lines int
	}
	var mu sync.Mutex
	files := map[string]*seen{}
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		total := 0
		for _, pat := range patterns {
			paths, _ := filepath.Glob(pat)
			for _, p := range paths {
				s := files[p]
				if s == nil {
					s = &seen{}
					files[p] = s
				}
				if f, err := os.Open(p); err == nil {
					if _, err := f.Seek(s.off, io.SeekStart); err == nil {
						b, _ := io.ReadAll(f)
						if i := strings.LastIndexByte(string(b), '\n'); i >= 0 {
							s.lines += strings.Count(string(b[:i+1]), "\n")
							s.off += int64(i + 1)
						}
					}
					f.Close()
				}
			}
		}
		for _, s := range files {
			total += s.lines
		}
		return total
	}
}

// A Nemesis makes a Schedule's faults on Targets and Net, records each on
// Rec as Record maps it (nil: DefaultRecording; a nil Rec records
// nothing), and journals each fault's start and end (Journal, one JSON
// line each: ReadJournal). Steps counts the system's recorded steps, for
// faults that wait for them (After): StepsIn its step logs.
type Nemesis struct {
	Targets map[string]Target
	Net     Network
	Rec     *qobs.Recorder
	Record  Recording
	Steps   func() int
	Journal io.Writer
	// Poll is how often a fault waiting for steps checks them (default
	// 10ms).
	Poll time.Duration

	mu      sync.Mutex
	sched   Schedule
	done    []bool
	undos   []undo
	cuts    map[[2]string]int // a pair cut, by the fault that cut it
	shaped  map[[2]string]int // a pair with latency or loss
	paused  map[string]int
	down    map[string]int
	stopc   chan struct{}
	stopped bool
	ran     chan struct{} // closed when Run returns
	jmu     sync.Mutex
}

// an undo is a fault's end, due at a time.
type undo struct {
	at    time.Time
	index int
}

func (n *Nemesis) init() {
	if n.cuts == nil {
		n.cuts, n.shaped, n.paused, n.down = map[[2]string]int{}, map[[2]string]int{}, map[string]int{}, map[string]int{}
	}
}

// Run makes the schedule's faults, each when it is due (Fault), and
// returns once every one has happened and been undone if it lasts For;
// or when ctx ends (ctx.Err()), or Stop is called (nil), with the faults
// that never happened in Remaining; or at the first fault that fails (the
// error names it). Faults it made are still in effect then: call Stop.
// A schedule with a misnamed fault (Misnamed) makes none, and fails.
func (n *Nemesis) Run(ctx context.Context, s Schedule) error {
	if msgs := n.misnamed(s); len(msgs) > 0 {
		n.mu.Lock()
		n.sched, n.done = s, make([]bool, len(s))
		n.mu.Unlock()
		return fmt.Errorf("qnemesis: the schedule makes no fault, since %d cannot happen as written:\n  %s", len(msgs), strings.Join(msgs, "\n  "))
	}
	n.mu.Lock()
	if n.ran != nil {
		select {
		case <-n.ran:
		default:
			n.mu.Unlock()
			return errors.New("qnemesis: Run called while another Run of this Nemesis runs: one schedule at a time")
		}
	}
	n.init()
	n.sched, n.done, n.undos = s, make([]bool, len(s)), nil
	n.stopc, n.stopped, n.ran = make(chan struct{}), false, make(chan struct{})
	stopc, ran := n.stopc, n.ran
	n.mu.Unlock()
	defer close(ran)
	start := time.Now()
	poll := n.Poll
	if poll <= 0 {
		poll = 10 * time.Millisecond
	}
	for {
		now := time.Now()
		// a fault's undo before any later fault: Random's "undone before
		// the next" when both are due
		if u, ok := n.dueUndo(now); ok {
			if err := n.end(ctx, u.index); err != nil {
				return err
			}
			continue
		}
		i, wake, waitsSteps := n.due(now.Sub(start))
		if i >= 0 {
			if err := n.apply(ctx, i); err != nil {
				return err
			}
			continue
		}
		n.mu.Lock()
		finished := !slices.Contains(n.done, false) && len(n.undos) == 0
		for _, u := range n.undos {
			if d := u.at.Sub(now); wake < 0 || d < wake {
				wake = d
			}
		}
		n.mu.Unlock()
		if finished {
			return nil
		}
		if waitsSteps && (wake < 0 || wake > poll) {
			wake = poll
		}
		if wake < 0 {
			// nothing to wait for and nothing due: no fault can happen
			// (Misnamed catches the causes; this is the guard)
			return fmt.Errorf("qnemesis: faults %v can never become due: what they follow did not happen", n.Remaining())
		}
		t := time.NewTimer(wake)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-stopc:
			t.Stop()
			return nil
		}
	}
}

// due returns the fault due at elapsed, or -1 with how long until one
// may be (wake, -1 if none waits for time) and whether one waits for
// steps.
func (n *Nemesis) due(elapsed time.Duration) (i int, wake time.Duration, waitsSteps bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	wake = -1
	steps := -1
	first := map[string]bool{} // kinds with an earlier fault not done
	for i, f := range n.sched {
		if n.done[i] {
			continue
		}
		earlier := first[f.Kind]
		first[f.Kind] = true
		if earlier {
			continue // each kind's in order
		}
		ready := true
		for _, j := range f.Follows {
			// what it follows has happened and, if it lasts For, ended:
			// Random's "undone before the next", however late it began
			ready = ready && n.done[j] && !slices.ContainsFunc(n.undos, func(u undo) bool { return u.index == j })
		}
		if !ready {
			continue
		}
		if f.At > elapsed {
			if d := f.At - elapsed; wake < 0 || d < wake {
				wake = d
			}
			continue
		}
		if f.After > 0 {
			if steps < 0 {
				steps = n.Steps()
			}
			if steps < f.After {
				waitsSteps = true
				continue
			}
		}
		return i, wake, waitsSteps
	}
	return -1, wake, waitsSteps
}

func (n *Nemesis) dueUndo(now time.Time) (undo, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	best := -1
	for k, u := range n.undos {
		if !u.at.After(now) && (best < 0 || u.at.Before(n.undos[best].at)) {
			best = k
		}
	}
	if best < 0 {
		return undo{}, false
	}
	u := n.undos[best]
	n.undos = slices.Delete(n.undos, best, best+1)
	return u, true
}

// incarnation is t's current incarnation, 0 if it numbers none.
func incarnation(t Target) int {
	if x, ok := t.(incarnated); ok {
		return x.Incarnation()
	}
	return 0
}

// apply makes fault i, records it, journals its start and schedules its
// undo.
func (n *Nemesis) apply(ctx context.Context, i int) error {
	n.mu.Lock()
	f := n.sched[i]
	n.done[i] = true
	n.mu.Unlock()
	fam, names, _ := parse(f.Kind)
	ev := Event{Kind: f.Kind, Family: fam, Names: names, Arg: f.Arg, Toxicity: f.Toxicity}
	fail := func(err error) error { return fmt.Errorf("qnemesis: fault %d (%v): %w", i, f, err) }
	later := func() {
		if f.For > 0 { // For from when the fault took effect
			n.mu.Lock()
			n.undos = append(n.undos, undo{time.Now().Add(f.For), i})
			n.mu.Unlock()
		}
	}
	switch fam {
	case "crash":
		p, t := names[0], n.Targets[names[0]]
		n.mu.Lock()
		_, isDown := n.down[p]
		n.mu.Unlock()
		if isDown {
			return fail(fmt.Errorf("%s is down already (an earlier crash's For has not passed): give that crash a shorter For, or make this one follow its restart", p))
		}
		ev.Incarnation = incarnation(t)
		if err := t.Kill(ctx); err != nil {
			return fail(err)
		}
		n.mu.Lock()
		n.down[p] = i
		n.mu.Unlock()
		if err := n.after(ctx, i, ev); err != nil {
			return err
		}
		if f.For == 0 {
			return n.end(ctx, i)
		}
	case "pause":
		if err := n.Targets[names[0]].Pause(ctx); err != nil {
			return fail(err)
		}
		n.mu.Lock()
		n.paused[names[0]] = i
		n.mu.Unlock()
		if err := n.after(ctx, i, ev); err != nil {
			return err
		}
	case "resume":
		return n.resume(ctx, i, names[0], f.Kind, "start")
	case "partition", "isolate":
		pairs := [][2]string{sorted(names[0], names[len(names)-1])}
		if fam == "isolate" {
			pairs = n.pairsOf(names[0], nil)
			ev.Links = pairs
		}
		for _, p := range pairs {
			if err := n.Net.Cut(ctx, p[0], p[1]); err != nil {
				return fail(err)
			}
			n.mu.Lock()
			n.cuts[p] = i
			n.mu.Unlock()
		}
		if err := n.after(ctx, i, ev); err != nil {
			return err
		}
	case "heal":
		return n.heal(ctx, i, f.Kind, names, "start")
	case "latency", "loss":
		a, b := names[0], names[1]
		var err error
		if fam == "latency" {
			err = n.Net.Latency(ctx, a, b, f.Arg, f.Arg/2)
		} else {
			tox, timeout := f.Toxicity, f.Arg
			if tox == 0 {
				tox = 1
			}
			if timeout == 0 {
				timeout = DefaultLossTimeout
			}
			err = n.Net.Loss(ctx, a, b, tox, timeout)
		}
		if err != nil {
			return fail(err)
		}
		n.mu.Lock()
		n.shaped[sorted(a, b)] = i
		n.mu.Unlock()
		if err := n.after(ctx, i, ev); err != nil {
			return err
		}
	}
	later()
	return nil
}

// pairsOf are the linked pairs that include name; with cut set, only
// those cut.
func (n *Nemesis) pairsOf(name string, cut map[[2]string]int) [][2]string {
	var out [][2]string
	for _, p := range n.Net.Pairs() {
		if p[0] != name && p[1] != name {
			continue
		}
		if cut != nil {
			if _, ok := cut[p]; !ok {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// after records a fault's event once it is in effect, and journals its
// start.
func (n *Nemesis) after(ctx context.Context, i int, ev Event) error {
	rec := n.record(ctx, ev)
	return n.journal(Entry{Index: i, Kind: ev.Kind, Event: "start", Incarnation: ev.Incarnation, Links: ev.Links, Recorded: rec})
}

// end undoes fault i: a crash's restart, a cut's heal, a pause's resume,
// a shaped link's clear.
func (n *Nemesis) end(ctx context.Context, i int) error {
	n.mu.Lock()
	f := n.sched[i]
	n.mu.Unlock()
	fam, names, _ := parse(f.Kind)
	switch fam {
	case "crash":
		return n.restart(ctx, i, names[0])
	case "pause":
		return n.resume(ctx, i, names[0], "nemesis.resume."+names[0], "end")
	case "partition":
		return n.heal(ctx, i, "nemesis.heal."+names[0]+"."+names[1], names, "end")
	case "isolate":
		return n.heal(ctx, i, "nemesis.heal."+names[0], names, "end")
	case "latency", "loss":
		return n.clear(ctx, i, sorted(names[0], names[1]))
	}
	return nil
}

// restart starts target p's next incarnation, recorded before it starts
// (its steps follow the restart's).
func (n *Nemesis) restart(ctx context.Context, i int, p string) error {
	t := n.Targets[p]
	n.mu.Lock()
	delete(n.down, p)
	n.mu.Unlock()
	if t.Alive() {
		return nil // started by someone else: no restart of ours
	}
	ev := Event{Kind: "nemesis.restart." + p, Family: "restart", Names: []string{p}, Incarnation: incarnation(t) + 1}
	rec := n.record(ctx, ev)
	if err := n.journal(Entry{Index: i, Kind: ev.Kind, Event: "end", Incarnation: ev.Incarnation, Recorded: rec}); err != nil {
		return err
	}
	if err := t.Start(ctx); err != nil {
		return fmt.Errorf("qnemesis: restart %s: %w", p, err)
	}
	return nil
}

// resume resumes p, recorded before (p runs once it is resumed).
func (n *Nemesis) resume(ctx context.Context, i int, p, kind, event string) error {
	n.mu.Lock()
	_, paused := n.paused[p]
	delete(n.paused, p)
	n.mu.Unlock()
	if !paused && event == "end" {
		return nil // resumed already by a fault of the schedule
	}
	ev := Event{Kind: kind, Family: "resume", Names: []string{p}}
	rec := n.record(ctx, ev)
	if err := n.journal(Entry{Index: i, Kind: kind, Event: event, Recorded: rec}); err != nil {
		return err
	}
	if err := n.Targets[p].Resume(ctx); err != nil {
		return fmt.Errorf("qnemesis: resume %s: %w", p, err)
	}
	return nil
}

// heal heals the pair names, or every cut pair of the one name; recorded
// before the calls (a within bound covers their latency). A heal of
// nothing cut, at a fault's end, does nothing: a fault of the schedule
// healed it.
func (n *Nemesis) heal(ctx context.Context, i int, kind string, names []string, event string) error {
	n.mu.Lock()
	var pairs [][2]string
	if len(names) == 2 {
		if _, cut := n.cuts[sorted(names[0], names[1])]; cut || event == "start" {
			pairs = [][2]string{sorted(names[0], names[1])}
		}
	} else {
		cuts := n.cuts
		if event == "start" {
			cuts = nil // heal.<p> heals every link of p's, cut or not
		}
		n.mu.Unlock()
		pairs = n.pairsOf(names[0], cuts)
		n.mu.Lock()
	}
	for _, p := range pairs {
		delete(n.cuts, p)
	}
	n.mu.Unlock()
	if len(pairs) == 0 && event == "end" {
		return nil
	}
	ev := Event{Kind: kind, Family: "heal", Names: names}
	if len(names) == 1 {
		ev.Links = pairs
	}
	rec := n.record(ctx, ev)
	if err := n.journal(Entry{Index: i, Kind: kind, Event: event, Links: ev.Links, Recorded: rec}); err != nil {
		return err
	}
	for _, p := range pairs {
		if err := n.Net.Heal(ctx, p[0], p[1]); err != nil {
			return fmt.Errorf("qnemesis: %s: %w", kind, err)
		}
	}
	return nil
}

// clear removes a pair's latency and loss.
func (n *Nemesis) clear(ctx context.Context, i int, p [2]string) error {
	n.mu.Lock()
	_, shaped := n.shaped[p]
	delete(n.shaped, p)
	n.mu.Unlock()
	if !shaped {
		return nil
	}
	kind := "nemesis.clear." + p[0] + "." + p[1]
	rec := n.record(ctx, Event{Kind: kind, Family: "clear", Names: p[:]})
	if err := n.journal(Entry{Index: i, Kind: kind, Event: "end", Recorded: rec}); err != nil {
		return err
	}
	if err := n.Net.Clear(ctx, p[0], p[1]); err != nil {
		return fmt.Errorf("qnemesis: %s: %w", kind, err)
	}
	return nil
}

// record records ev as Record maps it, reporting whether it recorded a
// step.
func (n *Nemesis) record(ctx context.Context, ev Event) bool {
	if n.Rec == nil {
		return false
	}
	r := n.Record
	if r == nil {
		r = DefaultRecording()
	}
	f := r[ev.Family]
	if f == nil {
		return false
	}
	action, kv := f(ev)
	if action == "" {
		return false
	}
	n.Rec.Record(ctx, action, nil, kv...)
	return true
}

// Stop ends a Run (waiting for it to return) and settles the system: it
// heals every link the nemesis cut and clears every one it degraded,
// resumes what it paused, and starts every target that is down, whoever
// stopped it, each recorded as a fault's end is. It is the real
// counterpart of qenv's Settle, without the waiting: after it, run the
// probe that judges the system settled (and qobs.Settled). Errors do not
// stop it: it returns them all.
func (n *Nemesis) Stop(ctx context.Context) error {
	n.mu.Lock()
	n.init()
	if n.stopc != nil && !n.stopped {
		n.stopped = true
		close(n.stopc)
	}
	ran := n.ran
	n.mu.Unlock()
	if ran != nil {
		select {
		case <-ran:
		case <-ctx.Done():
			return fmt.Errorf("qnemesis: Stop: the Run did not return (a fault's call hangs): %w", ctx.Err())
		}
	}
	n.mu.Lock()
	n.undos = nil
	cuts, shaped, paused := sortedKeys(n.cuts), sortedKeys(n.shaped), keysOf(n.paused)
	n.mu.Unlock()
	var errs []error
	for _, p := range cuts {
		n.mu.Lock()
		i := n.cuts[p]
		n.mu.Unlock()
		errs = append(errs, n.heal(ctx, i, "nemesis.heal."+p[0]+"."+p[1], p[:], "end"))
	}
	for _, p := range shaped {
		n.mu.Lock()
		i := n.shaped[p]
		n.mu.Unlock()
		errs = append(errs, n.clear(ctx, i, p))
	}
	for _, p := range paused {
		n.mu.Lock()
		i := n.paused[p]
		n.mu.Unlock()
		errs = append(errs, n.resume(ctx, i, p, "nemesis.resume."+p, "end"))
	}
	for _, p := range keysOf(n.Targets) {
		n.mu.Lock()
		i, ok := n.down[p]
		n.mu.Unlock()
		if !ok {
			i = -1 // down by itself: no fault of ours to end
		}
		errs = append(errs, n.restart(ctx, i, p))
	}
	return errors.Join(errs...)
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[[2]string]int) [][2]string {
	out := make([][2]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] || out[i][0] == out[j][0] && out[i][1] < out[j][1] })
	return out
}

// Remaining are the faults of the last Run's schedule that never
// happened: a run that ended (ctx, Stop) before they were due, or steps
// that never came.
func (n *Nemesis) Remaining() []Fault {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []Fault
	for i, f := range n.sched {
		if !n.done[i] {
			out = append(out, f)
		}
	}
	return out
}

// Misnamed says what is wrong with each fault of the last Run's schedule
// that cannot happen as written (Run refuses such a schedule): qenv
// Script.Misnamed's rule for a real deployment's names.
func (n *Nemesis) Misnamed() []string {
	n.mu.Lock()
	s := n.sched
	n.mu.Unlock()
	return n.misnamed(s)
}

// misnamed is Misnamed of s: a kind of no family, a target or a link
// that does not exist, the "fault." prefix, a simulated double's kind,
// a Follows of no other fault or in a cycle, an After without Steps.
func (n *Nemesis) misnamed(s Schedule) []string {
	var out []string
	targets := keysOf(n.Targets)
	var pairs [][2]string
	if n.Net != nil {
		pairs = n.Net.Pairs()
	}
	linked := func(a, b string) bool { return slices.Contains(pairs, sorted(a, b)) }
	var pairNames []string
	for _, p := range pairs {
		pairNames = append(pairNames, p[0]+"."+p[1])
	}
	for i, f := range s {
		bad := func(format string, a ...any) {
			out = append(out, fmt.Sprintf("schedule fault %d (%v): ", i, f)+fmt.Sprintf(format, a...))
		}
		for _, j := range f.Follows {
			if j < 0 || j >= len(s) || j == i {
				bad("follows %d, which is not another fault of the schedule", j)
			}
		}
		if f.After > 0 && n.Steps == nil {
			bad("After %d counts the system's steps, and the Nemesis has no Steps: set it (qnemesis.StepsIn(the step logs)), or time the fault with At", f.After)
		}
		if strings.HasPrefix(f.Kind, "fault.") {
			bad("name its kind without the \"fault.\" prefix (%q), as reports print it", strings.TrimPrefix(f.Kind, "fault."))
			continue
		}
		fam, names, ok := parse(f.Kind)
		if !ok {
			bad("%q is a simulated double's fault, which a real deployment has no seam for: a Schedule makes nemeses (\"nemesis.crash.<target>\", \"nemesis.partition.<a>.<b>\")", f.Kind)
			continue
		}
		shape, known := families[fam]
		if !known {
			bad("%q is no nemesis family of a real deployment: the families are %s (\"nemesis.<family>.<target>\" or \".<a>.<b>\")", fam, strings.Join(keysOf(families), ", "))
			continue
		}
		switch {
		case len(names) == 0 || len(names) == 1 && names[0] == "":
			bad("%q is a family, not a fault: name its target or pair (%q)", f.Kind, f.Kind+".<name>")
		case len(names) == 1 && shape == "pair":
			bad("%s names a pair: %q", fam, "nemesis."+fam+".<a>.<b>")
		case len(names) == 2 && shape == "target":
			bad("%s names one target: %q (a name cannot contain a dot)", fam, "nemesis."+fam+".<target>")
		case len(names) > 2:
			bad("%q has %d names: a target or a pair, and a name cannot contain a dot", f.Kind, len(names))
		case len(names) == 1 && (fam == "crash" || fam == "pause" || fam == "resume"):
			if _, ok := n.Targets[names[0]]; !ok {
				bad("%s", noSuch("target", names[0], targets, "Nemesis.Targets"))
			}
		case len(names) == 1: // isolate, heal of one name
			if n.Net == nil {
				bad("no Network: set Nemesis.Net (a Toxiproxy, with a Link per directed link)")
			} else if len(n.pairsOf(names[0], nil)) == 0 {
				bad("%s has no link to cut or heal: make its links with Toxiproxy.Link (links: %s)", names[0], strings.Join(pairNames, ", "))
			}
		case n.Net == nil:
			bad("no Network: set Nemesis.Net (a Toxiproxy, with a Link per directed link)")
		case !linked(names[0], names[1]):
			bad("%s", noSuch("link", names[0]+"."+names[1], pairNames, "Toxiproxy.Link"))
		}
		switch {
		case fam == "latency" && f.Arg <= 0:
			bad("a latency fault's Arg is the latency: give it one")
		case fam == "loss" && (f.Toxicity < 0 || f.Toxicity > 1):
			bad("Toxicity %v is a share of connections, from 0 (all of them) to 1", f.Toxicity)
		}
	}
	if c := cycle(s); c != nil {
		out = append(out, fmt.Sprintf("schedule faults %v wait on each other (Follows, and each kind's faults in order): none of them can happen", c))
	}
	return out
}

// noSuch names a missing target or link, the nearest one there is, and
// where they come from.
func noSuch(what, name string, have []string, from string) string {
	msg := fmt.Sprintf("no %s %q (%ss: %s; from %s)", what, name, what, strings.Join(have, ", "), from)
	if near := nearest(name, have); near != "" {
		msg += fmt.Sprintf(": did you mean %s?", near)
	}
	return msg
}

// nearest is the name of have closest to name in edit distance, if close.
func nearest(name string, have []string) string {
	best, bestD := "", -1
	for _, h := range have {
		if d := distance(name, h); bestD < 0 || d < bestD {
			best, bestD = h, d
		}
	}
	if bestD >= 0 && bestD <= max(2, len(name)/4) {
		return best
	}
	return ""
}

func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(b)]
}

// cycle returns faults that wait on each other, or nil: the order's
// edges are Follows and each kind's earlier fault.
func cycle(s Schedule) []int {
	edges := make([][]int, len(s)) // i waits for edges[i]
	last := map[string]int{}
	for i, f := range s {
		for _, j := range f.Follows {
			if j >= 0 && j < len(s) && j != i {
				edges[i] = append(edges[i], j)
			}
		}
		if j, ok := last[f.Kind]; ok {
			edges[i] = append(edges[i], j)
		}
		last[f.Kind] = i
	}
	state := make([]int8, len(s)) // 0 unseen, 1 on the path, 2 done
	var path []int
	var found []int
	var visit func(i int) bool
	visit = func(i int) bool {
		state[i] = 1
		path = append(path, i)
		for _, j := range edges[i] {
			if state[j] == 1 {
				k := slices.Index(path, j)
				found = slices.Clone(path[k:])
				return true
			}
			if state[j] == 0 && visit(j) {
				return true
			}
		}
		path = path[:len(path)-1]
		state[i] = 2
		return false
	}
	for i := range s {
		if state[i] == 0 && visit(i) {
			sort.Ints(found)
			return found
		}
	}
	return nil
}

// journal writes an entry, timed and counted now.
func (n *Nemesis) journal(e Entry) error {
	if n.Journal == nil {
		return nil
	}
	e.Time = time.Now()
	if n.Steps != nil {
		c := n.Steps()
		e.Steps = &c
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	n.jmu.Lock()
	defer n.jmu.Unlock()
	if _, err := n.Journal.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("qnemesis: journal: %w", err)
	}
	return nil
}
