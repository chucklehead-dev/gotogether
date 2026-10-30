package gristsim_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	workloadmeta "github.com/chucklehead-dev/gotogether/internal/workload"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func runPhasePlan(a *grist.App, cfg gristsim.Config) ([]qtrace.Step, []runprofile.Phase, error) {
	sink := &qobs.MemorySink{}
	collector := &workloadmeta.Collector{}
	ctx := workloadmeta.With(qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)), collector)
	c := qenv.NewSeeded(1)
	c.Rates = qenv.NoFaults
	err := gristsim.Run(qenv.WithContext(c, ctx), a, cfg)
	return sink.Steps(), collector.Phases(), err
}

func refusingApp() *grist.App {
	a := grist.New("notes")
	blocked := grist.Rejection("blocked")
	noted := grist.NewEvent[Noted](a, "noted")
	grist.NewCommand(a, "note", func(r *grist.Reads, c Note) ([]grist.Event, error) {
		if c.Key == "blocked" {
			return nil, blocked
		}
		return []grist.Event{noted.Of(Noted{Key: c.Key})}, nil
	}, grist.Refuses(blocked), grist.Authorize(grist.Authenticated))
	grist.NewQuery(a, "count", func(r *grist.Reads, q Note) (Count, error) {
		return Count{N: len(r.Select(grist.Selector{Types: []string{"noted"}, Tags: []grist.TagPair{grist.Tag("topic", q.Key)}}))}, nil
	}, grist.Authorize(grist.Authenticated))
	return a
}

func TestPhaseRequiredOutcomeStopsLaterWork(t *testing.T) {
	for _, http := range []bool{false, true} {
		cfg := gristsim.Config{HTTP: http, Tenants: []string{"t1"}, Clients: 1, Ops: 1, Send: []string{"note"}, Gen: map[string]func(func(int) int) any{"note": func(func(int) int) any { return Note{Key: "random"} }}, Phases: []gristsim.Phase{
			{Name: "prepare", Faults: gristsim.FaultsDisabled, Calls: []gristsim.Call{{Name: "note", Args: Note{Key: "blocked"}}}},
			{Name: "later", Faults: gristsim.FaultsConfigured, Calls: []gristsim.Call{{Name: "note", Args: Note{Key: "later"}}}},
		}}
		steps, phases, err := runPhasePlan(refusingApp(), cfg)
		var failure *gristsim.PhaseError
		if !errors.As(err, &failure) || !failure.WorkloadNotReached() {
			t.Fatalf("HTTP %t: expected prerequisite failure, got %v", http, err)
		}
		if len(phases) != 2 || phases[0].Status != "failed" || phases[1].Status != "not_started" || phases[0].Requirements[0].ObservedOutcome != "blocked" || phases[0].Requirements[0].Status != "failed" {
			t.Fatalf("HTTP %t phase observations %+v", http, phases)
		}
		for _, step := range steps {
			if step.Settled || step.Args["topic"] == "later" || step.Args["topic"] == "random" {
				t.Fatalf("HTTP %t later workload or settle executed after unmet setup: %+v", http, step)
			}
		}
		cfg.Phases[0].Calls[0].Expect = "blocked"
		_, phases, err = runPhasePlan(refusingApp(), cfg)
		if err != nil || phases[0].Status != "completed" || phases[1].Status != "completed" {
			t.Fatalf("HTTP %t explicit required refusal should permit later work: %v %+v", http, err, phases)
		}
	}
}

