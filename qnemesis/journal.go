package qnemesis

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// FromScript is a qenv Script's faults as a Schedule (a Quint trace's,
// through qtest.FaultScript): the same kinds, After and Follows; a crash
// restarts at once, as qenv's does. After counts the system's steps
// (Nemesis.Steps), which is the trace's count only when the trace's steps
// are the program's own, recorded where the simulation records them;
// otherwise time the faults with At. A double's fault (no "nemesis."
// prefix) has no seam in a real deployment: Run refuses it, and Misnamed
// says so.
func FromScript(sc *qenv.Script) Schedule {
	s := make(Schedule, len(sc.Faults))
	for i, f := range sc.Faults {
		s[i] = Fault{Kind: f.Kind, After: f.After, Follows: append([]int(nil), f.Follows...)}
	}
	return s
}

// An Entry is a line of a nemesis's journal: a fault's start (Event
// "start") or its end ("end": a restart, a heal, a resume, a clear, by
// its For or by Stop), when it happened and how many steps the system had
// recorded then (Steps, if the Nemesis counts them).
type Entry struct {
	// Index is the fault's in the schedule; -1 for Stop's start of a
	// target no fault of the schedule had stopped.
	Index int       `json:"index"`
	Kind  string    `json:"kind"`
	Event string    `json:"event"`
	Time  time.Time `json:"time"`
	Steps *int      `json:"steps,omitempty"`
	// Incarnation is a crash's incarnation killed, a restart's started.
	Incarnation int `json:"incarnation,omitempty"`
	// Links are the pairs an isolate cut, or a heal of one name healed.
	Links [][2]string `json:"links,omitempty"`
	// Recorded: the nemesis recorded a step for it.
	Recorded bool `json:"recorded,omitempty"`
}

// A Journal is a nemesis's journal read back: every fault that happened,
// in the order it happened.
type Journal struct{ Entries []Entry }

// ReadJournal reads a journal (Nemesis.Journal's lines). A torn last
// line (the test killed while writing it) is dropped, as qtrace.ReadJSONL
// drops a torn step.
func ReadJournal(r io.Reader) (*Journal, error) {
	j := &Journal{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	var torn error
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		if torn != nil {
			return nil, torn // a bad line with lines after it is no tear
		}
		var e Entry
		if err := json.Unmarshal([]byte(text), &e); err != nil {
			torn = fmt.Errorf("qnemesis: journal line %d: %w", line, err)
			continue
		}
		j.Entries = append(j.Entries, e)
	}
	return j, sc.Err()
}

// Script is the journal's faults as a qenv Script, for a failed real
// run's faults re-made in a simulation: every fault the simulation can
// make, in the order it happened, at the first chance it has (no After).
// Crashes, pauses, partitions and heals map to qenv's nemeses of the same
// names (a pair sorted, as qenv names it; an isolate is a partition of
// each of its links, a heal of one name a heal of each link the journal
// shows cut); a crash's restart and a pause's resume are qenv's own.
//
// The trade: the faults keep their order, not their timing against the
// workload. A real run's step counts (its clients' real requests, its
// nodes' own logs) are not a simulated run's, which takes fewer steps or
// others, so faults timed by them were never reached. StepScript keeps
// them, for a journal whose steps are the simulated program's own.
//
// Each fault Follows the last fault before it that is no heal; a heal
// follows the last fault, and nothing waits for it. qenv heals a
// partition by itself after Network.CutLength (1 virtual second by
// default): a heal of a cut already healed so never happens, and stays
// in Remaining, where a chain through it would have stopped every fault
// after it. Set CutLength to LongestCut, or longer, to keep each
// partition until its heal. What the script cannot make as it happened
// is in Unreproducible; after the run, Unmade says which faults the
// simulation did not make, and why (a fault it cannot make holds up every
// fault after it).
func (j *Journal) Script() *qenv.Script { return j.script(false) }

// StepScript is Script with each fault After the steps the system had
// recorded then plus the faults recorded before it (a simulation that
// records its faults as steps counts them, as qtest.FaultScript does):
// the faults timed against the workload, which only a journal of the
// simulated program's own steps can reproduce (a run whose step counts
// a simulation repeats). A real run's journal's later faults are
// otherwise never reached.
func (j *Journal) StepScript() *qenv.Script { return j.script(true) }

