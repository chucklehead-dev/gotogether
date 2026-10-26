package qnemesis

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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
// run's faults re-made in a simulation: each in the order it happened
// (each Follows the one before), After the steps the system had recorded
// then plus the faults recorded before it (a simulation that records its
// faults as steps counts them, as qtest.FaultScript does). Crashes,
// pauses, partitions and heals map to qenv's nemeses of the same names (a
// pair sorted, as qenv names it; an isolate is a partition of each of its
// links, a heal of one name a heal of each); a crash's restart and a
// pause's resume are qenv's own (a simulated pause lasts Sim.Stalls.Pause,
// not the real one's length). Latency and loss have no qenv nemesis:
// Dropped lists them.
func (j *Journal) Script() *qenv.Script {
	sc := &qenv.Script{}
	recorded := 0
	add := func(kind string, e Entry) {
		f := qenv.Scripted{Kind: kind}
		if e.Steps != nil {
			f.After = *e.Steps + recorded
		}
		if n := len(sc.Faults); n > 0 {
			f.Follows = []int{n - 1}
		}
		sc.Faults = append(sc.Faults, f)
	}
	for _, e := range j.Entries {
		fam, names, _ := parse(e.Kind)
		switch {
		case (fam == "crash" || fam == "pause") && len(names) == 1:
			add(e.Kind, e)
		case (fam == "partition" || fam == "heal") && len(names) == 2:
			p := sorted(names[0], names[1])
			add("nemesis."+fam+"."+p[0]+"."+p[1], e)
		case fam == "isolate" || fam == "heal":
			to := "partition"
			if fam == "heal" {
				to = "heal"
			}
			for _, p := range e.Links {
				add("nemesis."+to+"."+p[0]+"."+p[1], e)
			}
		}
		if e.Recorded {
			recorded++
		}
	}
	return sc
}

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
