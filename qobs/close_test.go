package qobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// closingSink counts its Close calls and returns err from each.
type closingSink struct {
	MemorySink
	closes int
	err    error
}

func (c *closingSink) Close() error { c.closes++; return c.err }

// Closing a recorder closes its sink and returns the sink's error, once:
// a second Close closes nothing and returns the same error.
func TestRecorderCloseClosesItsSink(t *testing.T) {
	full := errors.New("disk full")
	sink := &closingSink{err: full}
	rec := NewRecorder(sink)
	rec.Record(context.Background(), "deposit", nil)
	if err := rec.Close(); !errors.Is(err, full) {
		t.Errorf("Close: %v, want the sink's error", err)
	}
	if err := rec.Close(); !errors.Is(err, full) {
		t.Errorf("a second Close: %v, want the first one's error", err)
	}
	if sink.closes != 1 {
		t.Errorf("the sink was closed %d times, want once", sink.closes)
	}
	rec.Record(context.Background(), "withdraw", nil)
	if n, late := len(sink.Steps()), rec.Late(); n != 1 || len(late) != 1 {
		t.Errorf("after Close: %d steps in the sink, %d late; want 1 and 1", n, len(late))
	}
}

// A recorder's Close flushes a Buffered file sink and closes its file:
// the step is in the file after it, not before.
func TestRecorderCloseFlushesABufferedLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "steps.jsonl")
	sink, err := FileSink(p, Buffered)
	if err != nil {
		t.Fatal(err)
	}
	rec := NewRecorder(sink)
	rec.Record(context.Background(), "deposit", nil, "amount", 5)
	if b, _ := os.ReadFile(p); len(b) != 0 {
		t.Fatalf("a Buffered log wrote before Close: %q", b)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	steps, err := qtrace.ReadJSONL(f)
	if err != nil || len(steps) != 1 || steps[0].Action != "deposit" {
		t.Errorf("the log after Close: %v, %v", steps, err)
	}
	if err := sink.Close(); err != nil { // its owner closed it already: no error, nothing closed twice
		t.Errorf("the sink's own Close after the recorder's: %v", err)
	}
}

// writer is a caller's writer that notes whether it was closed.
type writer struct {
	strings.Builder
	closed bool
}

func (w *writer) Close() error { w.closed = true; return nil }

// A log over a caller's writer is flushed by its recorder's Close, and
// the writer left open: it is the caller's.
func TestRecorderCloseLeavesTheCallersWriter(t *testing.T) {
	var w writer
	rec := NewRecorder(NewJSONLSink(&w, Buffered))
	rec.Record(context.Background(), "deposit", nil)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), `"quint.action":"deposit"`) {
		t.Errorf("Close did not flush to the writer: %q", w.String())
	}
	if w.closed {
		t.Error("Close closed the caller's writer")
	}
}

// A Tee closes its sinks, joining their errors; a sink hidden behind
// struct{ Sink } (one several recorders share) is not closed.
func TestTeeClose(t *testing.T) {
	a, b := errors.New("a"), errors.New("b")
	sa, sb, shared := &closingSink{err: a}, &closingSink{err: b}, &closingSink{}
	err := NewRecorder(Tee{sa, &MemorySink{}, sb, struct{ Sink }{shared}}).Close()
	if !errors.Is(err, a) || !errors.Is(err, b) {
		t.Errorf("Close: %v, want both sinks' errors", err)
	}
	if sa.closes != 1 || sb.closes != 1 || shared.closes != 0 {
		t.Errorf("closes: %d, %d, shared %d; want 1, 1, 0", sa.closes, sb.closes, shared.closes)
	}
}
