package qtrace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/formats"
)

// KeyTime is the step log's timestamp field (Unix nanoseconds). It is the
// only key of the native step log that is not an OTel attribute key.
const KeyTime = "quint.time_unix_nano"

// StepsFormat is the step log's format at the version this build writes
// (KeyFormat): a reader refuses a later major with the fix and reads
// every earlier one. A log without a format record is
// gotogether.steps/1, as every log written before it had a name.
const StepsFormat = "gotogether.steps/1"

// FormatLine is a step log's format record (with the newline): the
// first line WriteJSONL and qobs.JSONLSink write. A log may hold several
// (logs concatenated), each checked; none is a step.
func FormatLine() []byte { return []byte(`{"` + KeyFormat + `":"` + StepsFormat + `"}` + "\n") }

// Attributes flattens a step into the schema's attribute keys: exactly the
// attributes an OTel span event carries, plus KeyTime. The native step log
// is one such object per line.
func (s *Step) Attributes() map[string]any {
	m := map[string]any{KeyAction: s.Action, KeySeq: int64(s.Seq), KeyRecorder: s.Recorder, KeyOutcome: s.Outcome}
	if !s.Time.IsZero() { // a zero Time's UnixNano overflows: no time is no key, read back as zero
		m[KeyTime] = s.Time.UnixNano()
	}
	for _, kv := range [...]struct{ k, v string }{{KeyActor, s.Actor}, {KeyProcess, s.Process}, {KeyThread, s.Thread}, {KeyError, s.Error}, {KeyRef, s.Ref}} {
		if kv.v != "" {
			m[kv.k] = kv.v
		}
	}
	if !s.Invoked.IsZero() {
		m[KeyInvoked] = s.Invoked.UnixNano()
	}
	if s.Invocation {
		m[KeyInvocation] = true
	}
	if s.Call != 0 {
		m[KeyCall] = int64(s.Call)
	}
	if s.Settled {
		m[KeySettled] = true
	}
	if s.Lost != "" {
		m[KeyLost], m[KeyLostAfter] = s.Lost, int64(s.LostAfter)
		if !s.LostBefore.IsZero() {
			m[KeyLostBefore] = s.LostBefore.UnixNano()
		}
	}
	if !s.CompleteThrough.IsZero() {
		m[KeyCompleteThrough] = s.CompleteThrough.UnixNano()
	}
	if s.Ambiguous {
		m[KeyAssumed] = true
	}
	for k, v := range s.Args {
		m[PrefixArg+k] = v
	}
	for k, v := range s.Obs {
		m[PrefixObs+k] = v
	}
	for k, v := range s.Order {
		m[PrefixOrder+k] = v
	}
	return m
}

// MarshalLine encodes a step as one step-log line (with the newline):
// json.Marshal(s.Attributes()), byte for byte (AppendLine).
func (s *Step) MarshalLine() ([]byte, error) {
	b, err := s.AppendLine(make([]byte, 0, 256))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// WriteJSONL writes steps as a native step log, after its format record
// (FormatLine).
func WriteJSONL(w io.Writer, steps []Step) error {
	if _, err := w.Write(FormatLine()); err != nil {
		return err
	}
	var b []byte
	for i := range steps {
		var err error
		if b, err = steps[i].AppendLine(b[:0]); err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	return nil
}

// A TornTail is a step log whose last line was cut off: the writer
// stopped in the middle of it (a machine that lost power before the line
// reached the disk). The steps before it are good; the cut one is lost,
// and with it any step after it that was never written.
type TornTail struct {
	Line int    // the cut line's number
	Text string // what of it was written
}

func (e *TornTail) Error() string {
	return fmt.Sprintf("line %d: the log ends in the middle of a step (%d bytes, no newline): the writer stopped while writing it; the steps before it are kept", e.Line, len(e.Text))
}

// ReadJSONL reads a native step log: one flat JSON object of schema keys
// per line. Blank lines are skipped. A last line that is cut off (no
// newline, not a whole object) does not lose the log: ReadJSONL returns
// the steps before it with a *TornTail error, which a caller may report
// and go on. The steps then end in a loss record (MarkLost) for each
// recorder of the log, after its last seq kept: the cut line was one of
// theirs, which the cut may not name, and the writer stopped for all of
// them (a run whose other logs went on is judged knowing that).
func ReadJSONL(r io.Reader) ([]Step, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var out []Step
	for i, raw := range lines {
		n := i + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		s, err := parseLine(line, n)
		if errors.Is(err, errFormatRecord) {
			continue
		}
		if err != nil {
			// no newline after it: the writer stopped here. A whole object
			// followed by more is no cut-off line, wherever it is.
			if i == len(lines)-1 && !errors.Is(err, errTrailing) && !errors.As(err, new(formatError)) {
				return tornAt(out), &TornTail{Line: n, Text: raw}
			}
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func parseLine(line string, n int) (Step, error) {
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return Step{}, fmt.Errorf("line %d: %w", n, err)
	}
	// A line is one object: a second, or anything else after it, was
	// silently dropped once, losing a record or hiding a corruption.
	if _, err := dec.Token(); err != io.EOF {
		return Step{}, fmt.Errorf("line %d: %w after the step's object at offset %d: write one object per line", n, errTrailing, dec.InputOffset())
	}
	// a format record: its version checked, no step
	if f, ok := raw[KeyFormat]; ok && len(raw) == 1 {
		if _, err := formats.Steps.Check(fmt.Sprint(f)); err != nil {
			return Step{}, fmt.Errorf("line %d: %w", n, formatError{err})
		}
		return Step{}, errFormatRecord
	}
	attrs := map[string]any{}
	for k, v := range raw {
		if num, ok := v.(json.Number); ok {
			if i, err := num.Int64(); err == nil {
				attrs[k] = i
			} else if !strings.ContainsAny(string(num), ".eE") {
				attrs[k] = num // an integer past int64, whole: a float64 would round it
			} else if f, err := num.Float64(); err == nil {
				attrs[k] = f
			}
			continue
		}
		attrs[k] = v
	}
	var ts time.Time
	if ns, ok := attrs[KeyTime].(int64); ok {
		ts = time.Unix(0, ns)
	}
	delete(attrs, KeyTime)
	s, err := FromAttributes(ts, attrs)
	if err != nil {
		return Step{}, fmt.Errorf("line %d: %w", n, err)
	}
	s.Source = fmt.Sprintf("line %d", n)
	return s, nil
}

// errFormatRecord is parseLine's answer for a format record, which is
// no step.
var errFormatRecord = errors.New("format record") // read-only

// formatError is a format record's refusal: a whole line, never a torn
// one.
type formatError struct{ error }

func (e formatError) Unwrap() error { return e.error }

// errTrailing is a line's error when its object is followed by more.
var errTrailing = errors.New("trailing data") // read-only

// tornAt is a torn log's steps with the loss of each of its recorders'
// tails declared: the cut line was one of theirs, which the cut may not
// name, and the process writing the log stopped for all of them.
func tornAt(steps []Step) []Step {
	last := map[string]uint64{}
	var recs []string
	for _, s := range steps {
		if s.Lost != "" || !s.CompleteThrough.IsZero() {
			continue
		}
		if _, ok := last[s.Recorder]; !ok {
			recs = append(recs, s.Recorder)
		}
		last[s.Recorder] = max(last[s.Recorder], s.Seq)
	}
	for _, r := range recs {
		steps = MarkLost(steps, Lost{Recorder: r, After: last[r]})
	}
	return steps
}
