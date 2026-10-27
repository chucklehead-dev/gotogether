package grist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// An Envelope is what every command and query carries besides its
// arguments: the command id (the idempotency key, chosen by the caller),
// the tenant and the actor.
type Envelope struct {
	ID     string
	Tenant string
	Actor  string
}

// Rejected is a named, expected refusal: a decision's result, recorded
// as the step's outcome (a 422 with its name at an API). Compare with
// errors.Is against the value Rejection returned, or errors.As.
type Rejected struct{ Name string }

func (r *Rejected) Error() string { return "rejected: " + r.Name }

// Rejection declares a rejection a decision may return.
func Rejection(name string) error { return &Rejected{Name: name} }

// ErrKeyReused is a command whose id names a committed command with
// other arguments: a key names at most one committed outcome.
var ErrKeyReused = Rejection("idempotency-key-reused")

// ErrConflict is a command whose condition failed on every try: the
// state it read kept changing (a 409 at an API).
var ErrConflict = errors.New("grist: conflict: the decision's reads changed on every try; retry later")

// An Option configures a command.
type Option func(*options)

type options struct {
	unconditioned string
}

// Unconditioned appends the command's events whatever commits while it
// decides: its reads are advisory, for the reason given. It brings
// grain's unique-name race back on purpose; the model's precondition
// then judges each commit (docs/grist.md §2.4).
func Unconditioned(reason string) Option {
	return func(o *options) {
		if reason == "" {
			reason = "unstated"
		}
		o.unconditioned = reason
	}
}

// commandInfo is a command's registration, untyped.
type commandInfo struct {
	name, quint string
	args        reflect.Type
	run         func(ctx context.Context, s Store, env Envelope, args json.RawMessage) (Receipt, error)
}

// A Command is a struct of arguments C and a decision: reads and a
// command in, events or an error out.
type Command[C any] struct {
	app    *App
	info   *commandInfo
	decide func(*Reads, C) ([]Event, error)
	opts   options
}

// reservedArgs are step fields grist records with every command: a
// command's argument may not take their names. Read-only.
var reservedArgs = map[string]bool{"id": true, "readPos": true, "reads": true, "result": true, "trigger": true, "key": true, "ok": true}

// NewCommand registers a command: its decision is conditioned on its
// read set unless Unconditioned.
func NewCommand[C any](app *App, name string, decide func(r *Reads, c C) ([]Event, error), opts ...Option) *Command[C] {
	app.register("command", name)
	c := &Command[C]{app: app, decide: decide}
	for _, o := range opts {
		o(&c.opts)
	}
	q, _ := QuintName(name)
	c.info = &commandInfo{name: name, quint: q, args: reflect.TypeFor[C]()}
	app.checkArgs("command", name, c.info.args)
	c.info.run = func(ctx context.Context, s Store, env Envelope, raw json.RawMessage) (Receipt, error) {
		var args C
		if err := json.Unmarshal(raw, &args); err != nil {
			return Receipt{}, fmt.Errorf("grist: command %s: arguments %s: %w", name, raw, err)
		}
		return c.Execute(ctx, s, env, args)
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
		if f.IsExported() && reservedArgs[jsonName(f)] {
			a.problems = append(a.problems, fmt.Sprintf("%s %q: field %s is named %q, which grist records with every step: rename it", kind, name, f.Name, jsonName(f)))
		}
	}
	for _, p := range modelable(t, t.Name()) {
		a.problems = append(a.problems, fmt.Sprintf("%s %q: %s", kind, name, p))
	}
}

// Name is the command's name.
func (c *Command[C]) Name() string { return c.info.name }

