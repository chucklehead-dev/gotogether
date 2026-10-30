package gristsim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/calls"
	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	workloadmeta "github.com/chucklehead-dev/gotogether/internal/workload"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

// FaultPolicy explicitly selects whether a prerequisite phase permits the
// System's configured faults. Disabling faults does not heal an existing
// partition, settle work, or establish any model completion obligation.
type FaultPolicy string

const (
	// FaultsConfigured retains the chooser's fault policy during this phase.
	FaultsConfigured FaultPolicy = "configured"
	// FaultsDisabled prevents new fault choices during this phase.
	FaultsDisabled FaultPolicy = "disabled"
)

// Phase establishes named workload prerequisites before random clients start.
// Its calls must return their required outcomes in order, then each observation
// must obtain its expected query result. A failed prerequisite stops the run;
// later phases and random clients do not execute. There is no implicit settle.
type Phase struct {
	Name string
	// Timeout is a true virtual-time budget for the entire phase (default 1m).
	Timeout time.Duration
	// Faults must explicitly select FaultsConfigured or FaultsDisabled.
	Faults FaultPolicy
	Calls  []Call
	Await  []Observation
}

// Observation repeatedly calls a query through its actual client transport
// until the complete JSON result equals Result. This is a workload gate, not
// a Go predicate standing in for an independent model assertion.
type Observation struct {
	Call Call
	// Result must be explicit; use json.RawMessage("null") to require JSON null.
	Result any
	// Poll is the virtual interval between logical observations (default 10ms).
	// Each logical call keeps the ordinary transport retries and native records.
	Poll time.Duration
}

// PhaseError means the workload could not establish a declared prerequisite.
// It is not a model counterexample or evidence that later work was exercised.
type PhaseError struct {
	Phase       string
	Requirement string
	Reason      string
	Cause       error
}

