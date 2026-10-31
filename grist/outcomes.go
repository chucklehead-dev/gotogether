package grist

import (
	"context"
	"encoding/json"
	"fmt"
)

// OutcomeReader supplies the read-only store operations used by InspectOutcomes.
// Each operation observes the store independently; they do not form a snapshot.
type OutcomeReader interface {
	// Read is the typed event-page operation of store.Store.
	Read(context.Context, string, Selector, Position) ([]Stored, Position, error)
	// ReadEvent is the raw exact-position operation of store.Store.
	ReadEvent(context.Context, string, Position) (Stored, bool, error)
}

// OutcomeOptions selects a bounded page of stored failed and unknown outcomes.
type OutcomeOptions struct {
	// After excludes outcome positions through After, not older triggers.
	After Position
	// Limit defaults to 100; at most 1000 outcomes may be requested.
	Limit int
	// Processor selects its stored tag, including a processor no longer registered.
	Processor string
}

// TriggerStatus describes the identity observed at an original trigger position.
type TriggerStatus string

const (
	// TriggerRetained means the observed event has the original immutable ID.
	TriggerRetained TriggerStatus = "retained"
	// TriggerReplaced means a different immutable ID occupies the position.
	TriggerReplaced TriggerStatus = "replaced"
	// TriggerMissing means no event occupied the position when it was looked up.
	TriggerMissing TriggerStatus = "missing"
)

// OutcomeTrigger preserves original provenance separately from a later lookup.
// Status describes that lookup, not an atomic snapshot with the outcome page.
type OutcomeTrigger struct {
	Position Position      `json:"position"`
	ID       string        `json:"id"`
	Status   TriggerStatus `json:"status"`
	// ObservedID is the event found at Position, including unregistered types.
	ObservedID string `json:"observedId,omitempty"`
	// ObservedType is that event's stored type, without application decoding.
	ObservedType string `json:"observedType,omitempty"`
}

// StoredOutcome is one stored failure or unknown outcome. Failed does not imply
// that an external effect was definitely not applied, nor that retry is safe.
type StoredOutcome struct {
	ID             string         `json:"id"`
	Position       Position       `json:"position"`
	Time           int64          `json:"time"`
	Type           string         `json:"type"`
	Processor      string         `json:"processor"`
	ProcessorKnown bool           `json:"processorKnown"`
	Trigger        OutcomeTrigger `json:"trigger"`
	// EffectKey is derived from the original identity under the inspecting App.
	// It is not evidence that an external call ran or that a dependency deduplicates.
	EffectKey string `json:"effectKey"`
	// Error is present only when recorded in a grist.unknown body. Failed has no
	// stored error text; a callback or handler's steps may supply further evidence.
	Error *string `json:"error,omitempty"`
}

// OutcomeInspection is a bounded page of stored grist.failed/grist.unknown
// records, with independent raw trigger observations. It is not a reconciliation
// queue: successful, pending, crashed, lost or retired completions can be absent.
// No field is a model verdict or permission to retry or resume.
type OutcomeInspection struct {
	App         string `json:"app"`
	Tenant      string `json:"tenant"`
	Coverage    string `json:"coverage"`
	Observation string `json:"observation"`
	// Limitations states the evidence boundaries shared by JSON and text.
	Limitations []string `json:"limitations"`
	// ReadThrough is the outcome page's store read-through position. Trigger
	// lookups occur later and do not share a snapshot or durable history token.
	ReadThrough Position        `json:"readThrough"`
	After       Position        `json:"after"`
	Limit       int             `json:"limit"`
	Processor   string          `json:"processor,omitempty"`
	NextAfter   Position        `json:"nextAfter"`
	HasMore     bool            `json:"hasMore"`
	Outcomes    []StoredOutcome `json:"outcomes"`
}

