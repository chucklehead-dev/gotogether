package qobs

import (
	"bufio"
	"context"
	"io"
	"os"
	"sync"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// JSONLSink writes each step as one line of the native step log: a flat
// JSON object with the same keys as the OTel attributes (see qtrace), no
// OTel involved. `gotogether validate` reads it like OTLP/JSON.
type JSONLSink struct {
	mu  sync.Mutex
	w   io.Writer
	buf *bufio.Writer // Buffered only
	c   io.Closer     // FileSink's file
	err error
	// sync: Sync the writer after each step (Synced)
	sync bool
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
	// it is the default.
	PerStep Durability = iota
	// Synced also syncs after every step (an fsync; the writer's Sync
	// method for NewJSONLSink): a step is on the disk when Record returns,
	// so it survives the machine losing power. It costs a disk round trip
	// per step.
	Synced
	// Buffered buffers steps, for long runs nothing kills: fewer writes.
	// Close (or Flush) it when done: steps still in the buffer are lost if
	// the process dies, undetectably.
	Buffered
)

// NewJSONLSink writes the step log to w, as durably as d says (Synced
// syncs w if it has a Sync method, as *os.File and qenv.File have).
func NewJSONLSink(w io.Writer, d Durability) *JSONLSink {
	j := &JSONLSink{w: w, sync: d == Synced}
	if d == Buffered {
		j.buf = bufio.NewWriterSize(w, 64*1024)
		j.w = j.buf
	}
	return j
}

// FileSink creates (truncating) a step log file written as durably as d
// says: PerStep, the default, loses no step to a killed process.
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

func (j *JSONLSink) Record(_ context.Context, s *qtrace.Step) {
	b, err := s.MarshalLine()
	j.mu.Lock()
	defer j.mu.Unlock()
	if err == nil {
		_, err = j.w.Write(b)
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

// Close closes the file of a FileSink and returns the first error.
func (j *JSONLSink) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
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
