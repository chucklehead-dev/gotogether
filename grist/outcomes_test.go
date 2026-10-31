package grist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qobs"
)

type outcomeReadSpy struct {
	OutcomeReader
	beforeLookup func()
	reads        int
	lookups      []Position
}

func (s *outcomeReadSpy) Read(ctx context.Context, tenant string, q Selector, at Position) ([]Stored, Position, error) {
	s.reads++
	return s.OutcomeReader.Read(ctx, tenant, q, at)
}

func (s *outcomeReadSpy) ReadEvent(ctx context.Context, tenant string, p Position) (Stored, bool, error) {
	s.lookups = append(s.lookups, p)
	if s.beforeLookup != nil {
		hook := s.beforeLookup
		s.beforeLookup = nil
		hook()
	}
	return s.OutcomeReader.ReadEvent(ctx, tenant, p)
}

func appendOutcomeFixture(t *testing.T, s Store, tenant string, events ...Event) {
	t.Helper()
	for _, event := range events {
		cause := "fixture"
		if event.Type == failedType || event.Type == unknownType {
			var native struct {
				Processor string `json:"processor"`
			}
			if err := json.Unmarshal(event.Body, &native); err != nil {
				t.Fatal(err)
			}
			cause = "processor:" + native.Processor
		}
		if _, err := s.Append(t.Context(), tenant, store.Batch{Events: []Event{event}, Time: 1700000000000, Cause: cause}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInspectOutcomesIndependentIdentityObservations(t *testing.T) {
	for _, history := range []struct {
		name            string
		status          TriggerStatus
		replacementType string
	}{
		{"retained", TriggerRetained, ""},
		{"missing", TriggerMissing, ""},
		{"replaced-unregistered", TriggerReplaced, "retired-type"},
		{"replaced-identical", TriggerReplaced, "trigger"},
	} {
		status := history.status
		t.Run(history.name, func(t *testing.T) {
			a := New("inspection")
			type payload struct {
				Value int `json:"value"`
			}
			trigger := NewEvent[payload](a, "trigger")
			calls := 0
			NewProcessor(a, "active", trigger, func(context.Context, *Reads, payload, Meta) Result { calls++; return Skip() })
			s := memstore.New("inspection")
			if err := s.CreateTenant(t.Context(), "t"); err != nil {
				t.Fatal(err)
			}
			beforeTrigger := s.Backup()
			appendOutcomeFixture(t, s, "t", trigger.Of(payload{Value: 1}))
			original, found, err := s.ReadEvent(t.Context(), "t", 1)
			if err != nil || !found {
				t.Fatalf("original: found=%v err=%v", found, err)
			}
			appendOutcomeFixture(t, s, "t", failedEvent("active", original), unknownEvent("retired", original, "answer lost"))
			reader := &outcomeReadSpy{OutcomeReader: s}
			if status != TriggerRetained {
				reader.beforeLookup = func() {
					s.Restore(beforeTrigger)
					if status == TriggerReplaced {
						// A retired application's raw event type remains observable. No
						// catalog-based trigger selector may turn it into "missing".
						appendOutcomeFixture(t, s, "t", Event{Type: history.replacementType, Version: 1, Body: json.RawMessage(`{"value":1}`)})
					}
				}
			}
			report, err := a.InspectOutcomes(t.Context(), reader, "t", OutcomeOptions{After: 1})
			if err != nil {
				t.Fatal(err)
			}
			if report.ReadThrough != 3 || report.Coverage != "stored_failed_unknown" || report.Observation != "independent_reads" || len(report.Outcomes) != 2 || report.HasMore || report.NextAfter != 3 {
				t.Fatalf("page/observation scope: %+v", report)
			}
			if reader.reads != 1 || !reflect.DeepEqual(reader.lookups, []Position{1}) || calls != 0 {
				t.Fatalf("reads=%d lookups=%v callbacks=%d", reader.reads, reader.lookups, calls)
			}
			for i, out := range report.Outcomes {
				if out.ID == original.ID || out.Time != 1700000000000 || out.Trigger.ID != original.ID || out.Trigger.Position != 1 || out.Trigger.Status != status {
					t.Fatalf("original/outcome identities conflated: %+v", out)
				}
				wantKey := fmt.Sprintf("inspection/%s/t/1@%s", out.Processor, original.ID)
				if out.EffectKey != wantKey || out.ProcessorKnown != (i == 0) {
					t.Fatalf("processor/key: %+v", out)
				}
				if status == TriggerRetained && (out.Trigger.ObservedID != original.ID || out.Trigger.ObservedType != "trigger") {
					t.Fatalf("retained observation: %+v", out.Trigger)
				}
				if status == TriggerReplaced && (out.Trigger.ObservedID == original.ID || out.Trigger.ObservedID == "" || out.Trigger.ObservedType != history.replacementType) {
					t.Fatalf("replacement observation: %+v", out.Trigger)
				}
				if status == TriggerMissing && (out.Trigger.ObservedID != "" || out.Trigger.ObservedType != "") {
					t.Fatalf("missing observation: %+v", out.Trigger)
				}
			}
			if report.Outcomes[0].Error != nil || report.Outcomes[1].Error == nil || *report.Outcomes[1].Error != "answer lost" {
				t.Fatalf("stored error provenance: %+v", report.Outcomes)
			}
			encoded, err := json.Marshal(report)
			if err != nil || strings.Contains(string(encoded), `"error":null`) {
				t.Fatalf("error absent is not invented text: %s (%v)", encoded, err)
			}
		})
	}
}

func TestInspectOutcomesPaginationAndRetiredProcessor(t *testing.T) {
	a := New("inspection")
	s := memstore.New("inspection")
	if err := s.CreateTenant(t.Context(), "t"); err != nil {
		t.Fatal(err)
	}
	appendOutcomeFixture(t, s, "t", Event{Type: "old-trigger", Version: 1, Body: json.RawMessage(`{}`)})
	original, _, _ := s.ReadEvent(t.Context(), "t", 1)
	appendOutcomeFixture(t, s, "t", failedEvent("retired", original), unknownEvent("active", original, ""), unknownEvent("retired", original, "lost"))
	first, err := a.InspectOutcomes(t.Context(), s, "t", OutcomeOptions{Processor: "retired", Limit: 1, After: 1})
	if err != nil || len(first.Outcomes) != 1 || !first.HasMore || first.NextAfter != 2 || first.Outcomes[0].Trigger.Status != TriggerRetained || first.Outcomes[0].ProcessorKnown {
		t.Fatalf("first page: %+v (%v)", first, err)
	}
	second, err := a.InspectOutcomes(t.Context(), s, "t", OutcomeOptions{Processor: "retired", Limit: 1, After: first.NextAfter})
	if err != nil || len(second.Outcomes) != 1 || second.HasMore || second.NextAfter != 4 || second.Outcomes[0].Error == nil || *second.Outcomes[0].Error != "lost" {
		t.Fatalf("second page: %+v (%v)", second, err)
	}
	empty, err := a.InspectOutcomes(t.Context(), s, "t", OutcomeOptions{After: 4})
	if err != nil || empty.Limit != 100 || empty.NextAfter != 4 || empty.HasMore || empty.Outcomes == nil || len(empty.Outcomes) != 0 {
		t.Fatalf("empty/default page: %+v (%v)", empty, err)
	}
	emptyError, err := a.InspectOutcomes(t.Context(), s, "t", OutcomeOptions{After: 2, Limit: 1})
	if err != nil || len(emptyError.Outcomes) != 1 || emptyError.Outcomes[0].Error == nil || *emptyError.Outcomes[0].Error != "" {
		t.Fatalf("recorded empty error is distinct from no stored error: %+v (%v)", emptyError, err)
	}
	// Original positions describe a prior history and need not precede an
	// imported outcome's position or the current head.
	appendOutcomeFixture(t, s, "t", failedEvent("retired", Stored{Pos: 99, ID: original.ID}))
	missing, err := a.InspectOutcomes(t.Context(), s, "t", OutcomeOptions{After: 4})
	if err != nil || len(missing.Outcomes) != 1 || missing.Outcomes[0].Trigger.Position != 99 || missing.Outcomes[0].Trigger.Status != TriggerMissing {
		t.Fatalf("prior-history trigger: %+v (%v)", missing, err)
	}
}

type fixedOutcomeReader struct {
	outcomes           []Stored
	trigger            Stored
	readErr, lookupErr error
	reads, lookups     int
}

func (r *fixedOutcomeReader) Read(context.Context, string, Selector, Position) ([]Stored, Position, error) {
	r.reads++
	return r.outcomes, 20, r.readErr
}
func (r *fixedOutcomeReader) ReadEvent(context.Context, string, Position) (Stored, bool, error) {
	r.lookups++
	return r.trigger, true, r.lookupErr
}

func TestInspectOutcomesRefusesInvalidEvidenceWithoutPartialReport(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	a := New("inspection")
	trigger := Stored{ID: id, Tenant: "t", Pos: 1, Event: Event{Type: "retired-trigger", Version: 1, Body: json.RawMessage(`{}`)}}
	valid := Stored{ID: strings.Repeat("a", 32), Tenant: "t", Pos: 2, Cause: "processor:retired", Event: failedEvent("retired", trigger)}
	for _, broken := range []string{"missing-trigger-id", "malformed-trigger-id", "missing-unknown-error", "null-unknown-error", "missing-outcome-id", "tag-mismatch", "bad-trigger-envelope", "read-error", "lookup-error", "invalid-later-outcome"} {
		t.Run(broken, func(t *testing.T) {
			r := &fixedOutcomeReader{outcomes: []Stored{valid.Clone()}, trigger: trigger.Clone()}
			switch broken {
			case "missing-trigger-id":
				r.outcomes[0].Body = json.RawMessage(`{"processor":"retired","trigger":1}`)
			case "malformed-trigger-id":
				r.outcomes[0].Body = json.RawMessage(`{"processor":"retired","trigger":1,"triggerId":"not-an-id"}`)
			case "missing-unknown-error":
				r.outcomes[0].Event = unknownEvent("retired", trigger, "")
				r.outcomes[0].Body = json.RawMessage(`{"processor":"retired","trigger":1,"triggerId":"0123456789abcdef0123456789abcdef"}`)
			case "null-unknown-error":
				r.outcomes[0].Event = unknownEvent("retired", trigger, "")
				r.outcomes[0].Body = json.RawMessage(`{"processor":"retired","trigger":1,"triggerId":"0123456789abcdef0123456789abcdef","error":null}`)
			case "missing-outcome-id":
				r.outcomes[0].ID = ""
			case "tag-mismatch":
				r.outcomes[0].Tags = []TagPair{{Key: "processor", Value: "different"}}
			case "bad-trigger-envelope":
				r.trigger.Pos = 2
			case "read-error":
				r.readErr = errors.New("store unavailable")
			case "lookup-error":
				r.lookupErr = errors.New("lookup unavailable")
			case "invalid-later-outcome":
				bad := valid.Clone()
				bad.Pos = 3
				bad.ID = ""
				r.outcomes = append(r.outcomes, bad)
			}
			report, err := a.InspectOutcomes(t.Context(), r, "t", OutcomeOptions{})
			if report != nil || err == nil {
				t.Fatalf("invalid evidence yielded partial/trusted report: %+v (%v)", report, err)
			}
			if (broken == "missing-trigger-id" || broken == "malformed-trigger-id") && r.lookups != 0 {
				t.Fatal("looked up current occupant to guess missing identity")
			}
		})
	}
	for _, opts := range []OutcomeOptions{{After: -1}, {Limit: -1}, {Limit: 1001}} {
		r := &fixedOutcomeReader{}
		if report, err := a.InspectOutcomes(t.Context(), r, "t", opts); report != nil || err == nil || r.reads != 0 {
			t.Fatalf("invalid page options read store: %+v, %v, reads=%d", report, err, r.reads)
		}
	}
}

func TestInspectOutcomesDoesNotInventRetiredCompletion(t *testing.T) {
	type payload struct {
		Value int `json:"value"`
	}
	a := New("inspection")
	trigger := NewEvent[payload](a, "trigger")
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	p := NewProcessor(a, "deliver", trigger, func(context.Context, *Reads, payload, Meta) Result {
		return Effect(func(context.Context) error { calls.Add(1); close(entered); <-release; return errors.New("answer lost") }, AtMostOnce)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	defer rec.Close()
	ctx = qobs.WithRecorder(ctx, rec)
	s := memstore.New("inspection")
	if err := s.CreateTenant(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	appendRecorded := func(value int) {
		t.Helper()
		e := trigger.Of(payload{Value: value})
		obs, err := a.batchObs([]Event{e}, "command", "fixture")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Append(ctx, "t", store.Batch{Events: []Event{e}, Cause: "fixture", Step: encodeRow([]rowStep{newRowStep("fixture", 1, obs...)})}); err != nil {
			t.Fatal(err)
		}
	}
	appendRecorded(1)
	backup := s.Backup()
	events, head, err := s.Read(ctx, "t", Selector{Types: []string{"trigger"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := NewRelay(s)
	if err != nil {
		t.Fatal(err)
	}
	relay.RenderAs(a)
	flush := func() {
		t.Helper()
		if err := relay.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	flush()
	pl := &poller{app: a, s: s, p: p.info, tenant: "t", relay: relay, clocks: &relay.clocks, effects: new(atomic.Int64)}
	var once sync.Once
	releaseCall := func() { once.Do(func() { close(release) }) }
	defer func() {
		releaseCall()
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("callback did not finish")
		}
	}()
	go func() { defer close(done); pl.handle(withBase(ctx, "t", 0), 0, head, events) }()
	select {
	case <-entered:
	case <-done:
		t.Fatalf("handler returned without callback: events=%+v relay=%v steps=%+v", events, relay.Err(), sink.Steps())
	case <-ctx.Done():
		t.Fatal("callback did not start")
	}
	flush()
	appendRecorded(2)
	flush()
	s.Restore(backup)
	flush()
	if len(relay.Rewound()) != 1 {
		t.Fatal("restore not observed")
	}
	releaseCall()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("callback did not finish")
	}
	report, err := a.InspectOutcomes(ctx, s, "t", OutcomeOptions{})
	if err != nil || len(report.Outcomes) != 0 || report.Coverage != "stored_failed_unknown" || calls.Load() != 1 {
		t.Fatalf("retired completion invented or replayed: %+v (%v), calls=%d", report, err, calls.Load())
	}
	completions := 0
	for _, step := range sink.Steps() {
		if step.Action == "deliverEffect" {
			completions++
		}
	}
	if completions != 1 {
		t.Fatalf("truthful native completion count=%d", completions)
	}
}