// InspectOutcomes reads stored failed/unknown outcomes and the current event at
// each referenced position. It performs no writes, callbacks, retries or resume.
// Pagination and trigger observations are independent reads; a concurrent
// restore can change history between them. Missing or malformed native identity
// is an error, never inferred from today's occupant. Repeated trigger positions
// share one observation within the page.
func (a *App) InspectOutcomes(ctx context.Context, reader OutcomeReader, tenant string, opts OutcomeOptions) (*OutcomeInspection, error) {
	if tenant == "" || opts.After < 0 || opts.Limit < 0 || opts.Limit > 1000 {
		return nil, fmt.Errorf("grist: inspect outcomes: give a nonempty tenant, After >= 0, and Limit from 0 (default 100) through 1000")
	}
	if err := CheckTenant(tenant); err != nil {
		return nil, err
	}
	if opts.Limit == 0 {
		opts.Limit = 100
	}
	if err := checkText("outcome processor filter", opts.Processor); err != nil {
		return nil, err
	}
	q := Selector{Types: []string{failedType, unknownType}, After: opts.After, Limit: opts.Limit + 1}
	if opts.Processor != "" {
		q.Tags = []TagPair{{Key: "processor", Value: opts.Processor}}
	}
	events, head, err := reader.Read(ctx, tenant, q, 0)
	if err != nil {
		return nil, fmt.Errorf("grist: inspect outcomes: read tenant %s: %w", tenant, err)
	}
	report := &OutcomeInspection{App: a.Name, Tenant: tenant, Coverage: "stored_failed_unknown", Observation: "independent_reads", Limitations: []string{
		"Only stored failed/unknown outcomes are listed; absence does not prove completion or reconciliation.",
		"Outcome pages and trigger lookups are independent reads, not an atomic snapshot; a restore can change history between reads or pages.",
		"A failed outcome does not establish that an external effect was not applied or that retry is safe.",
		"Effect keys are derived under the inspecting app; they do not prove that an external call ran or a dependency deduplicates.",
	}, ReadThrough: head, After: opts.After, Limit: opts.Limit, Processor: opts.Processor, NextAfter: opts.After, Outcomes: []StoredOutcome{}}
	if len(events) > opts.Limit {
		report.HasMore = true
		events = events[:opts.Limit]
	}
	type observation struct {
		event Stored
		found bool
	}
	seen := map[Position]observation{}
	known := map[string]bool{}
	for _, p := range a.processors {
		known[p.name] = true
	}
	for _, e := range events {
		if e.Pos <= report.NextAfter || e.Pos > head || e.Tenant != tenant || !validTriggerID(e.ID) {
			return nil, fmt.Errorf("grist: inspect outcomes: invalid stored outcome envelope at tenant %s position %d: restore valid native data", tenant, e.Pos)
		}
		info := a.events[e.Type]
		if (e.Type != failedType && e.Type != unknownType) || info == nil {
			return nil, fmt.Errorf("grist: inspect outcomes: unexpected outcome type %q at position %d", e.Type, e.Pos)
		}
		if _, _, _, err := info.storedValue(e.Event, info.version); err != nil {
			return nil, fmt.Errorf("grist: inspect outcomes: tenant %s outcome %d: %w", tenant, e.Pos, err)
		}
		var native struct {
			Processor string  `json:"processor"`
			Trigger   int64   `json:"trigger"`
			TriggerID string  `json:"triggerId"`
			Error     *string `json:"error"`
		}
		if err := json.Unmarshal(e.Body, &native); err != nil {
			return nil, fmt.Errorf("grist: inspect outcomes: outcome %d: %w", e.Pos, err)
		}
		if e.Type == unknownType && native.Error == nil {
			return nil, fmt.Errorf("grist: inspect outcomes: outcome %d grist.unknown error must be a stored string (empty is allowed): restore valid native outcome data", e.Pos)
		}
		if native.Processor == "" || native.Trigger <= 0 || !validTriggerID(native.TriggerID) || !e.Tagged([]TagPair{{Key: "processor", Value: native.Processor}}) || (opts.Processor != "" && native.Processor != opts.Processor) {
			return nil, fmt.Errorf("grist: inspect outcomes: outcome %d has invalid processor/trigger provenance: restore valid native data; do not infer its original identity from the current log", e.Pos)
		}
		original := Position(native.Trigger)
		obs, cached := seen[original]
		if !cached {
			obs.event, obs.found, err = reader.ReadEvent(ctx, tenant, original)
			if err != nil {
				return nil, fmt.Errorf("grist: inspect outcomes: outcome %d trigger %d: %w", e.Pos, original, err)
			}
			if obs.found && (obs.event.Pos != original || obs.event.Tenant != tenant || !validTriggerID(obs.event.ID)) {
				return nil, fmt.Errorf("grist: inspect outcomes: invalid trigger observation at tenant %s position %d: restore valid native data", tenant, original)
			}
			seen[original] = obs
		}
		trigger := OutcomeTrigger{Position: original, ID: native.TriggerID, Status: TriggerMissing}
		if obs.found {
			trigger.ObservedID, trigger.ObservedType = obs.event.ID, obs.event.Type
			trigger.Status = TriggerReplaced
			if obs.event.ID == native.TriggerID {
				trigger.Status = TriggerRetained
			}
		}
		outcome := StoredOutcome{ID: e.ID, Position: e.Pos, Time: e.Time, Type: e.Type, Processor: native.Processor, ProcessorKnown: known[native.Processor], Trigger: trigger, EffectKey: effectKeyOf(a.Name, native.Processor, tenant, original, native.TriggerID)}
		if e.Type == unknownType {
			outcome.Error = native.Error
		}
		report.Outcomes = append(report.Outcomes, outcome)
		report.NextAfter = e.Pos
	}
	return report, nil
}
