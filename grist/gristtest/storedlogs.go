package gristtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// StoredLogsConfig names retained WriteLog files and today's commit-side model
// binding. Every file is required to contain committed batches with step rows.
// Options configures model validation as validate.NewChecker does.
type StoredLogsConfig struct {
	Files   []string
	Binding string
	Options validate.Options
}

// StoredLogResult is one retained fixture's checks. Scope is committed_prefix:
// the helper never invents a settle or executes processors or effects. SHA256
// identifies the provided bytes; it does not prove historical capture coverage.
// The fixture contains no historical snapshots. The snapshots check audits
// today's fold/snapshot round trips, not unchanged business meaning.
type StoredLogResult struct {
	File    string             `json:"file"`
	SHA256  string             `json:"sha256,omitempty"`
	Scope   string             `json:"scope"`
	Batches int                `json:"batches"`
	Events  int                `json:"events"`
	Tenants []string           `json:"tenants,omitempty"`
	Steps   int                `json:"steps"`
	Checks  []receipt.Check    `json:"checks"`
	Model   *validate.Document `json:"model,omitempty"`
}

// StoredLogs checks each retained application history against today's App in
// a fresh in-memory store: strict fixture decoding, app and batch validation,
// restoration, current fold/snapshot round trips, then commit-prefix model
// conformance. It records each fixture's checks and reports failures through t,
// continuing to the next fixture. Missing evidence never becomes a model pass.
//
// No node starts, no command executes, and no processor or effect is invoked.
// Pending effects may legitimately remain in the prefix; eventual properties
// are not checked because a retained Log has no observed settle marker.
// Restore also times step rows anew, so bindings with Within checks are refused:
// this workflow cannot establish historical wall-time obligations.
func StoredLogs(t testing.TB, app *grist.App, cfg StoredLogsConfig) []StoredLogResult {
	t.Helper()
	if app == nil || cfg.Binding == "" || len(cfg.Files) == 0 {
		t.Errorf("gristtest.StoredLogs: supply an App, its commit-side Binding, and at least one retained log file")
		return nil
	}
	var appErr error
	if appErr = app.Verify(); appErr == nil {
		appErr = app.CheckUpcasters()
	}
	results := make([]StoredLogResult, 0, len(cfg.Files))
	for _, file := range cfg.Files {
		r := storedLogResult(file)
		results = append(results, r)
		out := &results[len(results)-1]
		fail := func(stage string, status receipt.Status, err error) {
			out.set(stage, status, err.Error())
			t.Errorf("gristtest.StoredLogs: %s: %s: %v", file, stage, err)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			fail("fixture-read", receipt.Error, err)
			continue
		}
		hash := sha256.Sum256(data)
		out.SHA256 = hex.EncodeToString(hash[:])
		log, err := readStoredLog(data)
		if err != nil {
			fail("fixture-read", receipt.Error, err)
			continue
		}
		out.Batches, out.Tenants = len(log.Batches), log.Tenants()
		for _, b := range log.Batches {
			out.Events += len(b.Batch.Events)
		}
		out.set("fixture-read", receipt.Pass, "strictly decoded retained committed batches")
		if appErr != nil {
			fail("app-validation", receipt.Error, appErr)
			continue
		}
		out.set("app-validation", receipt.Pass, "current App.Verify and CheckUpcasters passed")
		for i, b := range log.Batches {
			if err = grist.CheckTenant(b.Tenant); err != nil {
				err = fmt.Errorf("batch %d has invalid tenant %q: %w", i+1, b.Tenant, err)
				break
			}
			if err = app.CheckStoredBatch(b.Batch); err != nil {
				err = fmt.Errorf("batch %d, tenant %s: %w", i+1, b.Tenant, err)
				break
			}
		}
		if err != nil {
			fail("batch-validation", receipt.Error, err)
			continue
		}
		out.set("batch-validation", receipt.Pass, "stored event decoding and committed step coverage checked")
		st := memstore.New(app.Name)
		if err := log.Restore(t.Context(), st); err != nil {
			fail("restore", receipt.Error, err)
			continue
		}
		out.set("restore", receipt.Pass, "restored into a fresh store")
		if len(app.Catalog().ReadModels) == 0 {
			out.set("snapshots", receipt.NotChecked, "the current app declares no read models to audit")
		} else {
			if err := storedLogSnapshots(t.Context(), app, st); err != nil {
				// CheckSnapshots does not distinguish disagreement from an
				// incomplete audit's read/encoding error. Do not invent a
				// counterexample classification by parsing its display text.
				fail("snapshots", receipt.Error, err)
				continue
			}
			out.set("snapshots", receipt.Pass, "current fold/snapshot round trips at up to 200 sampled positions per read model/key; no historical snapshots were captured")
		}
		sink := &qobs.MemorySink{}
		ctx := qobs.WithRecorder(t.Context(), qobs.NewRecorder(sink))
		relay, err := grist.NewRelay(st)
		if err == nil {
			relay.RenderAs(app)
			err = relay.Flush(ctx)
		}
		if err != nil {
			fail("model", receipt.Error, err)
			continue
		}
		steps := sink.Steps()
		out.Steps = len(steps)
		if len(steps) == 0 {
			fail("model", receipt.NotChecked, fmt.Errorf("no committed steps were relayed: retain step rows for model conformance"))
			continue
		}
		options := cfg.Options
		if options.Dir == "" {
			options.Dir = t.TempDir() // returned document artifact paths live for the test
		}
		checker, err := validate.NewChecker(cfg.Binding, options)
		if err != nil {
			fail("model", receipt.Error, err)
			continue
		}
		if checker.Binding.Complete {
			checker.Close()
			fail("model", receipt.Error, fmt.Errorf("binding requires a complete journey, but retained batches carry no observed settle: use a commit-prefix binding without a completion requirement"))
			continue
		}
		if len(checker.Binding.Within) > 0 {
			checker.Close()
			fail("model", receipt.Error, fmt.Errorf("binding has Within checks, but retained batches do not preserve original step-row timestamps: use a commit-prefix binding without historical wall-time obligations"))
			continue
		}
		// A commit binding that filters an actor or skips an outcome would check
		// only part of the retained prefix while giving an apparently green result.
		for i := range steps {
			if !checker.Binding.ChecksActor(steps[i].Actor) || checker.Binding.Skips(&steps[i]) {
				err = fmt.Errorf("binding excludes committed step %d (%s, actor %s): use a commit-side binding covering every retained step", i+1, steps[i].Action, steps[i].Actor)
				break
			}
		}
		if err != nil {
			checker.Close()
			fail("model", receipt.Error, err)
			continue
		}
		report, err := checker.Check(t.Context(), steps)
		checker.Close()
		if err != nil {
			fail("model", receipt.Error, err)
			continue
		}
		doc := validate.NewDocument("validate")
		doc.Binding = cfg.Binding
		doc.AddReport(report)
		out.Model = doc
		summary := report.Receipt()
		switch summary.Status {
		case receipt.Pass:
			doc.Verdict = "pass"
		case receipt.Fail:
			doc.Verdict, doc.Exit = "fail", 1
		case receipt.Error:
			doc.Verdict, doc.Exit = "error", 2
			doc.Error = summary.Reason
		default:
			doc.Verdict, doc.Exit = "no verdict", 2
		}
		doc.Reason = summary.Reason
		if !report.OK() {
			status := summary.Status
			if status == receipt.Pass {
				status = receipt.Inconclusive
			}
			fail("model", status, fmt.Errorf("committed-prefix check did not pass: %s", report.Summary()))
			continue
		}
		out.set("model", receipt.Pass, "every retained committed step checked as a prefix; no settle was inferred")
	}
	return results
}

