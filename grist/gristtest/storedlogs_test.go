package gristtest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/schemas"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

type keptNote struct {
	Item string `json:"item" grist:"tag"`
}

type keptCount struct{ N int }

type storedLogFixture struct {
	app                    *grist.App
	binding, file          string
	log                    gristtest.Log
	folds, handlers, sends atomic.Int64
	panicFold              atomic.Bool
}

func newStoredLogFixture(t *testing.T) *storedLogFixture {
	t.Helper()
	f := &storedLogFixture{app: grist.New("kept")}
	ev := grist.NewEvent[keptNote](f.app, "noted")
	cmd := grist.NewCommand(f.app, "note", func(_ *grist.Reads, c keptNote) ([]grist.Event, error) {
		return []grist.Event{ev.Of(c)}, nil
	}, grist.Appends(ev))
	grist.NewReadModel(f.app, "count", 1, func() keptCount { return keptCount{} },
		grist.On(ev, func(s keptCount, _ keptNote, m grist.Meta) keptCount {
			f.folds.Add(1)
			// Fingerprinting also probes the fold under tenant "probe". The
			// failure here belongs to an actual retained event, not a probe.
			if m.Tenant == "t1" && f.panicFold.Swap(false) {
				panic("deliberate retained-history fold panic")
			}
			s.N++
			return s
		}))
	grist.NewProcessor(f.app, "deliver", ev, func(context.Context, *grist.Reads, keptNote, grist.Meta) grist.Result {
		f.handlers.Add(1)
		return grist.Effect(func(context.Context) error { f.sends.Add(1); return nil }, grist.AtLeastOnce)
	})
	ctx := t.Context()
	st := gristtest.Capture(memstore.New("kept"))
	if err := st.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []string{"a", "b"} {
		if _, err := cmd.Execute(ctx, st, grist.Envelope{ID: item, Tenant: "t1"}, keptNote{Item: item}); err != nil {
			t.Fatal(err)
		}
	}
	f.log = st.Log()
	dir := t.TempDir()
	f.file = filepath.Join(dir, "kept.jsonl")
	writeStoredLog(t, f.file, f.log)
	f.binding = filepath.Join(dir, "kept.binding.yaml")
	for path, contents := range map[string]string{
		f.binding:                      "spec: kept.qnt\nmodule: keptModel\nactions:\n  note:\n    expect: 'n == {{.obs.head}}'\ninvariants: [safe]\neventually: [completed]\n",
		filepath.Join(dir, "kept.qnt"): "module keptModel {\n  var n: int\n  action init = n' = 0\n  action note = n' = n + 1\n  val safe = n >= 0\n  val completed = n == 999\n}\n",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func writeStoredLog(t *testing.T, path string, log gristtest.Log) {
	t.Helper()
	if err := gristtest.WriteLog(log, path); err != nil {
		t.Fatal(err)
	}
}

func cloneStoredLog(t *testing.T, log gristtest.Log) gristtest.Log {
	t.Helper()
	data, err := json.Marshal(log)
	if err != nil {
		t.Fatal(err)
	}
	var out gristtest.Log
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func storedLogCheck(t *testing.T, result gristtest.StoredLogResult, name string) receipt.Check {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("missing %s check: %+v", name, result.Checks)
	return receipt.Check{}
}

func inspectStoredLogs(t *testing.T, app *grist.App, cfg gristtest.StoredLogsConfig) ([]gristtest.StoredLogResult, []string) {
	t.Helper()
	var results []gristtest.StoredLogResult
	msgs := run(t, func(tb testing.TB) { results = gristtest.StoredLogs(tb, app, cfg) })
	for _, result := range results {
		if result.Model == nil {
			continue
		}
		if problems, err := schemas.Check(schemas.Report(), result.Model.JSON()); err != nil || len(problems) != 0 {
			t.Fatalf("%s: runtime model report violates published schema: problems=%v err=%v\n%s", result.File, problems, err, result.Model.JSON())
		}
		for _, actor := range result.Model.Actors {
			for _, artifact := range []string{actor.Module, actor.ITF} {
				if artifact != "" {
					if _, err := os.Stat(artifact); err != nil {
						t.Fatalf("%s: replay artifact removed before the caller could inspect it: %s: %v", result.File, artifact, err)
					}
				}
			}
		}
	}
	return results, msgs
}

// Both fixtures deliberately reuse their tenant and idempotency keys. Each
// restoration must be fresh, and retained triggers must never execute effects.
func TestStoredLogsUsesFreshStoresWithoutEffects(t *testing.T) {
	qtest.RequireQuint(t)
	f := newStoredLogFixture(t)
	second := filepath.Join(t.TempDir(), "second.jsonl")
	writeStoredLog(t, second, f.log)
	results, msgs := inspectStoredLogs(t, f.app, gristtest.StoredLogsConfig{Binding: f.binding, Files: []string{f.file, second}})
	if len(msgs) != 0 || len(results) != 2 {
		t.Fatalf("valid retained corpus: results=%+v errors=%v", results, msgs)
	}
	for _, result := range results {
		for _, name := range []string{"fixture-read", "app-validation", "batch-validation", "restore", "snapshots", "model"} {
			if check := storedLogCheck(t, result, name); check.Status != receipt.Pass {
				t.Fatalf("%s: %s = %+v", result.File, name, check)
			}
		}
		if result.Scope != "committed_prefix" || result.Batches != 2 || result.Events != 2 || result.Steps != 2 || len(result.Tenants) != 1 || result.Tenants[0] != "t1" || result.Model == nil {
			t.Fatalf("wrong retained-history scope or counts: %+v", result)
		}
		if c := storedLogCheck(t, result, "liveness"); c.Status != receipt.NotChecked || c.Reason == "" {
			t.Fatalf("pending effects became a liveness claim: %+v", c)
		}
		data, err := os.ReadFile(result.File)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if result.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("input identity missing: %+v", result)
		}
		encoded, err := json.Marshal(result)
		if err != nil || !strings.Contains(string(encoded), `"committed_prefix"`) || !strings.Contains(string(encoded), `"not_checked"`) || !strings.Contains(string(encoded), `"model"`) {
			t.Fatalf("native JSON lost check scope/status: %s, %v", encoded, err)
		}
	}
	if f.folds.Load() == 0 || f.handlers.Load() != 0 || f.sends.Load() != 0 {
		t.Fatalf("folds=%d handlers=%d sends=%d: replay must fold without running effects", f.folds.Load(), f.handlers.Load(), f.sends.Load())
	}
}

// A user fold can panic on one retained history. That is an audit error, not
// a model counterexample, and must not prevent the next independent fixture.
func TestStoredLogsReportsFoldPanicAndContinues(t *testing.T) {
	qtest.RequireQuint(t)
	f := newStoredLogFixture(t)
	second := filepath.Join(t.TempDir(), "after-panic.jsonl")
	writeStoredLog(t, second, f.log)
	f.panicFold.Store(true)
	results, msgs := inspectStoredLogs(t, f.app, gristtest.StoredLogsConfig{Binding: f.binding, Files: []string{f.file, second}})
	if len(msgs) != 1 || len(results) != 2 || !strings.Contains(msgs[0], "deliberate retained-history fold panic") {
		t.Fatalf("fold panic did not become a contextual fixture error: results=%+v errors=%v", results, msgs)
	}
	if check := storedLogCheck(t, results[0], "snapshots"); check.Status != receipt.Error || !strings.Contains(check.Reason, "deliberate retained-history fold panic") {
		t.Fatalf("fold panic became snapshot agreement or model failure: %+v", check)
	}
	if results[0].Model != nil || storedLogCheck(t, results[0], "model").Status != receipt.NotChecked {
		t.Fatalf("unperformed model check became a conclusion: %+v", results[0])
	}
	if storedLogCheck(t, results[1], "snapshots").Status != receipt.Pass || storedLogCheck(t, results[1], "model").Status != receipt.Pass || results[1].Model == nil {
		t.Fatalf("fold panic prevented the next independent fixture: %+v", results[1])
	}
	if f.panicFold.Load() || f.handlers.Load() != 0 || f.sends.Load() != 0 {
		t.Fatalf("panic fixture not exercised or replay invoked effects: armed=%v handlers=%d sends=%d", f.panicFold.Load(), f.handlers.Load(), f.sends.Load())
	}
}

func TestStoredLogsRejectsIncompleteAndCorruptBatches(t *testing.T) {
	qtest.RequireQuint(t)
	f := newStoredLogFixture(t)
	for _, tc := range []struct {
		name string
		edit func(*gristtest.Log)
	}{
		{"missing_step", func(l *gristtest.Log) { l.Batches[0].Batch.Step = nil }},
		{"empty_steps", func(l *gristtest.Log) { l.Batches[0].Batch.Step = json.RawMessage(`{"v":1,"steps":[]}`) }},
		{"unknown_step_version", func(l *gristtest.Log) { l.Batches[0].Batch.Step = json.RawMessage(`{"v":999,"steps":[]}`) }},
		{"unknown_event", func(l *gristtest.Log) { l.Batches[0].Batch.Events[0].Type = "removed-event" }},
		{"newer_event", func(l *gristtest.Log) { l.Batches[0].Batch.Events[0].Version = 99 }},
		{"wrong_body_type", func(l *gristtest.Log) { l.Batches[0].Batch.Events[0].Body = json.RawMessage(`{"item":42}`) }},
		{"null_body", func(l *gristtest.Log) { l.Batches[0].Batch.Events[0].Body = json.RawMessage(`null`) }},
		{"wrong_tags", func(l *gristtest.Log) { l.Batches[0].Batch.Events[0].Tags[0].Value = "another-item" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := cloneStoredLog(t, f.log)
			tc.edit(&log)
			bad := filepath.Join(t.TempDir(), "bad.jsonl")
			writeStoredLog(t, bad, log)
			results, msgs := inspectStoredLogs(t, f.app, gristtest.StoredLogsConfig{Binding: f.binding, Files: []string{bad, f.file}})
			if len(msgs) == 0 || len(results) != 2 || !strings.Contains(strings.Join(msgs, " "), "bad.jsonl") {
				t.Fatalf("corrupt fixture not identified or stopped the corpus: results=%+v errors=%v", results, msgs)
			}
			if c := storedLogCheck(t, results[0], "batch-validation"); c.Status != receipt.Error || c.Reason == "" {
				t.Fatalf("bad batch was not an input error: %+v", c)
			}
			if c := storedLogCheck(t, results[0], "model"); c.Status != receipt.NotChecked || c.Reason == "" || results[0].Model != nil {
				t.Fatalf("missing evidence became model conformance: %+v", results[0])
			}
			if c := storedLogCheck(t, results[1], "model"); c.Status != receipt.Pass {
				t.Fatalf("bad fixture prevented independent valid fixture: %+v", c)
			}
		})
	}
}

func TestStoredLogsReportsReadAndConfigurationErrors(t *testing.T) {
	f := newStoredLogFixture(t)
	for _, data := range []string{"", "{", `{"tenant":"t1","batch":{},"typo":true}`} {
		path := filepath.Join(t.TempDir(), "invalid.jsonl")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		results, msgs := inspectStoredLogs(t, f.app, gristtest.StoredLogsConfig{Binding: f.binding, Files: []string{path}})
		if len(msgs) == 0 || len(results) != 1 || storedLogCheck(t, results[0], "fixture-read").Status != receipt.Error || storedLogCheck(t, results[0], "model").Status != receipt.NotChecked {
			t.Fatalf("invalid input became a checked model: results=%+v errors=%v", results, msgs)
		}
	}
	for _, cfg := range []gristtest.StoredLogsConfig{{Binding: f.binding}, {Files: []string{f.file}}} {
		results, msgs := inspectStoredLogs(t, f.app, cfg)
		if len(msgs) == 0 || len(results) != 0 {
			t.Fatalf("invalid corpus configuration accepted: results=%+v errors=%v", results, msgs)
		}
	}
	if results, msgs := inspectStoredLogs(t, nil, gristtest.StoredLogsConfig{Binding: f.binding, Files: []string{f.file}}); len(msgs) == 0 || len(results) != 0 {
		t.Fatalf("nil app accepted: results=%+v errors=%v", results, msgs)
	}
}

func TestStoredLogsDistinguishesModelFailureAndToolError(t *testing.T) {
	qtest.RequireQuint(t)
	f := newStoredLogFixture(t)
	badBinding := filepath.Join(filepath.Dir(f.binding), "too-strict.binding.yaml")
	data, err := os.ReadFile(f.binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badBinding, []byte(strings.Replace(string(data), "n == {{.obs.head}}", "n == {{.obs.head}} + 1", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	results, msgs := inspectStoredLogs(t, f.app, gristtest.StoredLogsConfig{Binding: badBinding, Files: []string{f.file}})
	if len(msgs) == 0 || len(results) != 1 || results[0].Model == nil || storedLogCheck(t, results[0], "model").Status != receipt.Fail {
		t.Fatalf("model contradiction was not a model failure: results=%+v errors=%v", results, msgs)
	}
	missing := filepath.Join(t.TempDir(), "absent.binding.yaml")
	results, msgs = inspectStoredLogs(t, f.app, gristtest.StoredLogsConfig{Binding: missing, Files: []string{f.file}})
	if len(msgs) == 0 || len(results) != 1 || results[0].Model != nil || storedLogCheck(t, results[0], "model").Status != receipt.Error {
		t.Fatalf("tool setup error became a model counterexample: results=%+v errors=%v", results, msgs)
	}
}

func TestStoredLogsNoVerdictRemainsUnjudged(t *testing.T) {
	qtest.RequireQuint(t)
	f := newStoredLogFixture(t)
	model := filepath.Join(filepath.Dir(f.binding), "kept.qnt")
	data, err := os.ReadFile(model)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte(strings.Replace(string(data), "action init = n' = 0", "action init = any { n' = 0, n' = 1 }", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	results, msgs := inspectStoredLogs(t, f.app, gristtest.StoredLogsConfig{Binding: f.binding, Files: []string{f.file}, Options: validate.Options{ChoiceLimit: 1}})
	if len(msgs) == 0 || len(results) != 1 || results[0].Model == nil || storedLogCheck(t, results[0], "model").Status != receipt.Inconclusive {
		t.Fatalf("exhausted search became a model claim: results=%+v errors=%v", results, msgs)
	}
	if results[0].Model.Verdict != "no verdict" || results[0].Model.Exit != 2 {
		t.Fatalf("model no-verdict status lost: %+v", results[0].Model)
	}
}

func TestStoredLogsRejectsBindingsThatOmitEvidence(t *testing.T) {
	qtest.RequireQuint(t)
	f := newStoredLogFixture(t)
	data, err := os.ReadFile(f.binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, contents := range []string{
		string(data) + "actors: [another-app/*]\n",
		strings.Replace(string(data), "  note:\n", "  note:\n    outcomes: {ok: skip}\n", 1),
		string(data) + "complete: true\n",
	} {
		path := filepath.Join(filepath.Dir(f.binding), "filtered.binding.yaml")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		results, msgs := inspectStoredLogs(t, f.app, gristtest.StoredLogsConfig{Binding: path, Files: []string{f.file}})
		if len(msgs) == 0 || len(results) != 1 || results[0].Model != nil || storedLogCheck(t, results[0], "model").Status != receipt.Error {
			t.Fatalf("filtered retained steps became model evidence: results=%+v errors=%v", results, msgs)
		}
	}
}

// Batch.Time is retained event metadata, not the original StepRow.Time.
// Restoration supplies new row timestamps, which cannot establish deadlines.
func TestStoredLogsDoesNotJudgeDeadlinesFromRestoredTimestamps(t *testing.T) {
	qtest.RequireQuint(t)
	f := newStoredLogFixture(t)
	log := cloneStoredLog(t, f.log)
	log.Batches[0].Batch.Time = 1_000
	log.Batches[1].Batch.Time = 20_000
	writeStoredLog(t, f.file, log)
	model := filepath.Join(filepath.Dir(f.binding), "kept.qnt")
	data, err := os.ReadFile(model)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte(strings.Replace(string(data), "  val safe", "  val started = n == 1\n  val finished = n == 2\n  val safe", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(f.binding)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("within:\n  - after: started\n    holds: finished\n    bound: 1s\n")...)
	if err := os.WriteFile(f.binding, data, 0o600); err != nil {
		t.Fatal(err)
	}
	results, msgs := inspectStoredLogs(t, f.app, gristtest.StoredLogsConfig{Binding: f.binding, Files: []string{f.file}})
	if len(msgs) == 0 || len(results) != 1 || results[0].Model != nil {
		t.Fatalf("restored timing became a deadline judgment: results=%+v errors=%v", results, msgs)
	}
	check := storedLogCheck(t, results[0], "model")
	if check.Status != receipt.Error || !strings.Contains(check.Reason, "timestamp") {
		t.Fatalf("missing original timestamps were not explicit: %+v", check)
	}
}
