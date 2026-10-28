package grist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// An Envelope is what every command and query carries besides its
// arguments: the command id (the idempotency key, chosen by the caller),
// the tenant and the caller.
type Envelope struct {
	ID     string
	Tenant string
	Caller string
}

// Rejected is a named, expected refusal: a decision's result, recorded
// as the step's outcome (a 422 with its name at an API) and committed as
// its key's answer. Compare with errors.Is against the value Rejection
// returned (two Rejected of one name are the same refusal), or
// errors.As.
type Rejected struct{ Name string }

func (r *Rejected) Error() string { return "rejected: " + r.Name }

// Is reports whether target is a refusal of the same name: a retry
// answered from its key's marker returns a Rejected of its own.
func (r *Rejected) Is(target error) bool {
	t, ok := target.(*Rejected)
	return ok && t.Name == r.Name
}

// Rejection declares a rejection a decision may return.
func Rejection(name string) error { return &Rejected{Name: name} }

// ErrKeyReused is a command whose id names another command's outcome, or
// this command's with other arguments: a key names at most one outcome.
var ErrKeyReused = Rejection("idempotency-key-reused")

// ErrConflict is a command whose condition failed on every try: the
// state it read kept changing (a 409 at an API). It is the key's answer,
// committed like a rejection: a retry under the key gets it again.
var ErrConflict = errors.New("grist: conflict: the decision's reads changed on every try; retry later under a new id")

// ErrOutcomeUnknown is a command that ran out of transient tries after an
// append whose outcome is unknown: it may have committed. Retry it with
// the same id (Envelope.ID): its marker answers if it committed, and it
// decides again if not.
var ErrOutcomeUnknown = errors.New("grist: the command's outcome is unknown (an append may have committed): retry it with the same id")

// ErrUnavailable is a command or query that ran out of transient tries
// before any append: nothing committed. Retry it later (a command with
// the same id).
var ErrUnavailable = errors.New("grist: the store kept failing before anything committed: retry later")

// conflictOutcome is the outcome a conflict's marker and step carry.
const conflictOutcome = "conflict"

// refusal is the error a key's committed outcome answers with.
func refusal(outcome string) error {
	switch outcome {
	case "":
		return nil
	case conflictOutcome:
		return ErrConflict
	}
	return &Rejected{Name: outcome}
}

// commandInfo is a command's registration, untyped.
type commandInfo struct {
	name, quint string
	version     int
	args        reflect.Type
	opts        options
	// run decodes JSON arguments (decodeArgs) and executes them; replayed:
	// the answer is a marker's found before deciding (an earlier try's)
	run func(ctx context.Context, s Store, env Envelope, args json.RawMessage) (rc Receipt, replayed bool, err error)
}

// A Command is a struct of arguments C and a decision: reads and a
// command in, events or an error out.
type Command[C any] struct {
	app    *App
	info   *commandInfo
	decide func(*Reads, C) ([]Event, error)
}

// reservedArgs are step fields grist records with every command: a
// command's argument may not take their names. Read-only.
var reservedArgs = map[string]bool{"id": true, "caller": true, "readPos": true, "reads": true, "result": true, "trigger": true, "key": true, "ok": true}

// NewCommand registers a command: its decision is conditioned on its
// read set unless Unconditioned. Its options are Unconditioned,
// ConditionTries and TransientTries.
func NewCommand[C any](app *App, name string, decide func(r *Reads, c C) ([]Event, error), opts ...Option) *Command[C] {
	app.register("command", name)
	c := &Command[C]{app: app, decide: decide}
	q, _ := catalog.QuintName(name)
	c.info = &commandInfo{name: name, quint: q, version: 1, args: reflect.TypeFor[C](), opts: app.options("command", name, opts)}
	app.checkArgs("command", name, c.info.args)
	c.info.run = func(ctx context.Context, s Store, env Envelope, raw json.RawMessage) (Receipt, bool, error) {
		v, err := decodeArgs("command "+name, raw, c.info.args)
		if err != nil {
			return Receipt{}, false, err
		}
		return c.execute(ctx, s, env, v.Interface().(C))
	}
	app.commands[name] = c.info
	return c
}