func storedLogResult(file string) StoredLogResult {
	r := StoredLogResult{File: file, Scope: "committed_prefix"}
	for _, name := range []string{"fixture-read", "app-validation", "batch-validation", "restore", "snapshots", "model"} {
		r.Checks = append(r.Checks, receipt.Check{Kind: "stored_log", Name: name, Status: receipt.NotChecked, Reason: "blocked by an earlier check"})
	}
	r.Checks = append(r.Checks, receipt.Check{Kind: "stored_log", Name: "liveness", Status: receipt.NotChecked, Reason: "retained batches carry no observed settle; processors and effects were not run"})
	return r
}

// A fold is application code. Its panic is an incomplete audit, not a model
// counterexample, and must not prevent checking the corpus's next fixture.
func storedLogSnapshots(ctx context.Context, app *grist.App, st grist.Store) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("app %s's fold/snapshot audit panicked: %v", app.Name, value)
		}
	}()
	return app.CheckSnapshots(ctx, st)
}

func (r *StoredLogResult) set(name string, status receipt.Status, reason string) {
	for i := range r.Checks {
		if r.Checks[i].Name == name {
			r.Checks[i].Status, r.Checks[i].Reason = status, reason
			return
		}
	}
}

func readStoredLog(data []byte) (Log, error) {
	var log Log
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	for {
		var batch LoggedBatch
		err := decoder.Decode(&batch)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Log{}, fmt.Errorf("batch %d: %w", len(log.Batches)+1, err)
		}
		log.Batches = append(log.Batches, batch)
	}
	if len(log.Batches) == 0 {
		return Log{}, fmt.Errorf("empty retained log: capture at least one committed batch")
	}
	return log, nil
}
