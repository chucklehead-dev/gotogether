package grist

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// queryInfo is a query's registration, untyped.
type queryInfo struct {
	name, quint string
	args        reflect.Type
	result      reflect.Type
	opts        options
	run         func(ctx context.Context, s Store, env Envelope, args json.RawMessage) (json.RawMessage, Position, error)
}

// A QueryBlock is a registered query: reads and arguments Q in, a result
// R out; it never appends.
type QueryBlock[Q, R any] struct {
	app  *App
	info *queryInfo
	f    func(*Reads, Q) (R, error)
}

// NewQuery registers a query. Its step (recorded on return) has its
// arguments, the position read through (readPos) and its result's scalar
// fields as observations, so the model checks that the answer is the
// fold of the log through that position. Its option is TransientTries.
func NewQuery[Q, R any](app *App, name string, f func(r *Reads, q Q) (R, error), opts ...Option) *QueryBlock[Q, R] {
	app.register("query", name)
	qn, _ := catalog.QuintName(name)
	b := &QueryBlock[Q, R]{app: app, info: &queryInfo{name: name, quint: qn, args: reflect.TypeFor[Q](), result: reflect.TypeFor[R](),
		opts: app.options("query", name, opts)}, f: f}
	app.checkArgs("query", name, b.info.args)
	b.info.run = func(ctx context.Context, s Store, env Envelope, raw json.RawMessage) (json.RawMessage, Position, error) {
		v, err := decodeArgs("query "+name, raw, b.info.args)
		if err != nil {
			return nil, 0, err
		}
		res, p, err := b.Run(ctx, s, env, v.Interface().(Q))
		if err != nil {
			return nil, p, err
		}
		out, err := json.Marshal(res)
		return out, p, err
	}
	app.queries[name] = b.info
	return b
}

// Name is the query's name.
func (b *QueryBlock[Q, R]) Name() string { return b.info.name }

// Run answers the query for env's tenant at its head, and records its
// step: its arguments, the position read through, its result. Transient
// store errors are retried (TransientTries, with backoff), then
// answered with ErrUnavailable. Arguments grist cannot store or record
// alike on every tier are ErrBadArgs (Execute's), a result it cannot
// record (an integer past int64) the query's error.
func (b *QueryBlock[Q, R]) Run(ctx context.Context, s Store, env Envelope, q Q) (R, Position, error) {
	var zero R
	if err := checkEnvelope(env); err != nil {
		return zero, 0, fmt.Errorf("grist: query %s: %w", b.info.name, err)
	}
	if err := checkStrings(reflect.ValueOf(q), "argument"); err != nil {
		return zero, 0, badArgs("query "+b.info.name, "%v", strings.TrimPrefix(err.Error(), "grist: "))
	}
	args, _ := json.Marshal(q)
	argKV, err := rec.StepFields(b.info.args, args, "")
	if err != nil {
		return zero, 0, badArgs("query "+b.info.name, "%v", err)
	}
	for try := 1; ; try++ {
		r := newReads(ctx, b.app, s, env)
		res, err := b.f(r, q)
		if r.err != nil && store.Transient(r.err) {
			if try < b.info.opts.transientTries {
				backoff(ctx, try)
				continue
			}
			return zero, 0, fmt.Errorf("%w: query %s, %d transient store errors, the last: %v", ErrUnavailable, b.info.name, try, r.err)
		}
		if r.err != nil {
			return zero, 0, r.err
		}
		if r.bad != nil {
			err = r.bad
		}
		if nv := newerIn(err); nv != nil {
			return zero, 0, &olderRelease{block: "query " + b.info.name, nv: nv} // not this release's to answer
		}
		r.Position()
		kv := append(append([]any{}, argKV...), "caller", env.Caller, "readPos", int64(r.pos), "order.pos", readOrder(r.pos))
		var obs []any
		if err == nil {
			body, _ := json.Marshal(res)
			if obs, err = rec.StepFields(reflect.TypeFor[R](), body, "obs."); err != nil {
				err = fmt.Errorf("grist: query %s: its result: %w", b.info.name, err)
			}
		}
		if err != nil {
			record(withBase(ctx, env.Tenant, r.base), s, env.Tenant, b.info.quint, err, kv...)
			return zero, r.pos, err
		}
		record(withBase(ctx, env.Tenant, r.base), s, env.Tenant, b.info.quint, nil, append(kv, obs...)...)
		return res, r.pos, nil
	}
}

// Query runs the query name with JSON arguments, returning its result as
// JSON and the position read through (an adapter's path).
func (a *App) Query(ctx context.Context, s Store, env Envelope, name string, args json.RawMessage) (json.RawMessage, Position, error) {
	q := a.queries[name]
	if q == nil {
		return nil, 0, fmt.Errorf("grist: %s has no query %q (it has %v)", a.Name, name, a.Queries())
	}
	return q.run(ctx, s, env, args)
}

// ResultType is query name's result type (nil if none).
func (a *App) ResultType(name string) reflect.Type {
	if q := a.queries[name]; q != nil {
		return q.result
	}
	return nil
}
