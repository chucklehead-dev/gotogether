// Package qtrace is the model-agnostic core of gotogether: the telemetry schema
// every instrumented program emits, the Step it decodes to, and the
// reconstruction of an ordered, per-actor trace from an unordered bag of
// steps collected from live telemetry.
//
// Nothing here knows about any particular Quint model. The mapping from
// steps to model actions lives in a binding (package binding).
package qtrace

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The telemetry schema. Every model step is one span event named EventName.
// All attributes live on the event; readers fall back to the enclosing span's
// and resource's attributes for the non-arg keys, so a deployment can hoist
// e.g. quint.process to the resource.
const (
	EventName = "quint.step" // span event name
	SpanName  = "quint "     // prefix of the span a recorder opens per step: "quint <action>"

	KeyAction   = "quint.action"   // model action name (string, required)
	KeySeq      = "quint.seq"      // per-recorder dense sequence number, 1,2,3,... (int)
	KeyRecorder = "quint.recorder" // id of the recorder that assigned quint.seq (string)
	KeyActor    = "quint.actor"    // partition: which model instance the step belongs to (string)
	KeyProcess  = "quint.process"  // incarnation of the actor (string); a change means a restart
	KeyThread   = "quint.thread"   // program-order key: steps with the same thread are totally ordered by seq
	KeyOutcome  = "quint.outcome"  // "ok", "error", or an error's own classification
	KeyError    = "quint.error"    // error text, when the step failed

	// KeyInvoked is when the operation the step completes was invoked (Unix
	// nanoseconds): with the step's time, its real-time interval. A step
	// whose operation completed before another's was invoked precedes it.
	KeyInvoked = "quint.invoked_unix_nano"
	// KeyInvocation marks an invocation record (qobs.Invoke): the operation
	// began, and its completion, if any, names this record's seq as
	// KeyCall. An invocation never completed is in Trace.Pending.
	KeyInvocation = "quint.invocation"
	KeyCall       = "quint.call" // on a completion: its invocation record's seq (same recorder)
	// KeySettled marks a settle record (qobs.Settled): the system was quiet
	// then. A trace whose recorder's last record is one is Trace.Settled,
	// and only such a trace's eventual properties are judged.
	KeySettled = "quint.settled"
	// KeyLost marks a loss record (MarkLost, or ReadJSONL for a torn
	// tail): its recorder lost steps its log cannot show, "suffix" (every
	// step after KeyLostAfter's seq) or "prefix" (every step up to it). A
	// record, not a step: Reconstruct keeps it as Trace.Lost.
	KeyLost      = "quint.lost"
	KeyLostAfter = "quint.lost_after" // on a loss record: the seq the loss is after (a suffix) or up to (a prefix)
	// KeyLostBefore bounds a lost suffix in time (Unix nanoseconds): every
	// lost step was recorded before it, as a process reaped by then
	// recorded nothing later (qnemesis's crash). Absent: unbounded.
	KeyLostBefore = "quint.lost_before_unix_nano"
	// KeyCompleteThrough marks a watermark record (MarkComplete,
	// qobs.Recorder.CompleteThrough, validate -complete-through): its
	// recorder's steps recorded at or before this time (Unix nanoseconds)
	// are all in the input. Recorder "*" states it of every recorder. A
	// record, not a step: Reconstruct keeps it as Trace.Collected.
	KeyCompleteThrough = "quint.complete_through_unix_nano"
	// KeyAssumed marks an assumed step (Step.Ambiguous): a pending call's
	// effect a resolution placed, with no expect checked after it.
	KeyAssumed = "quint.assumed"
	// KeyRef is a step's link to the artifact it came from (Step.Ref): an
	// external driver's trace line, a screenshot. Free text for people
	// reading a failure; no check reads it.
	KeyRef = "quint.ref"

	PrefixArg   = "quint.arg."   // an observed parameter of the action
	PrefixObs   = "quint.obs."   // an observed projection of the post-state
	PrefixOrder = "quint.order." // a domain logical clock: steps carrying the same domain are ordered by its value
)

