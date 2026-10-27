// Package input reads any of gotogether's trace inputs, detecting the format:
// OTLP/JSON (spans carrying quint.step events), the native step log (JSONL
// of qtrace steps), an external driver's request log (package qdriver's
// protocol), or a model-level ITF trace.
package input

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qdriver"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Format of a trace input.
type Format string

const (
	OTLP    Format = "otlp"  // OTLP/JSON TracesData lines
	StepLog Format = "steps" // native step log (qobs.JSONLSink)
	ITF     Format = "itf"   // model-level ITF (gotogether, or quint run --mbt)
	// Requests is an external driver's request log (qdriver.Protocol):
	// its requests invocations, each completed by its own response
	Requests Format = "requests"
	Unknown  Format = ""
)

// Sniff detects the format from the first JSON value.
func Sniff(data []byte) Format {
	var first map[string]json.RawMessage
	if json.NewDecoder(bytes.NewReader(data)).Decode(&first) != nil {
		return Unknown
	}
	switch {
	case qdriver.Sniff(data):
		return Requests
	case first["resourceSpans"] != nil:
		return OTLP
	case first["states"] != nil:
		return ITF
	case first[qtrace.KeyAction] != nil:
		return StepLog
	}
	return Unknown
}

// File is one file's contents: steps (OTLP, step log, request log) or an
// ITF trace.
type File struct {
	Path   string
	Format Format
	Steps  []qtrace.Step
	ITF    *itf.Trace
	// Torn is a step log or request log whose last line was cut off:
	// Steps are those before it, and a loss record (qtrace.ReadJSONL,
	// qdriver.Read).
	Torn *qtrace.TornTail
	// Recorder is a request log's header's recorder ("" for any other
	// format): its records are numbered in it (Distinct).
	Recorder string
	// Warnings are what reading it found that a reader should know and
	// that decides nothing (qdriver.Log.Warnings).
	Warnings []string
}

// Distinct refuses two request logs naming one recorder: their records
// would be numbered alike, and Reconstruct would find every one of the
// second's a conflicting duplicate of the first's. The fix names both.
func Distinct(files ...*File) error {
	by := map[string]string{}
	for _, f := range files {
		if f == nil || f.Recorder == "" {
			continue
		}
		if first, ok := by[f.Recorder]; ok {
			return fmt.Errorf("%s and %s are request logs of one recorder %q: each log's records are numbered from 1 in its header's recorder, so the second's would collide with the first's. "+
				"Have each driver run write its own recorder (Playwright's adapter: a recorder per page, the default; Bombadil: bombadil.Options.Header.Recorder), or edit one header", first, f.Path, f.Recorder)
		}
		by[f.Recorder] = f.Path
	}
	return nil
}

// Read reads path. format may be "" or "auto" to detect it.
func Read(path string, format Format) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if format == "" || format == "auto" {
		if format = Sniff(data); format == Unknown && tornFirstLine(data) {
			format = StepLog // its one line cut off: ReadJSONL says it is torn
		}
	}
	in := &File{Path: path, Format: format}
	switch format {
	case OTLP:
		in.Steps, err = otelio.ReadSteps(bytes.NewReader(data))
	case StepLog:
		in.Steps, err = qtrace.ReadJSONL(bytes.NewReader(data))
		if errors.As(err, &in.Torn) {
			err = nil
		}
	case Requests:
		var l *qdriver.Log
		if l, err = qdriver.Read(bytes.NewReader(data)); errors.As(err, &in.Torn) {
			err = nil
		}
		if err == nil {
			in.Steps, in.Recorder, in.Warnings = l.Steps(), l.Header.Recorder, l.Warnings()
		}
	case ITF:
		in.ITF, err = itf.Parse(data)
	default:
		if strings.TrimSpace(string(data)) == "" {
			return in, nil // an empty file holds no steps
		}
		return nil, fmt.Errorf("%s: not OTLP/JSON, a step log, a request log or ITF (its first value %s). Want one of: "+
			"a step log, one object per line with %q, %q, %q, the step's arguments as %q<parameter> and its observations as %q<name> "+
			"(qobs.JSONLSink writes it), e.g. %s; a driver's request log, its first line {\"protocol\": %q, \"recorder\": ...} (package qdriver); "+
			"OTLP/JSON with \"resourceSpans\"; or ITF with \"states\"",
			path, firstKeys(data), qtrace.KeyAction, qtrace.KeySeq, qtrace.KeyRecorder, qtrace.PrefixArg, qtrace.PrefixObs, StepLogExample, qdriver.Protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("%s (%s): %w", path, format, err)
	}
	// a step log's step names its file and line (file:N), for a report
	// over several inputs, as a request log's does
	if format == StepLog || format == Requests {
		for i := range in.Steps {
			if s := &in.Steps[i]; strings.HasPrefix(s.Source, "line ") {
				s.Source = path + ":" + strings.TrimPrefix(s.Source, "line ")
			}
		}
	}
	return in, nil
}

// StepLogExample is one line of a step log: a deposit of 5 that left a
// balance of 5, the first step of recorder r.
// Read-only.
var StepLogExample = fmt.Sprintf(`{%q:"deposit",%q:1,%q:"r",%q:5,%q:5}`,
	qtrace.KeyAction, qtrace.KeySeq, qtrace.KeyRecorder, qtrace.PrefixArg+"amount", qtrace.PrefixObs+"balance")

// firstKeys describes the keys of data's first JSON object, or says it has
// none.
func firstKeys(data []byte) string {
	var first map[string]json.RawMessage
	if json.NewDecoder(bytes.NewReader(data)).Decode(&first) != nil {
		return "is not a JSON object"
	}
	keys := make([]string, 0, len(first))
	for k := range first {
		keys = append(keys, fmt.Sprintf("%q", k))
	}
	sort.Strings(keys)
	return "has the keys " + strings.Join(keys, ", ")
}

// tornFirstLine reports whether data is one line, an object begun and
// cut off (not whole, no newline after it): a step log whose writer
// stopped in its first step, which is torn, not of an unknown format.
func tornFirstLine(data []byte) bool {
	line := bytes.TrimLeft(data, " \t\r\n")
	return len(line) > 0 && line[0] == '{' && !bytes.ContainsRune(line, '\n') && !json.Valid(line)
}
