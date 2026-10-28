package qtrace

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// A step log names its format first; a reader skips the record, reads a
// log without one as gotogether.steps/1 (every log written before it had
// a name), and refuses a later major with the fix, wherever the record
// is (logs concatenated), and on an OTel step's span or resource too.
func TestStepLogFormat(t *testing.T) {
	steps := []Step{{Action: "a", Recorder: "r", Seq: 1}, {Action: "b", Recorder: "r", Seq: 2}}
	var b bytes.Buffer
	if err := WriteJSONL(&b, steps); err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(b.String(), "\n")
	if first+"\n" != string(FormatLine()) || !strings.Contains(first, StepsFormat) {
		t.Fatalf("the log's first line is %s, not its format record", first)
	}
	got, err := ReadJSONL(bytes.NewReader(b.Bytes()))
	if err != nil || len(got) != 2 || got[0].Source != "line 2" {
		t.Fatalf("a versioned log: %v %+v", err, got)
	}
	unversioned := strings.SplitN(b.String(), "\n", 2)[1]
	if got, err := ReadJSONL(strings.NewReader(unversioned)); err != nil || len(got) != 2 {
		t.Fatalf("a log without a format record: %v %+v", err, got)
	}
	// two logs concatenated: each record checked, none a step
	if got, err := ReadJSONL(strings.NewReader(b.String() + b.String())); err != nil || len(got) != 4 {
		t.Fatalf("logs concatenated: %v %d steps", err, len(got))
	}
	later := strings.ReplaceAll(b.String(), "gotogether.steps/1", "gotogether.steps/2")
	laterRecord, _, _ := strings.Cut(later, "\n")
	// the record first, after steps, and alone without a newline (a
	// refusal, not a torn tail)
	for _, log := range []string{later, unversioned + laterRecord + "\n", laterRecord} {
		_, err := ReadJSONL(strings.NewReader(log))
		if err == nil || !strings.Contains(err.Error(), "gotogether.steps/2 is a later version than this gotogether reads (gotogether.steps/1): upgrade gotogether") {
			t.Errorf("a later major's log %q: %v", log, err)
		}
	}
	if _, err := ReadJSONL(strings.NewReader(`{"quint.format":"gotogether.steps/0"}` + "\n")); err == nil || !strings.Contains(err.Error(), "gotogether.steps/0 is an older version than this gotogether reads (gotogether.steps/1, pre-release: the current one only): record the run again") {
		t.Errorf("an earlier major's log: %v", err)
	}
	if _, err := ReadJSONL(strings.NewReader(`{"quint.format":"gotogether.binding/2"}` + "\n")); err == nil || !strings.Contains(err.Error(), "is no step log") {
		t.Errorf("another format's record: %v", err)
	}
	// an OTel step: the format on its event, span or resource
	for i := range 3 {
		layers := []map[string]any{{KeyAction: "a"}, {}, {}}
		layers[i][KeyFormat] = "gotogether.steps/2"
		if _, err := FromAttributes(time.Now(), layers...); err == nil || !strings.Contains(err.Error(), "upgrade gotogether") {
			t.Errorf("a later major on layer %d: %v", i, err)
		}
		layers[i][KeyFormat] = StepsFormat
		if _, err := FromAttributes(time.Now(), layers...); err != nil {
			t.Errorf("this major on layer %d: %v", i, err)
		}
	}
}