// Step is one observed model step, decoded from a quint.step event (or
// recorded directly in memory by qobs.MemorySink).
type Step struct {
	Action   string
	Seq      uint64
	Recorder string
	Actor    string
	Process  string
	Thread   string
	Outcome  string
	Error    string
	Time     time.Time
	// Invoked is when the operation was invoked, if known (KeyInvoked): the
	// step took effect at some point between Invoked and Time. Zero:
	// unknown, some time before Time (no real-time bound).
	Invoked time.Time
	// Invocation marks an invocation record (KeyInvocation), not a step:
	// Reconstruct pairs it with its completion, whose Call is its Seq, and
	// keeps it in Trace.Pending if there is none.
	Invocation bool
	Call       uint64
	// Settled marks a settle record (KeySettled), not a step.
	Settled bool
	// Lost marks a loss record (KeyLost), not a step: "suffix" (its
	// recorder lost every step after seq LostAfter) or "prefix" (every
	// step up to it); "" for any other.
	Lost      string
	LostAfter uint64
	// LostBefore, if set, bounds a lost suffix: its steps were all
	// recorded before it (KeyLostBefore).
	LostBefore time.Time
	// CompleteThrough, if set, marks a watermark record
	// (KeyCompleteThrough), not a step: its recorder's steps through this
	// time are all in the input.
	CompleteThrough time.Time
	// Ambiguous marks a step whose effect is assumed, not observed: a
	// pending call resolved as having taken effect (qtest.ResolvePending).
	// It has no outcome and no observations, and the binding checks no
	// expect after it.
	Ambiguous bool
	// Args are scalars: string, int64, bool, float64, or json.Number
	// for an integer past int64 (a step log's, a request log's), kept
	// whole. A collection is its JSON text, as qobs records one.
	Args  map[string]any
	Obs   map[string]any
	Order map[string]int64
	// fromRecord: Invoked is the invocation record's time (Reconstruct),
	// the recorder's own clock, which no other machine's compares with
	fromRecord bool
	// slack: the clocks' skew a search ordered the step with
	// (LinearizeOptions.Skew), which the check of its order keeps
	slack time.Duration
	// carried: a watermark record a search's order carries
	// (CarryWatermark), taken as it is; carriedWhole: some recorder of
	// the actor stated none
	carried, carriedWhole bool
	Source                string // where it came from, for diagnostics (span id, "line N" of a step log read alone, file:N read by package input, ...)
	// Ref is the step's link to its artifact (KeyRef, qobs.Ref): a
	// driver's trace line, a screenshot ("bombadil/trace.jsonl:42
	// screenshot=screenshots/42.webp"). Unlike Source, which says where
	// gotogether read the step, it is written with the step and kept by
	// every reader. It is no model data: no verdict, and no verdict cache
	// key (Keyer), depends on it.
	Ref string
}

// Model reports whether s is a model step: not an invocation record, a
// settle record, a loss record or a watermark record, which Reconstruct
// takes out of the steps. A sink that counts or replays steps skips the
// others.
func (s *Step) Model() bool {
	return !s.Invocation && !s.Settled && s.Lost == "" && s.CompleteThrough.IsZero()
}

// Get returns an argument or, with the "obs." prefix, an observation.
func (s *Step) Get(name string) (any, bool) {
	if strings.HasPrefix(name, "obs.") {
		v, ok := s.Obs[strings.TrimPrefix(name, "obs.")]
		return v, ok
	}
	v, ok := s.Args[name]
	return v, ok
}

func (s *Step) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", s.Action)
	keys := slices.Sorted(maps.Keys(s.Args))
	b.WriteString("(")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%v", k, s.Args[k])
	}
	b.WriteString(")")
	if s.Outcome != "" && s.Outcome != "ok" {
		fmt.Fprintf(&b, " -> %s", s.Outcome)
	}
	return b.String()
}