func TestPhaseObservationTimeoutIsBoundedAndStopsLaterWork(t *testing.T) {
	for _, http := range []bool{false, true} {
		cfg := gristsim.Config{HTTP: http, Tenants: []string{"t1"}, Clients: 1, Ops: 1, Send: []string{"note"}, Phases: []gristsim.Phase{
			{Name: "reservation", Faults: gristsim.FaultsDisabled, Timeout: 25 * time.Millisecond, Await: []gristsim.Observation{{Call: gristsim.Call{Name: "count", Args: Note{Key: "missing"}}, Result: Count{N: 1}, Poll: 10 * time.Millisecond}}},
			{Name: "cancel", Faults: gristsim.FaultsConfigured, Calls: []gristsim.Call{{Name: "note", Args: Note{Key: "later"}}}},
		}}
		steps, phases, err := runPhasePlan(app(), cfg)
		var failure *gristsim.PhaseError
		if !errors.As(err, &failure) || !strings.Contains(err.Error(), "deadline") {
			t.Fatalf("HTTP %t expected query deadline, got %v", http, err)
		}
		if !strings.Contains(err.Error(), "notes.count") || !strings.Contains(err.Error(), `required query result {"n":1}`) || !strings.Contains(err.Error(), `{"topic":"missing"}`) {
			t.Fatalf("HTTP %t timeout error hid the actual prerequisite: %v", http, err)
		}
		first := phases[0]
		r := first.Requirements[0]
		if first.Finished != "25ms" || first.Status != "failed" || phases[1].Status != "not_started" || r.Status != "timed_out" || r.Attempts < 1 {
			t.Fatalf("HTTP %t timeout observations %+v %+v", http, first, r)
		}
		if r.InFlight {
			if r.ObservedRequestID == "" && len(r.Observed) != 0 {
				t.Fatal("an unanswered first observation acquired a result")
			}
		} else if r.Attempts != 3 || r.ObservedOutcome != "ok" || string(r.Observed) != `{"n":0}` {
			t.Fatalf("completed observations lost: %+v", r)
		}
		for _, step := range steps {
			if step.Settled || step.Action == "note" {
				t.Fatalf("HTTP %t timeout still reached later call or settle: %+v", http, step)
			}
		}
	}
}

func TestPhaseObservesAsyncProgressWithoutSettling(t *testing.T) {
	for _, http := range []bool{false, true} {
		a := grist.New("async")
		started := grist.NewEvent[Noted](a, "started")
		ready := grist.NewEvent[Noted](a, "ready")
		marked := grist.NewEvent[Noted](a, "marked")
		grist.NewCommand(a, "start", func(r *grist.Reads, c Note) ([]grist.Event, error) {
			return []grist.Event{started.Of(Noted{Key: c.Key})}, nil
		}, grist.Authorize(grist.Authenticated))
		grist.NewCommand(a, "mark", func(r *grist.Reads, c Note) ([]grist.Event, error) {
			return []grist.Event{marked.Of(Noted{Key: c.Key})}, nil
		}, grist.Authorize(grist.Authenticated))
		grist.NewProcessor(a, "prepare", started, func(ctx context.Context, r *grist.Reads, e Noted, m grist.Meta) grist.Result {
			return grist.Effect(func(ctx context.Context) error { qenv.Sleep(ctx, 25*time.Millisecond); return nil }, grist.AtLeastOnce, grist.OnSuccess(ready.Of(e)))
		}, grist.Delivers(grist.AtLeastOnce, ready))
		grist.NewQuery(a, "observe-ready", func(r *grist.Reads, q Note) (Count, error) {
			return Count{N: len(r.Select(grist.Selector{Types: []string{"ready"}, Tags: []grist.TagPair{grist.Tag("topic", q.Key)}}))}, nil
		}, grist.Authorize(grist.Authenticated))
		cfg := gristsim.Config{HTTP: http, Tenants: []string{"t1"}, Clients: 1, Ops: 1, Send: []string{"observe-ready"}, Phases: []gristsim.Phase{
			{Name: "reserve", Faults: gristsim.FaultsDisabled, Timeout: time.Second, Calls: []gristsim.Call{{Name: "start", Args: Note{Key: "a"}}}, Await: []gristsim.Observation{{Call: gristsim.Call{Name: "observe-ready", Args: Note{Key: "a"}}, Result: Count{N: 1}, Poll: 10 * time.Millisecond}}},
			{Name: "confirm", Faults: gristsim.FaultsConfigured, Calls: []gristsim.Call{{Name: "mark", Args: Note{Key: "a"}}}},
		}}
		steps, phases, err := runPhasePlan(a, cfg)
		if err != nil {
			t.Fatalf("HTTP %t: %v", http, err)
		}
		if len(phases) != 2 || phases[0].Status != "completed" || phases[1].Status != "completed" || phases[0].Requirements[1].Attempts < 2 {
			t.Fatalf("HTTP %t progress observations: %+v", http, phases)
		}
		elapsed, err := time.ParseDuration(phases[0].Finished)
		if err != nil || elapsed < 25*time.Millisecond || elapsed >= time.Second {
			t.Fatalf("HTTP %t phase did not stop at observed progress: %s", http, phases[0].Finished)
		}
		observed, confirmed := -1, -1
		for i, step := range steps {
			if step.Action == "observeReady" && step.Obs["n"] == int64(1) && observed < 0 {
				observed = i
			}
			if step.Action == "mark" && confirmed < 0 {
				confirmed = i
			}
			if step.Settled && confirmed < 0 {
				t.Fatalf("HTTP %t implicit settle before phase confirmation", http)
			}
		}
		if observed < 0 || confirmed < observed {
			t.Fatalf("HTTP %t confirmation preceded observable readiness (%d,%d)", http, observed, confirmed)
		}
	}
}

