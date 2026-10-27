package grist

import (
	"context"
	"encoding/json"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// record records a step of tenant at store: actor the tenant, process
// the store's name, whatever node or task records it (commits happen at
// the store, so a node's crash changes no step's process). Under a
// quiet context (gristsim's AtClients) it records nothing.
func record(ctx context.Context, s Store, tenant, action string, err error, kv ...any) {
	if rec.IsQuiet(ctx) {
		return
	}
	qobs.Record(ctx, action, err, append([]any{"actor", tenant, "process", s.Name()}, kv...)...)
}

// stepRow is a commit's step as its batch carries it (Batch.Step): the
// action and its fields but those the head gives (obs.head, the events'
// observations, order.pos), which a StepLog's row has. Slice 1 records
// from Batch.OnCommit; slice 2's sqlstore records from these rows.
func stepRow(action string, kv []any) json.RawMessage {
	fields := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			fields[k] = kv[i+1]
		}
	}
	b, err := json.Marshal(map[string]any{"action": action, "fields": fields})
	if err != nil {
		return nil
	}
	return b
}

// batchObs are a commit's observations: the head after it, and the
// events it appended (their types, string fields, integer fields and
// causes, in order: slice 1's binding takes scalars and lists,
// docs/grist.md §3.2). A cause is a (kind, name) pair: ("command", the
// command id) or ("processor", the processor's name), what the store
// stamps as the id or "processor:<name>".
func (a *App) batchObs(events []Event, head Position, causeKind, causeName string) []any {
	types, strs, ints, causes := []string{}, []string{}, []int64{}, [][]string{}
	for _, e := range events {
		types = append(types, e.Type)
		causes = append(causes, []string{causeKind, causeName})
		var info *eventInfo
		if a != nil {
			info = a.events[e.Type]
		}
		if info != nil {
			s, n := scalars(info.typ, e.Body)
			strs, ints = append(strs, s...), append(ints, n...)
		}
	}
	return []any{"obs.head", int64(head), "obs.events", types, "obs.strs", strs, "obs.ints", ints, "obs.causes", causes}
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
