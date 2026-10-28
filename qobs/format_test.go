package qobs

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// writes counts its writes.
type writes struct {
	bytes.Buffer
	n int
}

func (w *writes) Write(b []byte) (int, error) { w.n++; return w.Buffer.Write(b) }

// A step log begins with its format record, written with the first
// step: one write per step still, and an empty log stays empty.
func TestJSONLSinkNamesItsFormat(t *testing.T) {
	var w writes
	sink := NewJSONLSink(&w, PerStep)
	rec := NewRecorder(sink)
	if err := rec.Close(); err != nil || w.Len() != 0 {
		t.Fatalf("an empty log: %v %q", err, w.String())
	}
	sink = NewJSONLSink(&w, PerStep)
	rec = NewRecorder(sink)
	rec.Record(context.Background(), "deposit", nil)
	rec.Record(context.Background(), "withdraw", nil)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(w.String()), "\n")
	if w.n != 2 || len(lines) != 3 || lines[0]+"\n" != string(qtrace.FormatLine()) {
		t.Fatalf("%d writes, lines %q", w.n, lines)
	}
	steps, err := qtrace.ReadJSONL(strings.NewReader(w.String()))
	if err != nil || len(steps) != 2 || steps[0].Action != "deposit" {
		t.Fatalf("read back: %v %+v", err, steps)
	}
}
