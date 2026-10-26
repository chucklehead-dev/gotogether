package kv

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// RotateEvery has the store start a new step log every n operations:
// after the n-th, under the lock that orders operations (so none is in
// flight), it calls next, which switches the log (RotatingLog.Rotate), and
// records the store's whole state as the new log's first step, the
// model's restore (the binding's snapshot). The recorder is the same, so
// seqs go on across logs: a later log alone has lost its prefix, and its
// head says the state it goes on from. A failed next stops rotating; Err
// reports it.
func (s *Store) RotateEvery(n int, next func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.every, s.next = int64(n), next
}

// Rotations is how many logs the store has started after the first.
func (s *Store) Rotations() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rotations
}

// Err is the error that stopped rotation, if any.
func (s *Store) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rotateErr
}

// restore is a snapshot step's arguments: the model's store as two maps
// over its keys, and the revision.
type restore struct {
	values   map[string]string
	versions map[string]int64
	rev      int64
}

// rotate starts the next log after every s.every operations; called with
// s.mu held, after the operation's step.
func (s *Store) rotate(ctx context.Context) {
	if s.every <= 0 || s.next == nil || s.rotateErr != nil || s.op%s.every != 0 {
		return
	}
	if err := s.next(); err != nil {
		s.rotateErr = err
		return
	}
	s.rotations++
	head := restore{values: map[string]string{}, versions: map[string]int64{}, rev: s.revision}
	for k, e := range s.data {
		head.values[k], head.versions[k] = e.Value, e.Version
	}
	if s.mutation == StaleSnapshot {
		// the head written at the previous rotation; this one's is kept
		// for the next
		if s.stale.values == nil {
			s.stale = restore{values: map[string]string{}, versions: map[string]int64{}}
		}
		head, s.stale = s.stale, head
	}
	qobs.Record(ctx, "restore", nil, "values", head.values, "versions", head.versions, "rev", head.rev)
}

// RotatingLog is a step log in files of their own, one per rotation, each
// written PerStep: path's base with .1, .2, ... before its extension
// (steps.jsonl: steps.1.jsonl, steps.2.jsonl). Rotate closes one and
// starts the next.
type RotatingLog struct {
	mu    sync.Mutex
	path  string
	cur   *qobs.JSONLSink
	files []string
}

// NewRotatingLog starts the first file of the log at path.
func NewRotatingLog(path string) (*RotatingLog, error) {
	l := &RotatingLog{path: path}
	return l, l.Rotate()
}

// Record writes s to the current file.
func (l *RotatingLog) Record(ctx context.Context, s *qtrace.Step) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cur.Record(ctx, s)
}

// Rotate closes the current file and starts the next.
func (l *RotatingLog) Rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur != nil {
		if err := l.cur.Close(); err != nil {
			return err
		}
	}
	ext := ".jsonl"
	base := strings.TrimSuffix(l.path, ext)
	if base == l.path {
		ext = ""
	}
	name := fmt.Sprintf("%s.%d%s", base, len(l.files)+1, ext)
	sink, err := qobs.FileSink(name, qobs.PerStep)
	if err != nil {
		return err
	}
	l.cur, l.files = sink, append(l.files, name)
	return nil
}

// Files are the log's files, in order.
func (l *RotatingLog) Files() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.files...)
}

// Close closes the current file.
func (l *RotatingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur.Close()
}