// FromAttributes decodes a step from flattened attributes: the event's own,
// then (as fallbacks for the non-arg keys) the span's and the resource's.
func FromAttributes(t time.Time, layers ...map[string]any) (Step, error) {
	s := Step{Time: t, Args: map[string]any{}, Obs: map[string]any{}, Order: map[string]int64{}}
	str := func(k string) string {
		for _, l := range layers {
			if v, ok := l[k]; ok {
				return fmt.Sprint(v)
			}
		}
		return ""
	}
	s.Action = str(KeyAction)
	if s.Action == "" {
		return s, fmt.Errorf("%s: missing %s", EventName, KeyAction)
	}
	s.Recorder, s.Actor, s.Process, s.Thread = str(KeyRecorder), str(KeyActor), str(KeyProcess), str(KeyThread)
	s.Outcome, s.Error = str(KeyOutcome), str(KeyError)
	// a ref is one step's: the event's, else its span's, never the
	// resource's, which would link every step to one artifact
	for _, l := range layers[:min(len(layers), 2)] {
		if v, ok := l[KeyRef]; ok {
			s.Ref = fmt.Sprint(v)
			break
		}
	}
	if seq := str(KeySeq); seq != "" {
		n, err := strconv.ParseUint(seq, 10, 64)
		if err != nil {
			return s, fmt.Errorf("%s: bad %s %q", EventName, KeySeq, seq)
		}
		s.Seq = n
	}
	// args, obs, order keys and the invocation only from the event itself (layer 0)
	if len(layers) > 0 {
		if v, ok := layers[0][KeyInvocation]; ok {
			s.Invocation = v == true || fmt.Sprint(v) == "true"
		}
		if v, ok := layers[0][KeySettled]; ok {
			s.Settled = v == true || fmt.Sprint(v) == "true"
		}
		if v, ok := layers[0][KeyLost]; ok {
			s.Lost = fmt.Sprint(v)
			if s.Lost != "suffix" && s.Lost != "prefix" {
				return s, fmt.Errorf("%s: %s: %q is no loss (\"suffix\" or \"prefix\")", EventName, KeyLost, s.Lost)
			}
			if v, ok := layers[0][KeyLostAfter]; ok {
				n, err := toInt(v)
				if err != nil || n < 0 {
					return s, fmt.Errorf("%s: %s: not a seq: %v", EventName, KeyLostAfter, v)
				}
				s.LostAfter = uint64(n)
			}
			if v, ok := layers[0][KeyLostBefore]; ok {
				n, err := toInt(v)
				if err != nil {
					return s, fmt.Errorf("%s: %s: %w", EventName, KeyLostBefore, err)
				}
				s.LostBefore = time.Unix(0, n)
			}
		}
		if v, ok := layers[0][KeyCompleteThrough]; ok {
			n, err := toInt(v)
			if err != nil || n <= 0 {
				return s, fmt.Errorf("%s: %s: not a time in Unix nanoseconds: %v", EventName, KeyCompleteThrough, v)
			}
			s.CompleteThrough = time.Unix(0, n)
		}
		if v, ok := layers[0][KeyAssumed]; ok {
			s.Ambiguous = v == true || fmt.Sprint(v) == "true"
		}
		if v, ok := layers[0][KeyCall]; ok {
			n, err := toInt(v)
			if err != nil || n <= 0 {
				return s, fmt.Errorf("%s: %s: not a seq: %v", EventName, KeyCall, v)
			}
			s.Call = uint64(n)
		}
		if v, ok := layers[0][KeyInvoked]; ok {
			n, err := toInt(v)
			if err != nil {
				return s, fmt.Errorf("%s: %s: %w", EventName, KeyInvoked, err)
			}
			s.Invoked = time.Unix(0, n)
		}
		for k, v := range layers[0] {
			switch {
			case strings.HasPrefix(k, PrefixArg):
				s.Args[strings.TrimPrefix(k, PrefixArg)] = v
			case strings.HasPrefix(k, PrefixObs):
				s.Obs[strings.TrimPrefix(k, PrefixObs)] = v
			case strings.HasPrefix(k, PrefixOrder):
				n, err := toInt(v)
				if err != nil {
					return s, fmt.Errorf("%s: %s: %w", EventName, k, err)
				}
				s.Order[strings.TrimPrefix(k, PrefixOrder)] = n
			}
		}
	}
	if s.Actor == "" {
		s.Actor = "default"
	}
	if s.Process == "" {
		s.Process = s.Recorder
	}
	return s, nil
}

func toInt(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case float64:
		return int64(x), nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	}
	return 0, fmt.Errorf("not an integer: %v", v)
}
