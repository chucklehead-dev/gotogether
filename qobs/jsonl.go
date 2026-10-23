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
	buf *bufio.Writer // FileSink only
	c   io.Closer
	err error
	// sync: Sync the writer after each step (SyncedFileSink)
	sync bool
}

// NewJSONLSink writes the step log to w.
func NewJSONLSink(w io.Writer) *JSONLSink { return &JSONLSink{w: w} }

// FileSink creates (truncating) a step log file written one step at a
// time, with no buffer in the process: a step is in the file (the OS's,
// at least) when Record returns, so a process that is killed loses none.
// Steps lost at the end of a log cannot be detected (no later sequence
// number reveals the gap), which is why it is the default;
// BufferedFileSink is faster where nothing crashes. The OS writes the
// file back in its own time: a machine that loses power can lose the
// last steps and tear the line being written (SyncedFileSink).
func FileSink(path string) (*JSONLSink, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &JSONLSink{w: f, c: f}, nil
}

// SyncedFileSink is FileSink with an fsync after every step: a step is on
// the disk when Record returns, so it survives the machine losing power,
// not only the process being killed. FileSink's steps reach the OS at
// once and the disk when the OS writes them back: a power loss can take
// the last ones, and tear the line being written (qtrace.ReadJSONL keeps
// the steps before a torn last line, and says so). An fsync costs a disk
// round trip per step.
func SyncedFileSink(path string) (*JSONLSink, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &JSONLSink{w: f, c: f, sync: true}, nil
}

// NewSyncedJSONLSink writes the step log to w and syncs it after every
// step (w's Sync method, as *os.File and qenv.File have).
func NewSyncedJSONLSink(w interface {
	io.Writer
	Sync() error
}) *JSONLSink {
	return &JSONLSink{w: w, sync: true}
}

// BufferedFileSink creates (truncating) a buffered step log file: fewer
// writes, for long runs nothing kills. Close (or Flush) it when done:
// steps still in the buffer are lost if the process dies, undetectably.
func BufferedFileSink(path string) (*JSONLSink, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	b := bufio.NewWriterSize(f, 64*1024)
	return &JSONLSink{w: b, buf: b, c: f}, nil
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
