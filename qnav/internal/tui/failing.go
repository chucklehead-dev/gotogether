package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
)

// Failing is the navigator at a recorded run's failing step: the state
// after the longest accepted prefix, and why the next call or recorded
// checkpoint disagrees (the navigator's own explanation without CLI advice).
func Failing(ch *validate.Checker, start validate.NavStart) (ModelNav, string, string, error) {
	start.At = -1
	whole, rejected := ch.Nav(start)
	if rejected == nil {
		// Preserve the existing no-failure path when this model did not opt in.
		if len(ch.Model.Diagnostics) == 0 {
			return nil, "", "", errors.New("the model accepts every call of the run: no failing step")
		}
		report, err := ch.Check(context.Background(), start.Steps)
		if err != nil {
			return nil, "", "", err
		}
		for _, actor := range report.Actors {
			if start.Actor != "" && start.Actor != actor.Actor {
				continue
			}
			var details []diagnostic.Result
			for _, ab := range actor.Absent {
				if ab.Cause == validate.Defect && ab.Diagnostic != nil {
					details = append(details, *ab.Diagnostic)
				}
			}
			if len(details) > 0 {
				return terminalDiagnosticNav{whole, details}, "terminal state · failed completion property", "The recorded history conforms; " + actor.Reason() + ".", nil
			}
		}
		return nil, "", "", errors.New("the model accepts every call of the run: no failing step")
	}
	var last *validate.Navigator
	for k := 0; ; k++ {
		start.At = k
		n, err := ch.Nav(start)
		if err != nil {
			if last == nil {
				return nil, "", "", err
			}
			nav, checkpoint, err := rejectedHistory(ch, start, last, k-1)
			if err != nil {
				return nil, "", "", err
			}
			if checkpoint {
				return nav, fmt.Sprintf("the state before call %d, whose checkpoint disagrees with replayed state", k), tidyWhy(rejected.Error()), nil
			}
			return nav, fmt.Sprintf("the state before call %d, which the model rejects", k), tidyWhy(rejected.Error()), nil
		}
		last = n
	}
}

// rejectedHistory attaches only a full check's exact captured identity. The
// model cursor's index includes inferred actions and cannot name a capture.
func rejectedHistory(ch *validate.Checker, start validate.NavStart, nav ModelNav, at int) (ModelNav, bool, error) {
	report, err := ch.Check(context.Background(), start.Steps)
	if err != nil {
		return nil, false, err
	}
	// A checkpoint compares an accepted prefix with a recorded snapshot. It is
	// not a disabled call, and has no rejected-call evidence overlay.
	if start.Actor != "" || len(report.Actors) == 1 {
		for _, actor := range report.Actors {
			if start.Actor != "" && actor.Actor != start.Actor {
				continue
			}
			if f := actor.Failure; actor.Conforms == validate.Fail && f != nil && f.Kind == "state" && f.Index == at && ch.Binding.Snapshot != "" && f.Step != nil && f.Step.Action == ch.Binding.Snapshot && !f.Step.Ambiguous {
				return checkpointHistoryNav{ModelNav: nav}, true, nil
			}
		}
	}
	ref, err := rejectedRecordOf(report, start.Actor, at)
	if err != nil {
		return nil, false, err
	}
	if ref == nil {
		return nav, false, nil
	}
	if _, err := diagnostic.LookupRecord(start.Steps, *ref); err != nil {
		return nil, false, fmt.Errorf("rejected captured record: %w", err)
	}
	return rejectedHistoryNav{ModelNav: nav, ref: *ref}, false, nil
}

func rejectedRecordOf(report *validate.Report, selected string, at int) (*diagnostic.RecordRef, error) {
	if selected == "" && len(report.Actors) != 1 {
		return nil, fmt.Errorf("select the actor whose rejected capture to inspect")
	}
	for _, actor := range report.Actors {
		if selected != "" && actor.Actor != selected {
			continue
		}
		failure := actor.Failure
		if actor.Conforms != validate.Fail || failure == nil || failure.Kind != "disabled" || failure.Index != at {
			continue
		}
		if failure.Step == nil || failure.Step.Actor == "" || failure.Step.Recorder == "" || failure.Step.Seq == 0 {
			return nil, nil // an inferred step has no exact captured identity
		}
		step := failure.Step
		ref := diagnostic.RecordRef{Actor: step.Actor, Recorder: step.Recorder, Seq: step.Seq, Source: step.Source}
		return &ref, nil
	}
	return nil, fmt.Errorf("the navigator stopped at call %d, but validation did not establish a model rejection at that boundary", at+1)
}

// checkpointHistoryNav preserves the checked failure kind independently of
// presentation text. It supplies no rejected captured-record evidence.
type checkpointHistoryNav struct{ ModelNav }

func (n checkpointHistoryNav) checkpointMismatch() bool { return true }

func (n checkpointHistoryNav) Record(ref diagnostic.RecordRef) (*diagnostic.RecordDetail, error) {
	if nav, ok := n.ModelNav.(recordNav); ok {
		return nav.Record(ref)
	}
	return nil, fmt.Errorf("this navigator cannot retrieve captured records")
}

// This reference belongs to the fixed captured history even when the user
// follows a hypothetical model branch. It adds no verdict to cursor nodes.
type rejectedHistoryNav struct {
	ModelNav
	ref diagnostic.RecordRef
}

func (n rejectedHistoryNav) rejectedRecord() diagnostic.RecordRef { return n.ref }

func (n rejectedHistoryNav) Record(ref diagnostic.RecordRef) (*diagnostic.RecordDetail, error) {
	if nav, ok := n.ModelNav.(recordNav); ok {
		return nav.Record(ref)
	}
	return nil, fmt.Errorf("this navigator cannot retrieve captured records")
}

// tidyWhy is the navigator's error for a call the model rejects, as the
// navigator shows it: the call and why, without the advice the command
// line gives (which call to open the run at: the navigator is there).
func tidyWhy(s string) string {
	s = strings.TrimPrefix(s, "nav: ")
	if i := strings.Index(s, " is not a transition"); i >= 0 {
		if j := strings.Index(s, "\n"); j > i {
			s = s[:i] + s[j:]
		}
	}
	return s
}

// A terminal diagnostic stays labeled as fixed-history evidence when the user
// explores other model branches from the failure boundary.
type terminalDiagnosticNav struct {
	ModelNav
	details []diagnostic.Result
}

func (n terminalDiagnosticNav) Node(path ...string) (*validate.Node, error) {
	node, err := n.ModelNav.Node(path...)
	return n.attach(node, err)
}
func (n terminalDiagnosticNav) Fold(path ...string) (*validate.Node, error) {
	node, err := n.ModelNav.Fold(path...)
	return n.attach(node, err)
}
func (n terminalDiagnosticNav) attach(node *validate.Node, err error) (*validate.Node, error) {
	if err != nil {
		return nil, err
	}
	copy := *node
	copy.Diagnostics = append(slices.Clone(node.Diagnostics), n.details...)
	return &copy, nil
}

// Record preserves captured-record inspection through the terminal overlay.
func (n terminalDiagnosticNav) Record(ref diagnostic.RecordRef) (*diagnostic.RecordDetail, error) {
	if nav, ok := n.ModelNav.(recordNav); ok {
		return nav.Record(ref)
	}
	return nil, fmt.Errorf("this navigator cannot retrieve captured records")
}
