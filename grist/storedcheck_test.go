package grist

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

type storedCapture struct {
	store.Store
	batch store.Batch
}

func (s *storedCapture) Append(ctx context.Context, tenant string, b store.Batch) (store.Position, error) {
	p, err := s.Store.Append(ctx, tenant, b)
	if err == nil {
		s.batch = b
	}
	return p, err
}

type storedOld struct {
	Key string `json:"key" grist:"tag"`
	N   int    `json:"n"`
}

type storedNew struct {
	Key   string `json:"key" grist:"tag"`
	Count int    `json:"count"`
	Note  string `json:"note"`
}

func storedCheckFixture(t *testing.T) (*App, store.Batch) {
	t.Helper()
	a := New("retained")
	NewEvent[storedNew](a, "added", Upcast(1, func(v storedOld) storedNew {
		return storedNew{Key: v.Key, Count: v.N, Note: "new"}
	}))
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	e := Event{Type: "added", Version: 1, Tags: []TagPair{Tag("key", "k")}, Body: json.RawMessage(`{"key":"k","n":3}`)}
	// Old writer's row: observations were rendered in version 1, before the
	// grist.rendered field existed. No read model consumes this event.
	step := newRowStep("add", 1, "obs.events", []string{"added"}, "obs.strs", []string{"k"}, "obs.ints", []int64{3}, "obs.causes", [][]string{{"command", "id"}})
	return a, store.Batch{Events: []Event{e}, Cause: "id", Step: encodeRow([]rowStep{step})}
}

func editStoredStep(t *testing.T, b store.Batch, edit func(*stepRow)) store.Batch {
	t.Helper()
	var row stepRow
	if err := json.Unmarshal(b.Step, &row); err != nil {
		t.Fatal(err)
	}
	edit(&row)
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	b.Step = raw
	return b
}

