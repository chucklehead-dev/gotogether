package main

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

type failedFinalFile struct {
	syncErr, closeErr error
	closed            bool
}

func (*failedFinalFile) Write(b []byte) (int, error) { return len(b), nil }
func (f *failedFinalFile) Sync() error               { return f.syncErr }
func (f *failedFinalFile) Close() error              { f.closed = true; return f.closeErr }

func TestJournalFinalizationRejectsSyncCloseAndDirectoryFailure(t *testing.T) {
	for _, tc := range []struct {
		name              string
		syncErr, closeErr error
		missingDir        bool
	}{
		{name: "sync", syncErr: io.ErrUnexpectedEOF},
		{name: "close", closeErr: io.ErrClosedPipe},
		{name: "both", syncErr: io.ErrUnexpectedEOF, closeErr: io.ErrClosedPipe},
		{name: "directory", missingDir: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.jsonl")
			if tc.missingDir {
				path = filepath.Join(path, "missing", "journal")
			}
			f := &failedFinalFile{syncErr: tc.syncErr, closeErr: tc.closeErr}
			j := journal{file: f, path: path}
			err := j.finish()
			if err == nil || !f.closed {
				t.Fatalf("finalization accepted incomplete durability: %v closed=%v", err, f.closed)
			}
			if tc.syncErr != nil && !errors.Is(err, tc.syncErr) {
				t.Fatal("lost Sync error")
			}
			if tc.closeErr != nil && !errors.Is(err, tc.closeErr) {
				t.Fatal("lost Close error")
			}
			if tc.missingDir && !strings.Contains(err.Error(), "no such file") {
				t.Fatalf("missing directory evidence: %v", err)
			}
		})
	}
}