func (j *Journal) script(steps bool) *qenv.Script {
	sc := &qenv.Script{}
	recorded := 0
	cut := map[[2]string]bool{}
	lastFault := -1 // the last scripted fault that is no heal
	add := func(kind string, e Entry, heal bool) {
		f := qenv.Scripted{Kind: kind}
		if steps && e.Steps != nil {
			f.After = *e.Steps + recorded
		}
		prev := lastFault
		if heal {
			prev = len(sc.Faults) - 1
		}
		if prev >= 0 {
			f.Follows = []int{prev}
		}
		sc.Faults = append(sc.Faults, f)
		if !heal {
			lastFault = len(sc.Faults) - 1
		}
	}
	for _, e := range j.Entries {
		fam, names, _ := parse(e.Kind)
		var pairs [][2]string
		if len(names) == 2 {
			pairs = [][2]string{sorted(names[0], names[1])}
		} else {
			pairs = e.Links
		}
		switch {
		case (fam == "crash" || fam == "pause") && len(names) == 1:
			add(e.Kind, e, false)
		case fam == "partition" || fam == "isolate":
			for _, p := range pairs {
				cut[p] = true
				add("nemesis.partition."+p[0]+"."+p[1], e, false)
			}
		case fam == "heal":
			for _, p := range pairs {
				if cut[p] { // qenv's heal of a link not cut never happens
					delete(cut, p)
					add("nemesis.heal."+p[0]+"."+p[1], e, true)
				}
			}
		}
		if e.Recorded {
			recorded++
		}
	}
	return sc
}