// checkArgs notes a problem for each argument field whose name grist's
// recording takes, or whose type the model cannot hold.
func (a *App) checkArgs(kind, name string, t reflect.Type) {
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.IsExported() && reservedArgs[rec.JSONName(f)] {
			a.problems = append(a.problems, fmt.Sprintf("%s %q: field %s is named %q, which grist records with every step: rename it", kind, name, f.Name, rec.JSONName(f)))
		}
	}
	for _, p := range modelable(t, t.Name()) {
		a.problems = append(a.problems, fmt.Sprintf("%s %q: %s", kind, name, p))
	}
}

// Name is the command's name.
func (c *Command[C]) Name() string { return c.info.name }

// hash is the marker's hash of a command: its name, its version and its
// arguments' value (canonicalArgs: members sorted, zero-valued ones left
// out), so another command under the same id, or other arguments, is a
// reused key and not this one's retry, while a retry across a deploy
// that added a field (its zero then) or reordered the fields is the same
// command. A change of the arguments' meaning under the same name and
// fields is the command's version's to state.
func (c *Command[C]) hash(body []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00", c.info.name, c.info.version)
	h.Write(canonicalArgs(body))
	return hex.EncodeToString(h.Sum(nil))
}

// Execute runs the command for env's tenant. Every try begins at the
// key's marker: a key's first committed answer is its last, so a retry
// (or a late duplicate) of a command that committed gets its receipt, of
// one that was refused the refusal, and neither appends or records
// anything. Without a marker it decides on pinned reads and appends,
// conditioned on them, the events with the marker or, for a rejection,
// the marker alone (its outcome the rejection's name): a refusal is as
// final as a commit. A failed condition decides again (ConditionTries),
// then commits the conflict as the key's answer; transient store errors
// are retried apart (TransientTries, with backoff), the marker read once
// more before giving up. It returns the receipt, a *Rejected,
// ErrConflict, ErrKeyReused, ErrOutcomeUnknown (retry with the same id:
// an append whose answer was lost may have committed, a context ended
// during one included), ErrUnavailable (every transient error came
// before a commit: store.NotCommitted), or the decision's error (the
// code's fault, recorded with its text and not remembered).
//
// Arguments grist cannot store or record alike on every tier are
// ErrBadArgs before anything runs: text with a NUL byte or invalid UTF-8
// (encoding/json would turn it into U+FFFD), an integer past int64.
func (c *Command[C]) Execute(ctx context.Context, s Store, env Envelope, args C) (Receipt, error) {
	rc, _, err := c.execute(ctx, s, env, args)
	return rc, err
}