func TestCheckStoredBatchSupportsStoredAndRenderedVersions(t *testing.T) {
	a, b := storedCheckFixture(t)
	if err := a.CheckStoredBatch(b); err != nil {
		t.Fatal("old writer's observations:", err)
	}
	// During a rolling update, a writer can store the old event while its
	// step observations already use the newest model shape.
	b.Step = encodeRow([]rowStep{newRowStep("add", 1, a.batchObs(b.Events, "command", "id")...)})
	if err := a.CheckStoredBatch(b); err != nil {
		t.Fatal("new writer rendering old stored version:", err)
	}
	v := a.events["added"]
	current, err := v.currentBody(b.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	b.Events[0].Version, b.Events[0].Body = 2, current
	if err := a.CheckStoredBatch(b); err != nil {
		t.Fatal("current event and observation:", err)
	}
	// Processor causes use their registered name, not the stored prefix.
	b.Cause = "processor:emit"
	b.Step = encodeRow([]rowStep{newRowStep("emit", 1, a.batchObs(b.Events, "processor", "emit")...)})
	if err := a.CheckStoredBatch(b); err != nil {
		t.Fatal("processor event:", err)
	}
}

func TestCheckStoredBatchAcceptsActualCommandCapture(t *testing.T) {
	a := New("captured")
	event := NewEvent[storedNew](a, "added", Upcast(1, func(v storedOld) storedNew {
		return storedNew{Key: v.Key, Count: v.N, Note: "new"}
	}), WritesVersion(1, func(v storedNew) storedOld { return storedOld{Key: v.Key, N: v.Count} }))
	type add struct {
		Item  string `json:"item"`
		Count int    `json:"count"`
	}
	command := NewCommand(a, "add", func(_ *Reads, value add) ([]Event, error) {
		return []Event{event.Of(storedNew{Key: value.Item, Count: value.Count, Note: "new"})}, nil
	})
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	st := &storedCapture{Store: memstore.New(a.Name)}
	if err := st.CreateTenant(context.Background(), "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := command.Execute(context.Background(), st, Envelope{Tenant: "t", ID: "c"}, add{Item: "k", Count: 3}); err != nil {
		t.Fatal(err)
	}
	if len(st.batch.Events) != 1 || st.batch.Events[0].Version != 1 {
		t.Fatal("fixture did not capture a downcast write")
	}
	if err := a.CheckStoredBatch(st.batch); err != nil {
		t.Fatal("actual command row with downcast event and current observations:", err)
	}
}

func TestCheckStoredBatchChecksCurrentUpcastEvenWhenShapesMatch(t *testing.T) {
	for _, mutation := range []string{"panic", "tag"} {
		t.Run(mutation, func(t *testing.T) {
			a, b := storedCheckFixture(t)
			a = New("actual_value")
			NewEvent[storedOld](a, "added", Upcast(1, func(v storedOld) storedOld {
				// Generated registration values do not include 3. The retained
				// fixture must exercise the actual stored value as well.
				if v.N == 3 {
					if mutation == "panic" {
						panic("bad stored value")
					}
					v.Key = "changed"
				}
				return v
			}))
			if err := a.Verify(); err != nil {
				t.Fatal(err)
			}
			err := a.CheckStoredBatch(b)
			want := "panicked"
			if mutation == "tag" {
				want = "tags"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("retained upcaster defect escaped: %v", err)
			}
		})
	}
}

func TestCheckStoredBatchKeepsIntermediateRenderedShape(t *testing.T) {
	type middle struct {
		Key   string `json:"key" grist:"tag"`
		Count int    `json:"count"`
	}
	a := New("intermediate")
	NewEvent[storedNew](a, "added",
		Upcast(1, func(v storedOld) middle { return middle{Key: v.Key, Count: 2 * v.N} }),
		Upcast(2, func(v middle) storedNew { return storedNew{Key: v.Key, Count: v.Count + 100, Note: "new"} }))
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	_, b := storedCheckFixture(t)
	b.Step = encodeRow([]rowStep{newRowStep("add", 1,
		"obs.events", []string{"added"}, "obs.strs", []string{"k"}, "obs.ints", []int64{6},
		"obs.causes", [][]string{{"command", "id"}}, renderedKey, []int64{2})})
	if err := a.CheckStoredBatch(b); err != nil {
		t.Fatal("intermediate writer's observation:", err)
	}
	b = editStoredStep(t, b, func(r *stepRow) { r.Steps[0].KV[2][1] = []int64{106} })
	if err := a.CheckStoredBatch(b); err == nil || !strings.Contains(err.Error(), "obs.ints") {
		t.Fatalf("latest shape substituted for the recorded intermediate version: %v", err)
	}
}

func TestCheckStoredBatchRejectsUnreadableEventsWithoutReadModels(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*store.Batch)
		want string
	}{
		{"unknown", func(b *store.Batch) { b.Events[0].Type = "unknown" }, "not registered"},
		{"newer", func(b *store.Batch) { b.Events[0].Version = 3 }, "version 3"},
		{"negative_version", func(b *store.Batch) { b.Events[0].Version = -1 }, "version -1"},
		{"null", func(b *store.Batch) { b.Events[0].Body = json.RawMessage(`null`) }, "JSON object"},
		{"scalar", func(b *store.Batch) { b.Events[0].Body = json.RawMessage(`"k"`) }, "JSON object"},
		{"malformed", func(b *store.Batch) { b.Events[0].Body = json.RawMessage(`{"key":`) }, "unexpected end"},
		{"type", func(b *store.Batch) { b.Events[0].Body = json.RawMessage(`{"key":"k","n":"bad"}`) }, "cannot unmarshal"},
		{"tags", func(b *store.Batch) { b.Events[0].Tags[0].Value = "other" }, "tags"},
		{"missing_tags", func(b *store.Batch) { b.Events[0].Tags = nil }, "tags"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := storedCheckFixture(t)
			c.edit(&b)
			err := a.CheckStoredBatch(b)
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "stored event 1") {
				t.Fatalf("got %v; want event context and %s", err, c.want)
			}
			if c.name == "newer" {
				var newer *NewerVersionError
				if !errors.As(err, &newer) {
					t.Fatalf("newer-version type was lost: %v", err)
				}
			}
		})
	}
}

