package qnemesis

import (
	"os"
	"slices"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// lossRecords are the loss records of a killed incarnation's recorders
// (a Recorded target's): each recorder's tail after its last step in the
// incarnation's logs, cut at that step's time, or, for one with no step
// there, at the incarnation's start. A recorder found in the logs but
// not named is declared too: the logs are the incarnation's. actor is
// the actor of a recorder whose logs have no step (the nemesis's own:
// the binding's history), so its loss reaches the run it belongs to.
func lossRecords(t Target, inc int, actor string) []qtrace.Step {
	r, ok := t.(Recorded)
	if !ok {
		return nil
	}
	recorders, logs, started := r.RecordedBy(inc)
	last := map[string]qtrace.Step{}
	for _, path := range logs {
		f, err := os.Open(path)
		if err != nil {
			continue // never written: its recorders' cut is the start
		}
		steps, _ := qtrace.ReadJSONL(f) // a torn tail: the steps before it, and its own loss records
		f.Close()
		for _, s := range steps {
			if s.Lost != "" {
				continue
			}
			if l, ok := last[s.Recorder]; !ok || s.Seq > l.Seq || s.Seq == l.Seq && s.Time.After(l.Time) {
				last[s.Recorder] = s
			}
			if !slices.Contains(recorders, s.Recorder) {
				recorders = append(recorders, s.Recorder)
			}
		}
	}
	var out []qtrace.Step
	for _, id := range recorders {
		rec := qtrace.Step{Action: "lost", Recorder: id, Process: id, Actor: actor, Lost: "suffix", Time: started}
		if l, ok := last[id]; ok {
			rec.LostAfter, rec.Time, rec.Actor, rec.Process = l.Seq, l.Time, l.Actor, l.Process
		}
		if rec.Time.IsZero() {
			rec.Time = time.Unix(0, 0) // a start not known: everything may depend on the loss
		}
		out = append(out, rec)
	}
	return out
}
