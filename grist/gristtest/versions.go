package gristtest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
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
	// CaptureError means capture could not establish a complete committed
	// history. WriteLog and Restore refuse it; the capture never turns an
	// unknown append outcome into a claim that nothing committed.
	CaptureError string
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
// node of today's app relays and serves from. Captured RetainedIDs preserve
// original event identity. A historical batch with none materializes new
// identities: its original identities were not recorded. A poisoned capture
// is refused before changing st. A batch that does not commit is the error
// (a store already holding events).
func (l Log) Restore(ctx context.Context, st grist.Store) error {
	if l.CaptureError != "" {
		return fmt.Errorf("gristtest: cannot restore incomplete capture: %s", l.CaptureError)
	}
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
	if l.CaptureError != "" {
		return fmt.Errorf("gristtest: cannot write incomplete capture: %s", l.CaptureError)
	}
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
	mu       sync.Mutex
	log      Log
	inFlight atomic.Int32
}

// Capture wraps st, recording each batch an Append commits: its Log is
// the history made through it, including immutable identities assigned through
// the trusted retained-history seam. It submits those identities atomically
// with the batch instead of reading them after commit, when a restore could
// already have replaced the events. Overlapping Append calls or unknown outcomes
// poison the capture rather than claim a complete history. It keeps st's step
// log, tenant feed and snapshots.
func Capture(st grist.Store) *Capturing { return &Capturing{Store: st} }

// Append implements store.Store, recording the batch if it commits.
func (c *Capturing) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if c.inFlight.Add(1) != 1 {
		c.poison("overlapping appends: capture cannot establish their commit order; capture a fresh sequential history")
	}
	defer c.inFlight.Add(-1)
	batch := copyCapturedBatch(b)
	generated := batch.RetainedIDs == nil && len(batch.Events) > 0
	var p store.Position
	var err error
	for {
		if generated {
			batch.RetainedIDs = make([]string, len(batch.Events))
			for i := range batch.RetainedIDs {
				batch.RetainedIDs[i] = qenv.NewID(ctx)
			}
		}
		p, err = c.Store.Append(ctx, t, copyCapturedBatch(batch))
		var exists *store.IdentityExists
		if generated && errors.As(err, &exists) && !store.MayHaveCommitted(err) && ctx.Err() == nil {
			continue // Imported IDs may precede a new simulation's allocator.
		}
		break
	}
	if err != nil {
		if store.MayHaveCommitted(err) {
			c.poison(fmt.Sprintf("append of tenant %s has unknown outcome: %v; capture a fresh history", t, err))
		}
		return p, err
	}
	c.mu.Lock()
	c.log.Batches = append(c.log.Batches, LoggedBatch{Tenant: t, Batch: batch})
	c.mu.Unlock()
	return p, nil
}

func (c *Capturing) poison(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.log.CaptureError == "" {
		c.log.CaptureError = reason
	}
}

func copyCapturedBatch(batch store.Batch) store.Batch {
	batch.Events = slices.Clone(batch.Events)
	for i := range batch.Events {
		batch.Events[i] = batch.Events[i].Clone()
	}
	batch.RetainedIDs = slices.Clone(batch.RetainedIDs)
	batch.Step = slices.Clone(batch.Step)
	if batch.Condition != nil {
		condition := *batch.Condition
		condition.Selectors = slices.Clone(condition.Selectors)
		for i := range condition.Selectors {
			condition.Selectors[i].Types = slices.Clone(condition.Selectors[i].Types)
			condition.Selectors[i].Tags = slices.Clone(condition.Selectors[i].Tags)
		}
		batch.Condition = &condition
	}
	if batch.Marker != nil {
		marker := *batch.Marker
		marker.Receipt.Events = slices.Clone(marker.Receipt.Events)
		batch.Marker = &marker
	}
	if batch.Checkpoint != nil {
		checkpoint := *batch.Checkpoint
		batch.Checkpoint = &checkpoint
	}
	return batch
}

// Log is an independent copy of the capture. CaptureError must be empty before
// the batches can be claimed as a complete committed history. Sampling during
// an Append returns an unusable snapshot; a later completed capture may be
// usable if no overlapping call or unknown outcome permanently poisoned it.
func (c *Capturing) Log() Log {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Log{Batches: slices.Clone(c.log.Batches), CaptureError: c.log.CaptureError}
	if out.CaptureError == "" && c.inFlight.Load() != 0 {
		out.CaptureError = "append still in flight: sample Log after all Append calls return"
	}
	for i := range out.Batches {
		out.Batches[i].Batch = copyCapturedBatch(out.Batches[i].Batch)
	}
	return out
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