func TestPhaseConfigurationRejectsUnusablePrerequisites(t *testing.T) {
	for _, phase := range []gristsim.Phase{
		{Name: "missing-policy", Calls: []gristsim.Call{{Name: "note"}}},
		{Name: "empty", Faults: gristsim.FaultsDisabled},
		{Name: "not-query", Faults: gristsim.FaultsDisabled, Await: []gristsim.Observation{{Call: gristsim.Call{Name: "note"}, Result: Count{}}}},
		{Name: "missing-result", Faults: gristsim.FaultsDisabled, Await: []gristsim.Observation{{Call: gristsim.Call{Name: "count"}}}},
		{Name: "unknown-outcome", Faults: gristsim.FaultsDisabled, Calls: []gristsim.Call{{Name: "note", Expect: "typo"}}},
		{Name: "unknown-argument-field", Faults: gristsim.FaultsDisabled, Calls: []gristsim.Call{{Name: "note", Args: json.RawMessage(`{"topci":"a"}`)}}},
		{Name: "unknown-result-field", Faults: gristsim.FaultsDisabled, Await: []gristsim.Observation{{Call: gristsim.Call{Name: "count"}, Result: json.RawMessage(`{"nn":1}`)}}},
		{Name: "blank-caller", Faults: gristsim.FaultsDisabled, Calls: []gristsim.Call{{Name: "note", Caller: "   "}}},
	} {
		steps, _, err := runPhasePlan(app(), gristsim.Config{Phases: []gristsim.Phase{phase}})
		if err == nil || len(steps) != 0 {
			t.Fatalf("invalid phase %s executed: err %v, %d native steps", phase.Name, err, len(steps))
		}
	}
}

func TestPhaseNamesDoNotChangeBusinessPrincipal(t *testing.T) {
	type owned struct {
		Owner string `json:"owner"`
	}
	for _, http := range []bool{false, true} {
		a := grist.New("ownership")
		created := grist.NewEvent[owned](a, "created")
		changed := grist.NewEvent[owned](a, "changed")
		grist.NewCommand(a, "create", func(r *grist.Reads, c struct{}) ([]grist.Event, error) {
			return []grist.Event{created.Of(owned{Owner: r.Caller()})}, nil
		}, grist.Authorize(grist.Authenticated))
		notOwner := grist.Rejection("not-owner")
		grist.NewCommand(a, "change", func(r *grist.Reads, c struct{}) ([]grist.Event, error) {
			owner := ""
			for _, e := range r.Select(grist.Selector{Types: []string{"created"}}) {
				v, _ := created.Decode(e)
				owner = v.Owner
			}
			if owner != r.Caller() {
				return nil, notOwner
			}
			return []grist.Event{changed.Of(owned{Owner: owner})}, nil
		}, grist.Refuses(notOwner), grist.Authorize(grist.Authenticated))
		grist.NewQuery(a, "owner", func(r *grist.Reads, q struct{}) (owned, error) {
			for _, e := range r.Select(grist.Selector{Types: []string{"created"}}) {
				v, _ := created.Decode(e)
				return v, nil
			}
			return owned{}, nil
		}, grist.Authorize(grist.Authenticated))
		cfg := gristsim.Config{HTTP: http, Tenants: []string{"t1"}, Clients: 1, Ops: 1, Send: []string{"owner"}, Phases: []gristsim.Phase{
			{Name: "create ownership", Faults: gristsim.FaultsDisabled, Calls: []gristsim.Call{{Name: "create", Args: struct{}{}}}},
			{Name: "modify with another display name", Faults: gristsim.FaultsDisabled, Calls: []gristsim.Call{{Name: "change", Args: struct{}{}}}},
		}}
		_, phases, err := runPhasePlan(a, cfg)
		if err != nil || phases[0].Status != "completed" || phases[1].Status != "completed" || phases[0].Requirements[0].Caller != "phase" || phases[1].Requirements[0].Caller != "phase" {
			t.Fatalf("HTTP %t display names changed principal: %v %+v", http, err, phases)
		}
		cfg.Phases[0].Calls[0].Caller = "alice"
		cfg.Phases[1].Calls[0].Caller = "bob"
		_, phases, err = runPhasePlan(a, cfg)
		var failure *gristsim.PhaseError
		if !errors.As(err, &failure) || phases[1].Requirements[0].ObservedOutcome != "not-owner" || phases[1].Requirements[0].Caller != "bob" {
			t.Fatalf("HTTP %t explicit distinct principal ignored: %v %+v", http, err, phases)
		}
	}
}

