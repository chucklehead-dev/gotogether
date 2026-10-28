package validate

import (
	"fmt"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A report names a record one way (I20): on-call greps one index, not
// three. The record's id is where it was read, when that is known (a
// step log's file:line, "line N" of one read alone, a span), else its
// recorder's and seq ("payments-1#11"), which every reader keeps. The
// step's place in the replay and its recorder's seq are the model
// author's: a verbose report adds them.

// RecordID is how a report names the record of step s: where it was
// read (qtrace.Step.Source: "day.jsonl:12", "line 12", "span 5b8a…"),
// else "<recorder>#<seq>".
func RecordID(s *qtrace.Step) string {
	if s == nil {
		return ""
	}
	if s.Source != "" {
		if span, _ := sourceIDs(s.Source); span != "" {
			return "span " + span
		}
		return s.Source
	}
	return fmt.Sprintf("%s#%d", s.Recorder, s.Seq)
}

// sourceIDs are the span and trace ids of a step converted from a span
// (otelio's and convert's Source: "span <id>", "span <id> trace <id>").
func sourceIDs(src string) (span, trace string) {
	rest, ok := strings.CutPrefix(src, "span ")
	if !ok {
		return "", ""
	}
	span, rest, _ = strings.Cut(rest, " ")
	trace, _ = strings.CutPrefix(rest, "trace ")
	return span, trace
}

// recordWhere is the record's line of a failure: its id, its wall time,
// who recorded it (the process, and the recorder when it names more:
// grist's <app>/<node>/<incarnation>, the pod), and its trace when it
// came from one; verbose adds the step's place in the replay (index,
// 0-based; -1 for none) and its recorder's seq.
func recordWhere(s *qtrace.Step, index int, verbose bool) string {
	parts := []string{RecordID(s)}
	if !s.Time.IsZero() {
		parts = append(parts, s.Time.UTC().Format(time.RFC3339Nano))
	}
	switch {
	case s.Process != "" && (s.Recorder == "" || s.Recorder == s.Process):
		parts = append(parts, "process "+s.Process)
	case s.Process != "":
		parts = append(parts, "process "+s.Process+", recorder "+s.Recorder+identityNote(s.Recorder))
	case s.Recorder != "":
		parts = append(parts, "recorder "+s.Recorder+identityNote(s.Recorder))
	}
	if _, trace := sourceIDs(s.Source); trace != "" { // its span is its id
		parts = append(parts, "trace "+trace)
	}
	if verbose {
		v := fmt.Sprintf("seq %d of recorder %s", s.Seq, s.Recorder)
		if index >= 0 {
			v = fmt.Sprintf("step %d of the replay, %s", index+1, v)
		}
		if s.Thread != "" && s.Thread != s.Recorder {
			v += ", thread " + s.Thread
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, ", ")
}

// identityNote reads a recorder id of grist's node identity,
// <app>/<node>/<incarnation> (grist.Identity), as its pod and start; a
// relay's (grist/<store>/<tenant>) and any other id say nothing more.
func identityNote(rec string) string {
	ps := strings.Split(rec, "/")
	if len(ps) != 3 || ps[0] == "grist" || ps[0] == "" || ps[1] == "" || ps[2] == "" {
		return ""
	}
	return fmt.Sprintf(" (node %s, incarnation %s)", ps[1], ps[2])
}
