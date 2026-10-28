package gristtest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// Upcasts fails t with every problem of app's upcasters
// (App.CheckUpcasters): run over generated values of each old version's
// struct, each must be total, pure and keep the event's tags, and reach
// the next version with every field it does not Drop.
func Upcasts(t testing.TB, app *grist.App) {
	t.Helper()
	if err := app.CheckUpcasters(); err != nil {
		t.Fatal(err)
	}
}

// Snapshots fails t when a read model's snapshot in st, or one taken at
// any position of its log, plus the events after it, folds to other than
// a fold from the start (App.CheckSnapshots: run it on a store a test
// made, a restored Log's or a tier's; it writes snapshots).
func Snapshots(t testing.TB, app *grist.App, st grist.Store) {
	t.Helper()
	if err := app.CheckSnapshots(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

// A Log is a store's history as the batches that made it, in commit
// order, each with its tenant: what an app of one version appended (its
// events, markers, checkpoints and step rows, as that version shaped
// them), kept as testdata so that the app's later versions are judged on
// it (TierConfig.Log, Restore). Record one with Capture before an event
// changes shape, never after: it is the deployed version's log.
type Log struct {
	Batches []LoggedBatch
}

// A LoggedBatch is one committed append of a Log.
type LoggedBatch struct {
	Tenant string      `json:"tenant"`
	Batch  store.Batch `json:"batch"`
}

// Tenants are the log's tenants, in order of their first batch.
func (l Log) Tenants() []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range l.Batches {
		if !seen[b.Tenant] {
			seen[b.Tenant] = true
			out = append(out, b.Tenant)
		}
	}
	return out
}

// Restore appends the log's batches to st in order, each tenant created
// first: st then holds the history the log's app made (positions,
// markers, checkpoints and step rows alike, the rows timed anew), which a
// node of today's app relays and serves from. A batch that does not
// commit is the error (a store already holding events).
func (l Log) Restore(ctx context.Context, st grist.Store) error {
	for _, t := range l.Tenants() {
		if err := st.CreateTenant(ctx, t); err != nil {
			return err
		}
	}
	for i, b := range l.Batches {
		if _, err := st.Append(ctx, b.Tenant, b.Batch); err != nil {
			return fmt.Errorf("gristtest: restoring the log's batch %d (tenant %s): %w (restore into a fresh store)", i+1, b.Tenant, err)
		}
	}
	return nil
}

// WriteLog writes l to path, a batch per line.
func WriteLog(l Log, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, b := range l.Batches {
		if err := enc.Encode(b); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadLog reads the log at path (WriteLog's), failing t if it cannot.
func ReadLog(t testing.TB, path string) Log {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var l Log
	d := json.NewDecoder(f)
	for {
		var b LoggedBatch
		if err := d.Decode(&b); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("%s: batch %d: %v", path, len(l.Batches)+1, err)
		}
		l.Batches = append(l.Batches, b)
	}
	return l
}

// Capturing is a store that records every batch it commits (Capture).
type Capturing struct {
	grist.Store
	mu  sync.Mutex
	log Log
}

// Capture wraps st, recording each batch an Append commits: its Log is
// the history made through it. It keeps st's step log, tenant feed and
// snapshots, so a node runs on it.
func Capture(st grist.Store) *Capturing { return &Capturing{Store: st} }

// Append implements store.Store, recording the batch if it commits.
func (c *Capturing) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	p, err := c.Store.Append(ctx, t, b)
	if err == nil {
		c.mu.Lock()
		c.log.Batches = append(c.log.Batches, LoggedBatch{Tenant: t, Batch: b})
		c.mu.Unlock()
	}
	return p, err
}

// Log is what was committed through c, in commit order.
func (c *Capturing) Log() Log {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Log{Batches: append([]LoggedBatch(nil), c.log.Batches...)}
}

// Steps implements store.StepLog.
func (c *Capturing) Steps(ctx context.Context, t string, after int64, limit int) ([]store.StepRow, error) {
	return c.Store.(store.StepLog).Steps(ctx, t, after, limit)
}

// WaitSteps implements store.StepLog.
func (c *Capturing) WaitSteps(ctx context.Context, t string, after int64) error {
	return c.Store.(store.StepLog).WaitSteps(ctx, t, after)
}

// StepCount implements store.StepLog.
func (c *Capturing) StepCount(ctx context.Context, t string) (int64, error) {
	return c.Store.(store.StepLog).StepCount(ctx, t)
}

// WaitTenants implements store.TenantFeed.
func (c *Capturing) WaitTenants(ctx context.Context, n int) error {
	return c.Store.(store.TenantFeed).WaitTenants(ctx, n)
}

// PutSnapshot implements store.SnapshotStore.
func (c *Capturing) PutSnapshot(ctx context.Context, t string, s store.Snapshot) error {
	return c.Store.(store.SnapshotStore).PutSnapshot(ctx, t, s)
}

// GetSnapshot implements store.SnapshotStore.
func (c *Capturing) GetSnapshot(ctx context.Context, t, name, key, fp string) (store.Snapshot, bool, error) {
	return c.Store.(store.SnapshotStore).GetSnapshot(ctx, t, name, key, fp)
}