func TestPhaseUnansweredPollRetainsPreviousAnsweredObservation(t *testing.T) {
	a := grist.New("stalled")
	queries := 0
	grist.NewQuery(a, "count", func(r *grist.Reads, q struct{}) (Count, error) {
		queries++
		if queries > 1 {
			// This endpoint stops answering after its first result. Wait uses
			// its current simulation task even without an explicit context.
			qenv.Wait(context.Background(), func() bool { return false })
		}
		return Count{N: 0}, nil
	}, grist.Authorize(grist.Authenticated))
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 1, Phases: []gristsim.Phase{{Name: "ready", Faults: gristsim.FaultsDisabled, Timeout: 2 * time.Minute, Await: []gristsim.Observation{{Call: gristsim.Call{Name: "count", Args: struct{}{}}, Result: Count{N: 1}, Poll: 10 * time.Second}}}}}
	_, phases, err := runPhasePlan(a, cfg)
	var failure *gristsim.PhaseError
	if !errors.As(err, &failure) {
		t.Fatalf("expected unmet prerequisite: %v", err)
	}
	r := phases[0].Requirements[0]
	if r.Attempts != 2 || r.AttemptOutcome != "pending" || r.InFlight || r.ObservedOutcome != "ok" || string(r.Observed) != `{"n":0}` || !strings.HasSuffix(r.ObservedRequestID, "-poll-1") || !strings.HasSuffix(r.RequestID, "-poll-2") || r.Status != "timed_out" {
		t.Fatalf("unanswered query acquired or replaced an answered result: %+v", r)
	}
}

func TestPhaseQueryComparisonPreservesLargeIntegerIdentity(t *testing.T) {
	a := grist.New("large")
	grist.NewQuery(a, "number", func(r *grist.Reads, q struct{}) (struct {
		N int64 `json:"n"`
	}, error) {
		return struct {
			N int64 `json:"n"`
		}{N: 9007199254740993}, nil
	}, grist.Authorize(grist.Authenticated))
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 1, Send: []string{"number"}, Phases: []gristsim.Phase{{Name: "exact", Faults: gristsim.FaultsDisabled, Timeout: time.Millisecond, Await: []gristsim.Observation{{Call: gristsim.Call{Name: "number", Args: struct{}{}}, Result: json.RawMessage(`{"n":9007199254740992}`)}}}}}
	_, phases, err := runPhasePlan(a, cfg)
	if err == nil || phases[0].Requirements[0].Status != "timed_out" {
		t.Fatalf("distinct int64 observations collapsed via float64: %v %+v", err, phases)
	}
	cfg.Phases[0].Await[0].Result = json.RawMessage(`{"n":9007199254740993}`)
	_, phases, err = runPhasePlan(a, cfg)
	if err != nil || phases[0].Status != "completed" {
		t.Fatalf("equal large integer did not satisfy query gate: %v %+v", err, phases)
	}
}
