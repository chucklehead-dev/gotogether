package qtrace

import (
	"bytes"
	"errors"
	"strings"
	"testing"
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
	if !errors.As(err, &tt) || tt.Line != 2 || len(got) != 1 || got[0].Action != "a" {
		t.Fatalf("torn log: %d steps, %v", len(got), err)
	}
	if got, err := ReadJSONL(strings.NewReader(whole)); err != nil || len(got) != 2 {
		t.Fatalf("whole log: %d steps, %v", len(got), err)
	}
	broken := strings.Replace(whole, `"quint.action":"a"`, `"quint.action":`, 1)
	if _, err := ReadJSONL(strings.NewReader(broken)); err == nil || errors.As(err, &tt) {
		t.Fatalf("a bad first line: %v", err)
	}
}
