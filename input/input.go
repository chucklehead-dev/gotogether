// Package input reads any of gotogether's trace inputs, detecting the format:
// OTLP/JSON (spans carrying quint.step events), the native step log (JSONL
// of qtrace steps), or a model-level ITF trace.
package input

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Format of a trace input.
type Format string

const (
	OTLP    Format = "otlp"  // OTLP/JSON TracesData lines
	StepLog Format = "steps" // native step log (qobs.JSONLSink)
	ITF     Format = "itf"   // model-level ITF (gotogether, or quint run --mbt)
	Unknown Format = ""
)

// Sniff detects the format from the first JSON value.
func Sniff(data []byte) Format {
	var first map[string]json.RawMessage
	if json.NewDecoder(bytes.NewReader(data)).Decode(&first) != nil {
		return Unknown
	}
	switch {
	case first["resourceSpans"] != nil:
		return OTLP
	case first["states"] != nil:
		return ITF
	case first[qtrace.KeyAction] != nil:
		return StepLog
	}
	return Unknown
}

// File is one file's contents: steps (OTLP, step log) or an ITF trace.
type File struct {
	Path   string
	Format Format
	Steps  []qtrace.Step
	ITF    *itf.Trace
	// Torn is a step log whose last line was cut off: Steps are those
	// before it (qtrace.ReadJSONL).
	Torn *qtrace.TornTail
}

// Read reads path. format may be "" or "auto" to detect it.
func Read(path string, format Format) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if format == "" || format == "auto" {
		format = Sniff(data)
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
	case ITF:
		in.ITF, err = itf.Parse(data)
	default:
		if strings.TrimSpace(string(data)) == "" {
			return in, nil // an empty file holds no steps
		}
		return nil, fmt.Errorf("%s: not OTLP/JSON, a step log or ITF (its first value %s). Want one of: "+
			"a step log, one object per line with %q, %q and the step's arguments and \"obs.<name>\" observations at the top level "+
			"(qobs.JSONLSink writes it: {%q:\"deposit\",%q:1,%q:\"r\",\"amount\":5,\"obs.balance\":5}); "+
			"OTLP/JSON with \"resourceSpans\"; or ITF with \"states\"", path, firstKeys(data),
			qtrace.KeyAction, qtrace.KeySeq, qtrace.KeyAction, qtrace.KeySeq, qtrace.KeyRecorder)
	}
	if err != nil {
		return nil, fmt.Errorf("%s (%s): %w", path, format, err)
	}
	// a step's source names its file, for a report over several inputs
	for i := range in.Steps {
		if s := &in.Steps[i]; s.Source != "" {
			s.Source = filepath.Base(path) + " " + s.Source
		}
	}
	return in, nil
}

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