// Unmade says why a simulation did not make every fault of a journal's
// script (nil if it did; ask after the run): each fault the simulation
// could not make, as Script.Misnamed names it (a pause without
// Sim.Stalls.Pause, a partition without PartitionNemesis: no such
// nemesis), each fault left waiting for one that never happened, and
// each the run never came to. Script keeps the journal's order, so one
// fault the simulation cannot make leaves every fault after it unmade: a
// re-make in another order would not be the run's (a crash before the
// partition it came after), and a Script cannot know the simulation's
// configuration. A qtest exploration with the script as its chooser
// fails on the same faults; a test running the script itself asks Unmade.
func Unmade(sc *qenv.Script) error {
	rem := sc.Remaining()
	if len(rem) == 0 {
		return nil
	}
	// Remaining keeps the script's order: its faults' indices
	undone := map[int]bool{}
	k := 0
	for i, f := range sc.Faults {
		if k < len(rem) && reflect.DeepEqual(f, rem[k]) {
			undone[i] = true
			k++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "qnemesis: the simulation made %d of the script's %d faults", len(sc.Faults)-len(rem), len(sc.Faults))
	misnamed := map[int]bool{}
	for _, m := range sc.Misnamed() {
		var i int
		if _, err := fmt.Sscanf(m, "script fault %d ", &i); err == nil && i >= 0 && i < len(sc.Faults) {
			misnamed[i] = true // said: its cause is named
			if fix := configures(sc.Faults[i].Kind); fix != "" && strings.Contains(m, "no fault kind or nemesis the system declared") {
				// a nemesis qenv makes only when configured: Misnamed's
				// nearest name would mislead
				m = fmt.Sprintf("script fault %d (%v): the simulation has no such nemesis: qenv makes it only with %s", i, sc.Faults[i], fix)
			}
		}
		b.WriteString("\n  " + m)
	}
	for i, f := range sc.Faults {
		if !undone[i] || misnamed[i] {
			continue
		}
		waits := -1
		for _, j := range f.Follows {
			if undone[j] {
				waits = j
			}
		}
		if waits >= 0 {
			fmt.Fprintf(&b, "\n  fault %d (%v) waits for fault %d (%s), which never happened", i, f, waits, sc.Faults[waits].Kind)
		} else {
			fmt.Fprintf(&b, "\n  fault %d (%v) never happened: the run ended before it, or never reached a choice of its kind after %d steps", i, f, f.After)
		}
	}
	return errors.New(b.String())
}

// configures says what makes qenv declare a nemesis of kind: its
// family's configuration, "" for a kind of no nemesis family.
func configures(kind string) string {
	fam, names, ok := parse(kind)
	if !ok {
		return ""
	}
	name := strings.Join(names, ".")
	switch fam {
	case "crash":
		return fmt.Sprintf("a crashable process %s (Sim.Process(%q, true, ...))", name, name)
	case "pause":
		return fmt.Sprintf("Sim.Stalls.Pause set before the processes are made, and a process %s", name)
	case "slow":
		return fmt.Sprintf("Sim.Stalls.Slow and SlowFor set before the processes are made, and a process %s", name)
	case "partition", "heal":
		return fmt.Sprintf("Sim.Net().PartitionNemesis over the processes %s", strings.Join(names, " and "))
	case "jump":
		return "Sim.Clocks with a Jump, set before the processes are made"
	case "upgrade":
		return "Sim.Upgrades"
	}
	return ""
}

// LongestCut is the longest a link stayed cut in the journal (from its
// partition's start to its heal; to the journal's last entry, if never
// healed): what a simulation's Network.CutLength must be at least, in
// its own time, for Script's partitions to last until their heals.
func (j *Journal) LongestCut() time.Duration {
	since := map[[2]string]time.Time{}
	var longest time.Duration
	var last time.Time
	for _, e := range j.Entries {
		last = e.Time
		fam, names, _ := parse(e.Kind)
		pairs := e.Links
		if len(names) == 2 {
			pairs = [][2]string{sorted(names[0], names[1])}
		}
		for _, p := range pairs {
			switch fam {
			case "partition", "isolate":
				if _, ok := since[p]; !ok {
					since[p] = e.Time
				}
			case "heal":
				if t, ok := since[p]; ok {
					longest = max(longest, e.Time.Sub(t))
					delete(since, p)
				}
			}
		}
	}
	for _, t := range since {
		longest = max(longest, last.Sub(t))
	}
	return longest
}

// Unreproducible says what Script leaves out or cannot make as it
// happened, each with the fix: latency and loss (no qenv nemesis), a
// heal of links the journal shows were not cut (qenv's never happens),
// pauses of another length than a simulated pause's (Sim.Stalls.Pause),
// and cuts longer than the simulation's CutLength. Given the simulation
// (before its run), it compares the pauses and cuts with its Stalls.Pause
// and CutLength, within PauseTolerance; without one, with qenv's defaults
// (no pause: every pause is said, with its length).
func (j *Journal) Unreproducible(sim ...*qenv.Sim) []string {
	var out []string
	var stall, cutLength time.Duration
	cutLength = qenv.DefaultPartitionLength
	if len(sim) > 0 && sim[0] != nil {
		stall = sim[0].Stalls.Pause
		if c := sim[0].Net().CutLength; c > 0 {
			cutLength = c
		}
	}
	tolerance := func(d time.Duration) time.Duration { return max(PauseTolerance, d/10) }
	cut := map[[2]string]bool{}
	paused := map[string]time.Time{}
	var pauses []time.Duration
	for _, e := range j.Entries {
		fam, names, _ := parse(e.Kind)
		pairs := e.Links
		if len(names) == 2 {
			pairs = [][2]string{sorted(names[0], names[1])}
		}
		switch fam {
		case "latency", "loss":
			out = append(out, fmt.Sprintf("%s (fault %d): qenv has no %s nemesis; a Service's own faults (rpc.<name>.delay, its lost responses) are the nearest", e.Kind, e.Index, fam))
		case "partition", "isolate":
			for _, p := range pairs {
				cut[p] = true
			}
		case "heal":
			var none []string
			for _, p := range pairs {
				if !cut[p] {
					none = append(none, p[0]+"."+p[1])
				}
				delete(cut, p)
			}
			if len(none) > 0 {
				out = append(out, fmt.Sprintf("%s (fault %d) healed %s, which no fault of the journal cut: left out of the script (qenv heals only a cut link)", e.Kind, e.Index, strings.Join(none, ", ")))
			}
		case "pause":
			paused[names[0]] = e.Time
		case "resume":
			if t, ok := paused[names[0]]; ok && e.Event == "end" {
				d := e.Time.Sub(t)
				pauses = append(pauses, d)
				if stall == 0 || d-stall > tolerance(stall) || stall-d > tolerance(stall) {
					why := "the simulation's pauses are off (Sim.Stalls.Pause 0)"
					if stall > 0 {
						why = fmt.Sprintf("a simulated pause lasts Sim.Stalls.Pause, %v", stall)
					}
					out = append(out, fmt.Sprintf("%s (fault %d) lasted %v: %s; set it to about %v, before the processes are made",
						strings.Replace(e.Kind, "resume", "pause", 1), e.Index, d.Round(time.Millisecond), why, d.Round(time.Millisecond)))
				}
				delete(paused, names[0])
			}
		}
	}
	if d := j.LongestCut(); d > cutLength+PauseTolerance {
		out = append(out, fmt.Sprintf("a link stayed cut %v, longer than the simulation's CutLength (%v): set Sim.Net().CutLength to at least %v (LongestCut), or the simulation heals it first and its heal stays in Remaining",
			d, cutLength, d))
	}
	return out
}

// PauseTolerance is how far a real pause's or cut's length may be from
// the simulation's (or a tenth of it, if more) for Unreproducible to take
// them as the same: the nemesis's calls and the scheduler add a few
// milliseconds to a real one.
const PauseTolerance = 20 * time.Millisecond

// Dropped are the journal's faults Script leaves out, having no qenv
// nemesis: latency and loss.
func (j *Journal) Dropped() []Entry {
	var out []Entry
	for _, e := range j.Entries {
		if fam, _, _ := parse(e.Kind); fam == "latency" || fam == "loss" {
			out = append(out, e)
		}
	}
	return out
}
