package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// journal is an append-only internal campaign record, not a resume file.
// Sync makes each completed record durable before progress is reported.
// A missing complete event means partial evidence, even if every last observed
// trial passed. An abrupt kill may also leave an incomplete final JSON line.
type journal struct {
	mu   sync.Mutex
	file *os.File
	path string
}

type journalEvent struct {
	Event string    `json:"event"`
	At    time.Time `json:"at"`
	Data  any       `json:"data"`
}

type targetRecord struct {
	Suite int `json:"suite"`
	ID    int `json:"id"`
}
type sourceRecord struct {
	File       string         `json:"file"`
	Ordinal    int            `json:"ordinal"`
	Descriptor string         `json:"descriptor"`
	Targets    []targetRecord `json:"targets"`
}
type inventoryRecord struct {
	File       string `json:"file"`
	Ordinal    int    `json:"ordinal"`
	Descriptor string `json:"descriptor"`
	ID         int    `json:"id"`
}
type suiteRecord struct {
	Index     int               `json:"index"`
	Package   string            `json:"package"`
	Directory string            `json:"directory"`
	Binary    string            `json:"binary"`
	Inventory []inventoryRecord `json:"inventory"`
}
type attemptRecord struct {
	Execution executionResult `json:"execution"`
	targetRecord
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
}
type outcomeRecord struct {
	Source   sourceRecord    `json:"source"`
	Outcome  string          `json:"outcome"`
	Attempts []attemptRecord `json:"attempts"`
	Error    string          `json:"error,omitempty"`
}
type completeRecord struct {
	Total      int  `json:"total"`
	Survivors  int  `json:"survivors"`
	GatePassed bool `json:"gate_passed"`
}

func sourceMetadata(c candidate) sourceRecord {
	r := sourceRecord{File: c.file, Ordinal: c.ordinal, Descriptor: c.descriptor}
	for _, t := range c.targets {
		r.Targets = append(r.Targets, targetRecord{t.suite, t.id})
	}
	return r
}
func suiteMetadata(i int, s suite) suiteRecord {
	r := suiteRecord{Index: i, Package: s.pkg, Directory: s.dir, Binary: s.binary}
	for _, m := range s.mutants {
		r.Inventory = append(r.Inventory, inventoryRecord{m.file, m.ordinal, m.descriptor, m.id})
	}
	return r
}

func openJournal(root, pkg, explicit string) (*journal, error) {
	var file *os.File
	var err error
	if explicit == "" {
		dir := filepath.Join(root, ".gotogether", "mutations")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		file, err = os.CreateTemp(dir, strings.ReplaceAll(pkg, "/", "-")+"-*.jsonl")
	} else {
		if !filepath.IsAbs(explicit) {
			explicit = filepath.Join(root, explicit)
		}
		if err = os.MkdirAll(filepath.Dir(explicit), 0700); err != nil {
			return nil, err
		}
		// Never overwrite evidence from an earlier run. There is no resume mode.
		file, err = os.OpenFile(explicit, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	}
	if err != nil {
		return nil, err
	}
	return &journal{file: file, path: file.Name()}, nil
}

func (j *journal) append(event string, data any) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := json.NewEncoder(j.file).Encode(journalEvent{event, time.Now().UTC(), data}); err != nil {
		return fmt.Errorf("write campaign record: %w", err)
	}
	if err := j.file.Sync(); err != nil {
		return fmt.Errorf("sync campaign record: %w", err)
	}
	return nil
}
