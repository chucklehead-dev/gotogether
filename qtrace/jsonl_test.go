package qtrace

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// A log whose writer stopped in the middle of its last line keeps the
// steps before it, and says the tail is torn; a bad line anywhere else
// is an error.
func TestReadJSONLTornTail(t *testing.T) {
	var b bytes.Buffer
	steps := []Step{{Action: "a", Recorder: "r", Seq: 1}, {Action: "b", Recorder: "r", Seq: 2}}
	if err := WriteJSONL(&b, steps); err != nil {
		t.Fatal(err)
	}
	whole := b.String()
	torn := whole[:len(whole)-10] // the second line cut off
	got, err := ReadJSONL(strings.NewReader(torn))
	var tt *TornTail
	// the step before the cut, and the loss of what its recorder wrote after
	if !errors.As(err, &tt) || tt.Line != 3 /* the format record is line 1 */ || len(got) != 2 || got[0].Action != "a" || got[1].Lost != "suffix" || got[1].LostAfter != 1 {
		t.Fatalf("torn log: %+v, %v", got, err)
	}
	if got, err := ReadJSONL(strings.NewReader(whole)); err != nil || len(got) != 2 {
		t.Fatalf("whole log: %d steps, %v", len(got), err)
	}
	broken := strings.Replace(whole, `"quint.action":"a"`, `"quint.action":`, 1)
	if _, err := ReadJSONL(strings.NewReader(broken)); err == nil || errors.As(err, &tt) {
		t.Fatalf("a bad first line: %v", err)
	}
}

// A step log keeps what the records are: an invocation and its
// completion's call, a settle, and an assumed step (a resolution's),
// which a validated log once lost, its expect checked after all.
func TestJSONLKeepsRecordKinds(t *testing.T) {
	in := []Step{
		{Action: "write", Seq: 1, Recorder: "r", Invocation: true, Time: time.Unix(1, 0)},
		{Action: "write", Seq: 2, Recorder: "r", Call: 1, Time: time.Unix(2, 0)},
		{Action: "write", Seq: 3, Recorder: "r", Ambiguous: true, Time: time.Unix(3, 0)},
		{Action: "settled", Seq: 4, Recorder: "r", Settled: true, Time: time.Unix(4, 0)},
	}
	var b bytes.Buffer
	if err := WriteJSONL(&b, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadJSONL(&b)
	if err != nil {
		t.Fatal(err)
	}
	if !out[0].Invocation || out[1].Call != 1 || !out[2].Ambiguous || !out[3].Settled {
		t.Errorf("read back %+v", out)
	}
}
