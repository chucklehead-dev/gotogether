package grist

import (
	"context"

	"github.com/chucklehead-dev/gotogether/qobs"
)

type quietKey struct{}

// Quiet returns a context under which grist records no step: gristsim's
// AtClients, where the clients record the run (one binding sees every
// actor of a run, so a run records one side; docs/grist.md §3.1).
func Quiet(ctx context.Context) context.Context { return context.WithValue(ctx, quietKey{}, true) }

func quiet(ctx context.Context) bool { v, _ := ctx.Value(quietKey{}).(bool); return v }

// record records a step of tenant at store: actor the tenant, process
// the store's name, whatever node or task records it (commits happen at
// the store, so a node's crash changes no step's process).
func record(ctx context.Context, s Store, tenant, action string, err error, kv ...any) {
	if quiet(ctx) {
		return
	}
	qobs.Record(ctx, action, err, append([]any{"actor", tenant, "process", s.Name()}, kv...)...)
}

// batchObs are a commit's observations: the head after it, and the
// events it appended (their types, string fields and integer fields, in
// order: slice 1's binding takes scalars and lists, docs/grist.md §3.2).
func (a *App) batchObs(events []Event, head Position) []any {
	types, strs, ints := []string{}, []string{}, []int64{}
	for _, e := range events {
		types = append(types, e.Type)
		var info *eventInfo
		if a != nil {
			info = a.events[e.Type]
		}
		if info != nil {
			s, n := scalars(info.typ, e.Body)
			strs, ints = append(strs, s...), append(ints, n...)
		}
	}
	return []any{"obs.head", int64(head), "obs.events", types, "obs.strs", strs, "obs.ints", ints}
}

// commitOrder is a commit step's place on the tenant's domain clock
// (order.pos): 2·P for a batch ending at P; 2·H+1 for one that appended
// nothing, H the head its transaction read: after the commit of H,
// before that of H+1, with the reads through H (docs/grist.md §3.1).
func commitOrder(appended int, head Position) int64 {
	if appended > 0 {
		return 2 * int64(head)
	}
	return 2*int64(head) + 1
}

// readOrder is a step that reads through p and commits nothing: 2·p+1.
func readOrder(p Position) int64 { return 2*int64(p) + 1 }

// triples are a read set as the model takes it: a (type, tag key, tag
// value) triple per type of each query, key "" for a type-only query.
func triples(qs []Query) [][]string {
	out := [][]string{}
	for _, q := range qs {
		for _, t := range q.Types {
			k, v := "", ""
			if len(q.Tags) > 0 {
				k, v = q.Tags[0].Key, q.Tags[0].Value
			}
			out = append(out, []string{t, k, v})
		}
	}
	return out
}