func (e *PhaseError) Error() string {
	s := fmt.Sprintf("gristsim: phase %q", e.Phase)
	if e.Requirement != "" {
		s += " prerequisite " + e.Requirement
	}
	s += ": " + e.Reason + "; later workload was not reached"
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

// Unwrap preserves a scheduler or transport cause without treating the unmet
// workload prerequisite as a model verdict.
func (e *PhaseError) Unwrap() error { return e.Cause }

// WorkloadNotReached identifies prerequisite failures to generic test drivers.
func (e *PhaseError) WorkloadNotReached() bool { return true }

type phaseAction struct {
	app      *grist.App
	call     Call
	tenant   string
	args     json.RawMessage
	draw     bool
	query    bool
	expected json.RawMessage
	poll     time.Duration
	meta     runprofile.Requirement
}

type phasePlan struct {
	phase   Phase
	actions []phaseAction
	meta    runprofile.Phase
}

func planPhases(app *grist.App, cfg Config) ([]phasePlan, error) {
	knownApps := map[*grist.App]bool{app: true}
	for _, a := range cfg.Peers {
		knownApps[a] = true
	}
	names := map[string]bool{}
	plans := make([]phasePlan, 0, len(cfg.Phases))
	for pi, phase := range cfg.Phases {
		prefix := fmt.Sprintf("gristsim: Config.Phases[%d]", pi)
		if strings.TrimSpace(phase.Name) == "" || names[phase.Name] {
			return nil, fmt.Errorf("%s: give every phase a nonempty unique Name", prefix)
		}
		names[phase.Name] = true
		if phase.Timeout < 0 {
			return nil, fmt.Errorf("%s (%s): Timeout must be nonnegative", prefix, phase.Name)
		}
		if phase.Timeout == 0 {
			phase.Timeout = time.Minute
		}
		if phase.Faults != FaultsConfigured && phase.Faults != FaultsDisabled {
			return nil, fmt.Errorf("%s (%s): Faults must explicitly select FaultsConfigured or FaultsDisabled", prefix, phase.Name)
		}
		if len(phase.Calls) == 0 && len(phase.Await) == 0 {
			return nil, fmt.Errorf("%s (%s): an empty phase establishes no prerequisite; add Calls or Await", prefix, phase.Name)
		}
		plan := phasePlan{phase: phase, meta: runprofile.Phase{Name: phase.Name, Status: "not_started", Faults: string(phase.Faults), Timeout: phase.Timeout.String(), Requirements: []runprofile.Requirement{}}}
		add := func(c Call, expected any, poll time.Duration, kind string, index int) error {
			target := c.App
			if target == nil {
				target = app
			}
			if !knownApps[target] {
				return fmt.Errorf("%s (%s): %s %d targets an App not run by this composition; use the root App or an actual Config.Peers App", prefix, phase.Name, kind, index)
			}
			blocks := append(target.Commands(), target.Queries()...)
			if !slices.Contains(blocks, c.Name) {
				return fmt.Errorf("%s (%s): %q is no command or query of %s (its blocks: %v)", prefix, phase.Name, c.Name, target.Name, blocks)
			}
			query := isQuery(target, c.Name)
			if c.Caller == "" {
				c.Caller = "phase"
			}
			if strings.TrimSpace(c.Caller) != c.Caller || strings.ContainsAny(c.Caller, "\r\n") {
				return fmt.Errorf("%s (%s): %s %d Caller must be a nonblank principal without surrounding whitespace or line breaks", prefix, phase.Name, kind, index)
			}
			if c.Expect == "" {
				c.Expect = "ok"
			}
			if !knownOutcome(target, c.Name, c.Expect) {
				return fmt.Errorf("%s (%s): %s %d expects unknown outcome %q of %s.%s; use ok, a framework refusal, or a declared command refusal", prefix, phase.Name, kind, index, c.Expect, target.Name, c.Name)
			}
			var goal json.RawMessage
			if kind == "query_result" {
				if !query {
					return fmt.Errorf("%s (%s): Await[%d] names command %s; observe a query result", prefix, phase.Name, index, c.Name)
				}
				if c.Expect != "ok" {
					return fmt.Errorf("%s (%s): Await[%d] must require an answered query (Expect ok)", prefix, phase.Name, index)
				}
				if expected == nil {
					return fmt.Errorf("%s (%s): Await[%d] requires an explicit Result; use json.RawMessage(\"null\") for JSON null", prefix, phase.Name, index)
				}
				var err error
				goal, err = jsonValue(expected)
				if err != nil {
					return fmt.Errorf("%s (%s): Await[%d] Result: %w", prefix, phase.Name, index, err)
				}
				if _, err := rec.StepFields(target.ResultType(c.Name), goal, ""); err != nil {
					return fmt.Errorf("%s (%s): Await[%d] Result does not decode as %s's result: %w", prefix, phase.Name, index, c.Name, err)
				}
				if err := strictValue(target.ResultType(c.Name), goal); err != nil {
					return fmt.Errorf("%s (%s): Await[%d] Result: %w", prefix, phase.Name, index, err)
				}
				if poll < 0 {
					return fmt.Errorf("%s (%s): Await[%d] Poll must be nonnegative", prefix, phase.Name, index)
				}
				if poll == 0 {
					poll = 10 * time.Millisecond
				}
			}
			var args json.RawMessage
			if c.Args != nil {
				var err error
				args, err = jsonValue(c.Args)
				if err != nil {
					return fmt.Errorf("%s (%s): %s %d (%s) arguments: %w", prefix, phase.Name, kind, index, c.Name, err)
				}
				if _, err := rec.StepFields(target.ArgsType(c.Name), args, ""); err != nil {
					return fmt.Errorf("%s (%s): %s %d (%s) arguments: %w", prefix, phase.Name, kind, index, c.Name, err)
				}
				if err := strictValue(target.ArgsType(c.Name), args); err != nil {
					return fmt.Errorf("%s (%s): %s %d (%s) arguments: %w", prefix, phase.Name, kind, index, c.Name, err)
				}
			}
			tenants := cfg.Tenants
			if c.Tenant != "" {
				if !slices.Contains(tenants, c.Tenant) {
					return fmt.Errorf("%s (%s): tenant %q is not one of Config.Tenants %v", prefix, phase.Name, c.Tenant, tenants)
				}
				tenants = []string{c.Tenant}
			}
			for _, tenant := range tenants {
				label := fmt.Sprintf("%s-%d/%s", kind, index+1, tenant)
				id := fmt.Sprintf("%s-phase-%d-%s-%d", tenant, pi+1, kind, index+1)
				meta := runprofile.Requirement{Name: label, Kind: kind, App: target.Name, Tenant: tenant, Caller: c.Caller, Action: c.Name, RequestID: id, Status: "not_started", ExpectedOutcome: c.Expect, Arguments: append(json.RawMessage(nil), args...), Expected: append(json.RawMessage(nil), goal...)}
				plan.actions = append(plan.actions, phaseAction{app: target, call: c, tenant: tenant, args: args, draw: c.Args == nil, query: query, expected: goal, poll: poll, meta: meta})
				plan.meta.Requirements = append(plan.meta.Requirements, meta)
			}
			return nil
		}
		for i, c := range phase.Calls {
			if err := add(c, nil, 0, "call_outcome", i); err != nil {
				return nil, err
			}
		}
		for i, o := range phase.Await {
			if err := add(o.Call, o.Result, o.Poll, "query_result", i); err != nil {
				return nil, err
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func knownOutcome(app *grist.App, name, outcome string) bool {
	if slices.Contains([]string{"ok", "conflict", "denied", "idempotency-key-reused"}, outcome) {
		return true
	}
	for _, b := range app.Catalog().Commands {
		if b.Name == name {
			return slices.Contains(b.Refuses, outcome)
		}
	}
	return false
}

func jsonValue(v any) (json.RawMessage, error) {
	var b []byte
	switch v := v.(type) {
	case json.RawMessage:
		b = append([]byte(nil), v...)
	case []byte:
		b = append([]byte(nil), v...)
	default:
		var err error
		b, err = json.Marshal(v)
		if err != nil {
			return nil, err
		}
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("value is not valid JSON")
	}
	return b, nil
}

// A misspelled JSON field is a configuration error, not a query condition to
// poll until its deadline. Decode with the actual API type before any call.
func strictValue(typ reflect.Type, data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(reflect.New(typ).Interface()); err != nil {
		return err
	}
	var rest any
	if err := d.Decode(&rest); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}

func sameJSON(a, b json.RawMessage) bool {
	decode := func(data []byte) (any, error) {
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		var v any
		err := d.Decode(&v)
		return v, err
	}
	x, e := decode(a)
	if e != nil {
		return false
	}
	y, e := decode(b)
	if e != nil {
		return false
	}
	var equal func(any, any) bool
	equal = func(a, b any) bool {
		switch a := a.(type) {
		case json.Number:
			v, ok := b.(json.Number)
			if !ok {
				return false
			}
			x, ok := new(big.Rat).SetString(string(a))
			if !ok {
				return false
			}
			y, ok := new(big.Rat).SetString(string(v))
			return ok && x.Cmp(y) == 0
		case []any:
			v, ok := b.([]any)
			if !ok || len(a) != len(v) {
				return false
			}
			for i := range a {
				if !equal(a[i], v[i]) {
					return false
				}
			}
			return true
		case map[string]any:
			v, ok := b.(map[string]any)
			if !ok || len(a) != len(v) {
				return false
			}
			for k, x := range a {
				y, ok := v[k]
				if !ok || !equal(x, y) {
					return false
				}
			}
			return true
		default:
			return reflect.DeepEqual(a, b)
		}
	}
	return equal(x, y)
}

func outcome(a calls.Answer) string {
	switch {
	case a.Pending:
		return "pending"
	case a.Err != "":
		return "error"
	case a.Rejected != "":
		return a.Rejected
	default:
		return "ok"
	}
}

func (w *workload) runPhases(plans []phasePlan) error {
	if len(plans) == 0 {
		return nil
	}
	metas := make([]runprofile.Phase, len(plans))
	for i, p := range plans {
		metas[i] = p.meta
	}
	workloadmeta.Define(w.s.Context(), metas)
	for pi, plan := range plans {
		if plan.phase.Faults == FaultsDisabled {
			w.s.PauseFaults()
		} else {
			w.s.ResumeFaults()
		}
		meta := plan.meta.Clone()
		start := w.s.Elapsed()
		meta.Status = "running"
		meta.Started = start.String()
		meta.Deadline = (start + plan.phase.Timeout).String()
		workloadmeta.Update(w.s.Context(), pi, meta)
		done := false
		var failed *PhaseError
		active := -1
		w.s.Spawn("phase:"+plan.phase.Name, func(ctx context.Context) {
			ctx = qobs.Thread(ctx)
			for ai, action := range plan.actions {
				active = ai
				r := &meta.Requirements[ai]
				r.Status = "running"
				args := action.args
				if action.draw {
					args = w.genFor(action.app, action.call.Name)
				}
				r.Arguments = append(json.RawMessage(nil), args...)
				for {
					r.Attempts++
					id := action.meta.RequestID
					if action.poll > 0 {
						id = fmt.Sprintf("%s-poll-%d", id, r.Attempts)
					}
					r.RequestID = id
					r.InFlight = true
					r.AttemptOutcome = "in_flight"
					workloadmeta.Update(w.s.Context(), pi, meta)
					req := request{Query: action.query, Name: action.call.Name, Args: args, Env: grist.Envelope{ID: id, Tenant: action.tenant, Caller: action.call.Caller}}
					a := w.callApp(ctx, action.app, req)
					r.InFlight = false
					r.AttemptOutcome = outcome(a)
					if !a.Pending {
						r.ObservedRequestID = id
						r.ObservedOutcome = r.AttemptOutcome
						r.Observed = append(json.RawMessage(nil), a.Result...)
					}
					if action.poll == 0 {
						if r.AttemptOutcome != action.call.Expect {
							r.Status = "failed"
							r.Reason = fmt.Sprintf("required outcome %q, observed %q", action.call.Expect, r.AttemptOutcome)
							if a.Err != "" {
								r.Reason += ": " + a.Err
							}
							failed = &PhaseError{Phase: plan.phase.Name, Requirement: r.Name, Reason: fmt.Sprintf("%s.%s tenant %s: %s", action.app.Name, action.call.Name, action.tenant, r.Reason)}
						} else {
							r.Status = "satisfied"
						}
						workloadmeta.Update(w.s.Context(), pi, meta)
						break
					}
					if r.AttemptOutcome == "ok" && sameJSON(a.Result, action.expected) {
						r.Status = "satisfied"
						workloadmeta.Update(w.s.Context(), pi, meta)
						break
					}
					workloadmeta.Update(w.s.Context(), pi, meta)
					qenv.Sleep(ctx, action.poll)
				}
				if failed != nil {
					break
				}
			}
			done = true
		})
		reached, err := w.s.RunUntil(plan.phase.Timeout, func() bool { return done })
		meta.Finished = w.s.Elapsed().String()
		if err != nil || !reached {
			reason := fmt.Sprintf("phase deadline %s reached before prerequisites were satisfied", meta.Deadline)
			if err != nil {
				reason = "simulation stopped before prerequisites were satisfied"
			}
			name := ""
			if active >= 0 {
				r := &meta.Requirements[active]
				r.Status = "timed_out"
				if err != nil {
					r.Status = "failed"
				}
				wanted := "outcome " + r.ExpectedOutcome
				if r.Kind == "query_result" {
					wanted = "query result " + string(r.Expected)
				}
				observed := "no completed logical call"
				if r.ObservedOutcome != "" {
					observed = fmt.Sprintf("last observed outcome %q", r.ObservedOutcome)
					if len(r.Observed) > 0 {
						observed += " with result " + string(r.Observed)
					}
				}
				if r.InFlight {
					observed += "; request " + r.RequestID + " is still in flight"
				} else if r.AttemptOutcome != r.ObservedOutcome {
					observed += "; latest logical call outcome " + r.AttemptOutcome
				}
				reason = fmt.Sprintf("%s.%s tenant %s arguments %s: %s; required %s; %s", r.App, r.Action, r.Tenant, r.Arguments, reason, wanted, observed)
				r.Reason = reason
				name = r.Name
			}
			failed = &PhaseError{Phase: plan.phase.Name, Requirement: name, Reason: reason, Cause: err}
		}
		if failed != nil {
			meta.Status = "failed"
			meta.Reason = failed.Reason
			workloadmeta.Update(w.s.Context(), pi, meta)
			return failed
		}
		meta.Status = "completed"
		workloadmeta.Update(w.s.Context(), pi, meta)
	}
	// The ordinary randomized workload uses its configured chooser policy.
	w.s.ResumeFaults()
	return nil
}
