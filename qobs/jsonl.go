package qobs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// JSONLSink writes each step as one line of the native step log: a flat
// JSON object with the same keys as the OTel attributes (see qtrace), no
// OTel involved, after the log's format record (qtrace.FormatLine,
// gotogether.steps/1), which goes out with the first step.
// `gotogether validate` reads it like OTLP/JSON.
type JSONLSink struct {
	mu  sync.Mutex
	w   io.Writer
	buf *bufio.Writer // Buffered only
	c   io.Closer     // FileSink's file
	err error
	// sync: Sync the writer after each step (Synced)
	sync   bool
	closed bool // Close was called: a later step is an error, not lost in a buffer
	// headed: the format record is written (with the first step, in its
	// write: a log is never a format record alone, and a disk sees one
	// write per step)
	headed bool
}

// Durability is how much of a step log survives its writer's end.
type Durability int

const (
	// PerStep writes each step at once, with no buffer in the process: a
	// step is in the file (the OS's, at least) when Record returns, so a
	// process that is killed loses none. The OS writes the file back in
	// its own time: a machine that loses power can lose the last steps and
	// tear the line being written (qtrace.ReadJSONL keeps the steps before
	// a torn last line, and says so). Steps lost at the end of a log cannot
	// be detected (no later sequence number reveals the gap), which is why
	// it is the default; a loss known otherwise is declared to the check
	// (qtrace.MarkLost, gotogether validate -lost-suffix).
	PerStep Durability = iota
	// Synced also syncs after every step (an fsync; the writer's Sync
	// method for NewJSONLSink): a step is on the disk when Record returns,
	// so it survives the machine losing power. It costs a disk round trip
	// per step.
	Synced
	// Buffered buffers steps, for long runs nothing kills: fewer writes.
	// Close its recorder (or it) when done: steps still in the buffer
	// are lost if the process dies, undetectably.
	Buffered
)

// NewJSONLSink writes the step log to w, as durably as d says (Synced
// syncs w if it has a Sync method, as *os.File and qenv.File have). w
// stays the caller's: Close flushes to it and leaves it open.
func NewJSONLSink(w io.Writer, d Durability) *JSONLSink {
	j := &JSONLSink{w: w, sync: d == Synced}
	if _, ok := w.(interface{ Sync() error }); d == Synced && !ok {
		// a sync that cannot happen, silently, would claim a durability
		// the log does not have
		j.err = fmt.Errorf("qobs: a Synced step log needs a writer with a Sync method (%T has none): give it the *os.File, or another durability", w)
	}
	if d == Buffered {
		j.buf = bufio.NewWriterSize(w, 64*1024)
		j.w = j.buf
	}
	return j
}

// FileSink creates (truncating) a step log file written as durably as d
// says: PerStep, the default, loses no step to a killed process. The
// sink owns the file, which its Close (or its recorder's) closes.
func FileSink(path string, d Durability) (*JSONLSink, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	j := NewJSONLSink(f, d)
	j.c = f
	return j, nil
}

// Flush writes buffered steps to the file.
func (j *JSONLSink) Flush() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.buf != nil {
		if err := j.buf.Flush(); err != nil && j.err == nil {
			j.err = err
		}
	}
	return j.err
}

// lineBufs are the buffers steps are encoded in, outside the sink's
// lock: steps recorded on several cores encode in parallel.
// Process-wide.
var lineBufs = sync.Pool{New: func() any { b := make([]byte, 0, 512); return &b }}

func (j *JSONLSink) Record(_ context.Context, s *qtrace.Step) {
	bp := lineBufs.Get().(*[]byte)
	b, err := s.AppendLine((*bp)[:0])
	defer func() {
		if cap(b) <= 64<<10 { // a huge step's buffer is not kept
			*bp = b[:0]
			lineBufs.Put(bp)
		}
	}()
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		if j.err == nil {
			j.err = fmt.Errorf("qobs: step %s recorded after the step log was closed", s.Action)
		}
		return
	}
	if err == nil {
		if !j.headed {
			b = append(qtrace.FormatLine(), b...) // (once: a copy the pool does not keep)
		}
		_, err = j.w.Write(b)
		j.headed = j.headed || err == nil
	}
	if s, ok := j.w.(interface{ Sync() error }); ok && err == nil && j.sync {
		err = s.Sync()
	}
	if err != nil && j.err == nil {
		j.err = err
	}
}

// Err is the first write error, if any.
func (j *JSONLSink) Err() error { j.mu.Lock(); defer j.mu.Unlock(); return j.err }

// Close flushes a Buffered log and closes a FileSink's file, and returns
// the first error the log met. A writer given to NewJSONLSink is the
// caller's: Close flushes to it but does not close it. A step recorded
// after Close is an error (Err), and a second Close returns the first
// one's error again. Recorder.Close calls it: a sink given to a recorder
// is closed by closing the recorder.
func (j *JSONLSink) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closed = true
	if j.buf != nil {
		if err := j.buf.Flush(); err != nil && j.err == nil {
			j.err = err
		}
		j.buf = nil
	}
	if j.c != nil {
		if err := j.c.Close(); err != nil && j.err == nil {
			j.err = err
		}
		j.c = nil
	}
	return j.err
}