// Execute runs the command for env's tenant: it checks its marker, decides
// on pinned reads and appends the events conditioned on them, deciding
// again when the condition fails (ConditionTries) and retrying transient
// store errors apart (TransientTries, with backoff). It returns the
// receipt of the commit (or of the first attempt's, found by its marker:
// a retry after a lost answer appends nothing), a *Rejected, ErrConflict,
// or the store's or the decision's error.
func (c *Command[C]) Execute(ctx context.Context, s Store, env Envelope, args C) (Receipt, error) {
	if env.ID == "" {
		return Receipt{}, fmt.Errorf("grist: command %s without an id: every command carries one (its idempotency key, Envelope.ID)", c.info.name)
	}
	body, err := json.Marshal(args)
	if err != nil {
		return Receipt{}, fmt.Errorf("grist: command %s: %w", c.info.name, err)
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	argKV := StepFields(c.info.args, body, "")
	step := func(r *Reads, outcome error, kv ...any) {
		all := append(append([]any{}, argKV...), "id", env.ID, "readPos", int64(r.pos), "reads", triples(r.queries))
		record(ctx, s, env.Tenant, c.info.quint, outcome, append(all, kv...)...)
	}
	// refused is a step that commits nothing: it reads through r's
	// position, and appends nothing there
	refused := func(r *Reads, outcome error, kv ...any) {
		step(r, outcome, append(append(kv, "order.pos", readOrder(r.pos)), c.app.batchObs(nil, r.pos)...)...)
	}
	conditionTries, transient := 0, 0
	var last *Reads
	for {
		// every try begins at the marker: a commit whose answer was lost
		// (or a retry of a committed command) is answered from it
		m, ok, err := s.Marker(ctx, env.Tenant, env.ID)
		if err == nil && ok {
			if m.Hash != hash {
				r := newReads(ctx, s, env.Tenant, env.ID)
				r.Position()
				refused(r, nil, "outcome", "idempotency-key-reused")
				return Receipt{}, ErrKeyReused
			}
			return m.Receipt, nil
		}
		r := newReads(ctx, s, env.Tenant, env.ID)
		var events []Event
		var derr error
		if err == nil {
			events, derr = c.decide(r, args)
			err = r.err
		}
		if err != nil {
			if !Transient(err) {
				return Receipt{}, err
			}
			if transient++; transient >= TransientTries {
				return Receipt{}, fmt.Errorf("grist: command %s: %d transient store errors: %w", c.info.name, transient, err)
			}
			backoff(ctx, transient)
			continue
		}
		if r.bad != nil {
			derr = r.bad
		}
		for _, e := range events {
			if derr == nil && c.app.events[e.Type] == nil {
				derr = fmt.Errorf("grist: command %s emits %q, which is not registered on %s: register it (NewEvent)", c.info.name, e.Type, c.app.Name)
			}
		}
		if derr != nil {
			r.Position()
			var rej *Rejected
			if errors.As(derr, &rej) {
				refused(r, nil, "outcome", rej.Name)
				return Receipt{}, derr
			}
			refused(r, derr) // the code's fault, recorded with its text
			return Receipt{}, derr
		}
		r.Position()
		last = r
		b := Batch{Events: events, Marker: &Marker{ID: env.ID, Hash: hash}, Time: qenv.Now(ctx).UnixMilli(), Cause: env.ID}
		if c.opts.unconditioned == "" {
			b.Condition = &Condition{After: r.pos, Queries: r.queries}
		} else {
			r.queries = nil // the condition it committed under: none (the step's read set)
		}
		b.OnCommit = func(head Position) {
			step(r, nil, append(c.app.batchObs(events, head), "order.pos", commitOrder(len(events), head))...)
		}
		head, err := s.Append(ctx, env.Tenant, b)
		if err == nil {
			return NewReceipt(env.Tenant, head, len(events)), nil
		}
		var cf *ConditionFailed
		switch {
		case errors.As(err, &cf):
			if conditionTries++; conditionTries >= ConditionTries && cf.Marker == "" {
				refused(last, nil, "outcome", "conflict")
				return Receipt{}, ErrConflict
			}
		case Transient(err):
			if transient++; transient >= TransientTries {
				return Receipt{}, fmt.Errorf("grist: command %s: %d transient store errors: %w", c.info.name, transient, err)
			}
			backoff(ctx, transient)
		default:
			return Receipt{}, err
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
	return c.run(ctx, s, env, args)
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
