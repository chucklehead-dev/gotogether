package diagnostic

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// ParseRecordRef reads an exact JSON record reference, refusing unknown fields,
// trailing values and incomplete identities. Source is optional display text.
func ParseRecordRef(data string) (RecordRef, error) {
	var ref RecordRef
	d := json.NewDecoder(strings.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&ref); err != nil {
		return ref, fmt.Errorf("record reference: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ref, fmt.Errorf("record reference: expected one JSON object")
	}
	if strings.TrimSpace(ref.Actor) == "" || strings.TrimSpace(ref.Recorder) == "" || ref.Seq == 0 {
		return ref, fmt.Errorf("record reference: supply actor, recorder and a nonzero sequence")
	}
	return ref, nil
}

// RecordDetail is a captured record, not a model state or a causal judgment.
// Reference identifies the capture; Source and Ref are display provenance only.
type RecordDetail struct {
	Reference    RecordRef       `json:"reference"`
	Action       string          `json:"action"`
	Outcome      string          `json:"outcome,omitempty"`
	Error        string          `json:"error,omitempty"`
	Source       string          `json:"source,omitempty"`
	Process      string          `json:"process,omitempty"`
	Thread       string          `json:"thread,omitempty"`
	Arguments    json.RawMessage `json:"arguments"`
	Observations json.RawMessage `json:"observations"`
	Invoked      string          `json:"invoked,omitempty"`
	Time         string          `json:"time,omitempty"`
	Call         uint64          `json:"call,omitempty"`
	Invocation   bool            `json:"invocation"`
	Ref          string          `json:"ref,omitempty"`
}

// LookupRecord finds one exact identity in a capture. Conflicting records are
// refused; repeated copies differing only in display provenance are one record.
// It never interprets a Source string or searches business argument values.
func LookupRecord(steps []qtrace.Step, ref RecordRef) (*RecordDetail, error) {
	if strings.TrimSpace(ref.Actor) == "" || strings.TrimSpace(ref.Recorder) == "" || ref.Seq == 0 {
		return nil, fmt.Errorf("record lookup: supply actor, recorder and a nonzero sequence")
	}
	var found *qtrace.Step
	for i := range steps {
		s := &steps[i]
		if s.Actor != ref.Actor || s.Recorder != ref.Recorder || s.Seq != ref.Seq {
			continue
		}
		if found != nil && !qtrace.SameRecord(found, s) {
			return nil, fmt.Errorf("record lookup: ambiguous identity %s %s seq %d: capture contains conflicting records", ref.Actor, ref.Recorder, ref.Seq)
		}
		if found == nil {
			found = s
		}
	}
	if found == nil {
		return nil, fmt.Errorf("record lookup: %s %s seq %d is missing from this capture; open its native capture or inspect the projected record", ref.Actor, ref.Recorder, ref.Seq)
	}
	args, err := json.Marshal(found.Args)
	if err != nil {
		return nil, err
	}
	obs, err := json.Marshal(found.Obs)
	if err != nil {
		return nil, err
	}
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format(time.RFC3339Nano)
	}
	return &RecordDetail{Reference: RecordRef{Actor: found.Actor, Recorder: found.Recorder, Seq: found.Seq, Source: found.Source}, Action: found.Action, Outcome: found.Outcome, Error: found.Error, Source: found.Source, Process: found.Process, Thread: found.Thread, Arguments: args, Observations: obs, Time: stamp(found.Time), Invoked: stamp(found.Invoked), Call: found.Call, Invocation: found.Invocation, Ref: found.Ref}, nil
}
