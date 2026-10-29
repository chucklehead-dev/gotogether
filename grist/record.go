package grist

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// record records a step of tenant at store that commits nothing (a
// query, a reused key, the code's error, an effect): actor
// <app>/<tenant> and process the store's name (the App's, as every tier
// names its store), whatever node or task records it (commits
// happen at the store, so a node's crash changes no step's process). A
// commit's step is not recorded here but from its step row (Relay), with
// the same actor and process.
func record(ctx context.Context, s Store, tenant, action string, err error, kv ...any) {
	kv = append([]any{"actor", rec.Actor(s.Name(), tenant), "process", s.Name()}, kv...)
	shiftOrders(kv, orderBase(ctx, tenant)) // after a rewind, on the tenant's new clock
	qobs.Record(ctx, action, err, kv...)
}

// batchObs are a commit's observations but its head (obs.head, which
// the relay adds from the step row): the events it appended (their
// types, string fields, integer fields and causes, in order: slice 1's
// binding takes scalars and lists, docs/grist.md §3.2). A cause is a
// (kind, name) pair: ("command", the command id) or ("processor", the
// processor's name), what the store stamps as the id or
// "processor:<name>".
func (a *App) batchObs(events []Event, causeKind, causeName string) []any {
	types, strs, ints, causes := []string{}, []string{}, []int64{}, [][]string{}
	rendered := []int64{}
	for _, e := range events {
		types = append(types, e.Type)
		rendered = append(rendered, 0)
		causes = append(causes, []string{causeKind, causeName})
		var info *eventInfo
		if a != nil {
			info = a.events[e.Type]
		}
		if info != nil {
			body := e.Body
			if storedVersion(e.Version) < info.version {
				// written as an older version (WritesVersion): recorded in the
				// newest's shape, the model's, as the relay renders it
				if b, err := info.currentBody(e); err == nil {
					body = b
				}
			}
			s, n := scalars(info.typ, body)
			strs, ints = append(strs, s...), append(ints, n...)
			rendered[len(rendered)-1] = int64(info.version)
		}
	}
	kv := []any{"obs.events", types, "obs.strs", strs, "obs.ints", ints, "obs.causes", causes}
	if len(events) > 0 {
		// a commit's row: the relay reads it (renderedKey), never a step
		kv = append(kv, renderedKey, rendered)
	}
	return kv
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

// triples are a read set as slice 1 records it: a (type, tag key, tag
// value) triple per type of each selector, key "" for a type-only read.
// grist.qnt's readsOf turns them into its Read records ({ typ, tags }),
// the encoding slice 2's typed template function records directly.
func triples(qs []Selector) [][]string {
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