func TestCheckStoredBatchRejectsMissingOrInconsistentStepEvidence(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*stepRow)
		want string
	}{
		{"empty_steps", func(r *stepRow) { r.Steps = nil }, "no steps"},
		{"wrong_version", func(r *stepRow) { r.V = 9 }, "version 9"},
		{"negative_count", func(r *stepRow) { r.Steps[0].Events = -1 }, "event count"},
		{"missing_events", func(r *stepRow) { r.Steps[0].Events = 0 }, "accounts for 0"},
		{"excess_events", func(r *stepRow) { r.Steps[0].Events = math.MaxInt }, "event count"},
		{"empty_action", func(r *stepRow) { r.Steps[0].Action = "" }, "no action"},
		{"bad_pair", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{"bad"}) }, "key/value"},
		{"bad_key", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{7, 1}) }, "field key"},
		{"duplicate_key", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{"obs.ints", []int{3}}) }, "field key"},
		{"relay_key", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{"obs.head", 1}) }, "field key"},
		{"actor_spoof", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{"actor", "other/tenant"}) }, "field key"},
		{"process_spoof", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{"process", "other"}) }, "field key"},
		{"thread_spoof", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{"thread", "other"}) }, "field key"},
		{"record_filter", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{"when", "error"}) }, "field key"},
		{"invocation_spoof", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{"invoked", "2026-01-01T00:00:00Z"}) }, "field key"},
		{"missing_observation", func(r *stepRow) { r.Steps[0].KV = r.Steps[0].KV[1:] }, "missing obs.events"},
		{"wrong_types", func(r *stepRow) { r.Steps[0].KV[0][1] = []string{"else"} }, "obs.events"},
		{"wrong_strings", func(r *stepRow) { r.Steps[0].KV[1][1] = []string{"else"} }, "obs.strs"},
		{"wrong_ints", func(r *stepRow) { r.Steps[0].KV[2][1] = []int{8} }, "obs.ints"},
		{"wrong_cause", func(r *stepRow) { r.Steps[0].KV[3][1] = [][]string{{"command", "other"}} }, "obs.causes"},
		{"bad_rendered_version", func(r *stepRow) { r.Steps[0].KV = append(r.Steps[0].KV, []any{renderedKey, []int{9}}) }, "registered versions"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := storedCheckFixture(t)
			b = editStoredStep(t, b, c.edit)
			if err := a.CheckStoredBatch(b); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want %s", err, c.want)
			}
		})
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{"v":`), json.RawMessage(`{"v":1,"steps":[]} {}`)} {
		a, b := storedCheckFixture(t)
		b.Step = raw
		if err := a.CheckStoredBatch(b); err == nil {
			t.Fatalf("accepted missing/malformed row %s", raw)
		}
	}
}

func TestCheckStoredBatchChecksEachStepSliceAndResume(t *testing.T) {
	a, b := storedCheckFixture(t)
	b.Events = append(b.Events, b.Events[0].Clone())
	b.Step = encodeRow([]rowStep{
		newRowStep("add", 1, a.batchObs(b.Events[:1], "command", "id")...),
		newRowStep("add", 1, a.batchObs(b.Events[1:], "command", "id")...),
	})
	if err := a.CheckStoredBatch(b); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckStoredBatch(store.Batch{Step: encodeRow([]rowStep{newRowStep("gristResumed", 0)})}); err != nil {
		t.Fatal("resume:", err)
	}
	// Outcome is recorded deliberately for command refusals and remains a
	// legitimate row field, unlike the recorder's routing/filter controls.
	b = editStoredStep(t, b, func(r *stepRow) {
		r.Steps[0].KV = append(r.Steps[0].KV, []any{"outcome", "ok"})
	})
	if err := a.CheckStoredBatch(b); err != nil {
		t.Fatal("legitimate outcome field:", err)
	}
}

func TestCheckStoredBatchRejectsWrappedUnsignedObservations(t *testing.T) {
	type unsigned struct {
		V uint64 `json:"v"`
	}
	a := New("unsigned")
	NewEvent[unsigned](a, "number")
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []uint64{math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64} {
		body, err := json.Marshal(unsigned{V: value})
		if err != nil {
			t.Fatal(err)
		}
		// event.go's projection currently wraps the unsigned value. Even an
		// otherwise consistent retained row cannot make that observation true.
		b := store.Batch{Events: []Event{{Type: "number", Version: 1, Body: body}}, Cause: "id",
			Step: encodeRow([]rowStep{newRowStep("add", 1, "obs.events", []string{"number"}, "obs.strs", []string{},
				"obs.ints", []int64{int64(value)}, "obs.causes", [][]string{{"command", "id"}})})}
		err = a.CheckStoredBatch(b)
		if value == math.MaxInt64 {
			if err != nil {
				t.Fatal("representable unsigned boundary:", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "field v") || !strings.Contains(err.Error(), "int64") {
			t.Fatalf("unsigned value %d became faithful integer evidence: %v", value, err)
		}
	}
}

func TestCheckStoredBatchRejectsUnsignedOverflowInIntermediateShape(t *testing.T) {
	type first struct {
		V int64 `json:"v"`
	}
	type middle struct {
		V uint64 `json:"v"`
	}
	type final struct {
		V int64 `json:"v"`
	}
	a := New("intermediate_unsigned")
	NewEvent[final](a, "number",
		Upcast(1, func(v first) middle {
			if v.V == 3 {
				return middle{V: math.MaxUint64}
			}
			return middle{}
		}),
		Upcast(2, func(middle) final { return final{} }))
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	b := store.Batch{Events: []Event{{Type: "number", Version: 1, Body: json.RawMessage(`{"v":3}`)}}, Cause: "id",
		Step: encodeRow([]rowStep{newRowStep("add", 1, "obs.events", []string{"number"}, "obs.strs", []string{},
			"obs.ints", []int64{3}, "obs.causes", [][]string{{"command", "id"}})})}
	if err := a.CheckStoredBatch(b); err == nil || !strings.Contains(err.Error(), "version 2 scalar observations") || !strings.Contains(err.Error(), "int64") {
		t.Fatalf("overflowing intermediate version was hidden by a representable final value: %v", err)
	}
}