// execute is Execute, saying whether its answer is a marker found before
// deciding (an earlier try's: the API's Idempotent-Replayed).
func (c *Command[C]) execute(ctx context.Context, s Store, env Envelope, args C) (_ Receipt, replayed bool, _ error) {
	if env.ID == "" {
		return Receipt{}, false, fmt.Errorf("grist: command %s without an id: every command carries one (its idempotency key, Envelope.ID)", c.info.name)
	}
	if err := checkEnvelope(env); err != nil {
		return Receipt{}, false, fmt.Errorf("grist: command %s: %w", c.info.name, err)
	}
	if err := checkStrings(reflect.ValueOf(args), "argument"); err != nil {
		return Receipt{}, false, badArgs("command "+c.info.name, "%v", strings.TrimPrefix(err.Error(), "grist: "))
	}
	body, err := json.Marshal(args)
	if err != nil {
		return Receipt{}, false, fmt.Errorf("grist: command %s: %w", c.info.name, err)
	}
	argKV, err := rec.StepFields(c.info.args, body, "")
	if err != nil {
		return Receipt{}, false, badArgs("command "+c.info.name, "%v", err)
	}
	hash := c.hash(body)
	o := c.info.opts
	fields := func(r *Reads) []any {
		return append(append([]any{}, argKV...), "id", env.ID, "caller", env.Caller, "readPos", int64(r.pos), "reads", triples(r.sels))
	}
	// onReturn records a step that commits nothing (a reused key, the
	// code's error): it reads through r's position and appends nothing
	onReturn := func(r *Reads, outcome error, kv ...any) {
		all := append(append(fields(r), kv...), "order.pos", readOrder(r.pos))
		all = append(all, "obs.head", int64(r.pos))
		record(ctx, s, env.Tenant, c.info.quint, outcome, append(all, c.app.batchObs(nil, "command", env.ID)...)...)
	}
	answer := func(m store.Marker) (Receipt, error) {
		if m.Hash != hash {
			r := newReads(ctx, c.app, s, env)
			r.Position()
			onReturn(r, nil, "outcome", ErrKeyReused.(*Rejected).Name)
			return Receipt{}, ErrKeyReused
		}
		if err := refusal(m.Outcome); err != nil {
			return Receipt{}, err
		}
		return m.Receipt, nil
	}
	conditionTries, transient := 0, 0
	mayHaveCommitted := false
	var conflicted *Reads // the last try's reads, once the condition failed every try
	// retry counts a transient error and backs off (true), or gives up
	// with the answer: the marker's, read once more, or the error saying
	// whether an append may have committed
	retry := func(err error) (bool, Receipt, error) {
		if transient++; transient < o.transientTries {
			backoff(ctx, transient)
			return true, Receipt{}, nil
		}
		if m, ok, merr := s.Marker(ctx, env.Tenant, env.ID); merr == nil && ok {
			rc, aerr := answer(m)
			return false, rc, aerr
		}
		if mayHaveCommitted {
			return false, Receipt{}, fmt.Errorf("%w: command %s (id %s), %d transient store errors, the last: %v", ErrOutcomeUnknown, c.info.name, env.ID, transient, err)
		}
		return false, Receipt{}, fmt.Errorf("%w: command %s (id %s), %d transient store errors, the last: %v", ErrUnavailable, c.info.name, env.ID, transient, err)
	}
	for first := true; ; first = false {
		m, ok, err := s.Marker(ctx, env.Tenant, env.ID)
		if err == nil && ok {
			rc, err := answer(m)
			return rc, first, err
		}
		var r *Reads
		var events []Event
		outcome := ""   // a refusal's name: the marker's outcome
		byRule := false // the refusal is a rule's (NewRule), declared by it
		if err == nil && conflicted != nil {
			r, outcome = conflicted, conflictOutcome
		} else if err == nil {
			r = newReads(ctx, c.app, s, env)
			var derr error
			events, derr = c.decide(r, args)
			if err = r.err; err == nil {
				if r.bad != nil {
					derr = r.bad
				}
				for _, e := range events {
					if derr == nil && c.app.events[e.Type] == nil {
						derr = fmt.Errorf("grist: command %s emits %q, which is not registered on %s: register it (NewEvent)", c.info.name, e.Type, c.app.Name)
					}
				}
				if derr == nil {
					derr = checkEvents("command "+c.info.name, events)
				}
				if derr == nil {
					derr = c.app.checkAppends("command "+c.info.name, o.appends, events)
				}
				var rej *Rejected
				if derr != nil && errors.As(derr, &rej) && o.refuses != nil && !slices.Contains(o.refuses, rej.Name) {
					derr = fmt.Errorf("grist: command %s refused %s, which its Refuses does not declare (%v): add it, so its model has the outcome", c.info.name, rej.Name, o.refuses)
				}
				// the rules on what the decision appends, on its own reads:
				// their reads join the condition, a refusal is the command's
				var ruled *ruleRefusal
				if derr == nil {
					ruled, derr = c.app.applyRules(r, events)
					err = r.err // a store error: the try is retried
				}
				if derr == nil && ruled == nil && err == nil {
					derr = c.app.written("command "+c.info.name, events) // as the store keeps them: WritesVersion's
				}
				switch {
				case err != nil:
				case ruled != nil:
					outcome, events, byRule = ruled.rej.Name, nil, true
				case derr != nil && errors.As(derr, &rej):
					outcome, events = rej.Name, nil
				case derr != nil && newerIn(derr) != nil:
					// a newer release's event: nothing decided, nothing recorded
					return Receipt{}, false, &olderRelease{block: "command " + c.info.name, nv: newerIn(derr)}
				case derr != nil:
					r.Position()
					onReturn(r, derr) // the code's fault, recorded with its text, not remembered
					return Receipt{}, false, derr
				}
			}
		}
		if err != nil {
			if !store.Transient(err) {
				return Receipt{}, false, err
			}
			if again, rc, rerr := retry(err); !again {
				return rc, false, rerr
			}
			continue
		}
		r.Position()
		kv := fields(r)
		if outcome != "" {
			kv = append(kv, "outcome", outcome)
		}
		// the step is the batch's row, recorded from the store's step log
		// once committed (Relay): no hook, so a node that dies between the
		// commit and its record loses no step
		step := newRowStep(c.info.quint, len(events), append(kv, c.app.batchObs(events, "command", env.ID)...)...)
		if o.refuses == nil && outcome != "" && outcome != conflictOutcome && !byRule {
			// a refusal of a command that declares none: still a refusal
			// (remembered, answered), but its binding has no outcome for it,
			// and says so; the step's error says why and what to declare
			step.Error = fmt.Sprintf("grist: command %s refused %s and declares no refusals: declare them with grist.Refuses(...) on its NewCommand and regenerate the model, so its model and binding have the outcome", c.info.name, outcome)
		}
		b := store.Batch{Events: events, Marker: &store.Marker{ID: env.ID, Hash: hash, Outcome: outcome},
			Time: qenv.Now(ctx).UnixMilli(), Cause: env.ID, Step: encodeRow([]rowStep{step})}
		if o.unconditioned == "" && outcome != conflictOutcome {
			b.Condition = &store.Condition{After: r.pos, Selectors: r.sels}
		}
		head, err := s.Append(ctx, env.Tenant, b)
		if err == nil {
			if outcome != "" {
				return Receipt{}, false, refusal(outcome)
			}
			return store.NewReceipt(env.Tenant, head, len(events)), false, nil
		}
		var cf *store.ConditionFailed
		switch {
		case errors.As(err, &cf) && cf.Marker != "":
			// another try under this key committed first: its marker answers
		case errors.As(err, &cf):
			if conditionTries++; conditionTries >= o.conditionTries {
				conflicted = r
			}
		case store.Transient(err):
			// an error before the commit (store.NotCommitted: a lost race
			// for the head lock, a lock wait timed out) committed nothing
			mayHaveCommitted = mayHaveCommitted || store.MayHaveCommitted(err)
			if again, rc, rerr := retry(err); !again {
				return rc, false, rerr
			}
		case ctx.Err() != nil && store.MayHaveCommitted(err):
			// the context ended while the append committed: its answer is
			// lost, as a connection's would be, and the caller must retry
			// under the id to learn it
			return Receipt{}, false, fmt.Errorf("%w: command %s (id %s): %w", ErrOutcomeUnknown, c.info.name, env.ID, err)
		default:
			return Receipt{}, false, err
		}
	}
}

// backoff waits before the try-th retry of a transient error, in virtual
// time in a simulation: 5 ms doubling, at most 320 ms.
func backoff(ctx context.Context, try int) {
	d := 5 * time.Millisecond << min(try, 6)
	qenv.Sleep(ctx, d)
}

// Execute runs the command name with JSON arguments (an adapter's path:
// the API's, gristsim's clients').
func (a *App) Execute(ctx context.Context, s Store, env Envelope, name string, args json.RawMessage) (Receipt, error) {
	c := a.commands[name]
	if c == nil {
		return Receipt{}, fmt.Errorf("grist: %s has no command %q (it has %v)", a.Name, name, a.Commands())
	}
	rc, _, err := c.run(ctx, s, env, args)
	return rc, err
}

// ArgsType is the argument type of command or query name (nil if none):
// what gristsim's workload generates values of.
func (a *App) ArgsType(name string) reflect.Type {
	if c := a.commands[name]; c != nil {
		return c.args
	}
	if q := a.queries[name]; q != nil {
		return q.args
	}
	return nil
}
