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
// run's faults re-made in a simulation: each in the order it happened,
// After the steps the system had recorded then plus the faults recorded
// before it (a simulation that records its faults as steps counts them,
// as qtest.FaultScript does). Crashes, pauses, partitions and heals map
// to qenv's nemeses of the same names (a pair sorted, as qenv names it;
// an isolate is a partition of each of its links, a heal of one name a
// heal of each link the journal shows cut); a crash's restart and a
// pause's resume are qenv's own.
//
// Each fault Follows the last fault before it that is no heal; a heal
// follows the last fault, and nothing waits for it. qenv heals a
// partition by itself after Network.CutLength (1 virtual second by
// default): a heal of a cut already healed so never happens, and stays
// in Remaining, where a chain through it would have stopped every fault
// after it. Set CutLength to LongestCut, or longer, to keep each
// partition until its heal. What the script cannot make as it happened
// is in Unreproducible.
func (j *Journal) Script() *qenv.Script {
	sc := &qenv.Script{}
	recorded := 0
	cut := map[[2]string]bool{}
	lastFault := -1 // the last scripted fault that is no heal
	add := func(kind string, e Entry, heal bool) {
		f := qenv.Scripted{Kind: kind}
		if e.Steps != nil {
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
// a cut longer than qenv's default CutLength, and pauses (a simulated
// pause lasts Sim.Stalls.Pause).
func (j *Journal) Unreproducible() []string {
	var out []string
	cut := map[[2]string]bool{}
	paused := map[string]time.Time{}
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
				out = append(out, fmt.Sprintf("%s (fault %d) lasted %v: a simulated pause lasts Sim.Stalls.Pause; set it to that", strings.Replace(e.Kind, "resume", "pause", 1), e.Index, e.Time.Sub(t)))
				delete(paused, names[0])
			}
		}
	}
	if d := j.LongestCut(); d > qenv.DefaultPartitionLength {
		out = append(out, fmt.Sprintf("a link stayed cut %v, longer than qenv's default CutLength (%v): set Sim.Net().CutLength to at least %v (LongestCut), or the simulation heals it first and its heal stays in Remaining",
			d, qenv.DefaultPartitionLength, d))
	}
	return out
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
