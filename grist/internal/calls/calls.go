// Package calls records a client's calls of a grist App's commands and
// queries, as every client does (gristsim's workload, gristsim.Do,
// grist/client's Client.Do): one recording, so a client side recorded in
// a simulation, in process or over HTTP is judged by one binding.
package calls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Actor is the actor of tenant's client-side steps in app:
// <app>/<tenant>/clients (no App or tenant name holds a slash: grist.New,
// grist.CheckTenant).
func Actor(app, tenant string) string { return rec.ClientsActor(app, tenant) }

// An Answer is a call's answer as its client saw it.
type Answer struct {
	// Result is a query's result (JSON).
	Result json.RawMessage
	// Rejected is a refusal's name: a rejection's, "conflict", "denied",
	// "idempotency-key-reused".
	Rejected string
	// Err is an error's text.
	Err string
	// Pending: never answered (every try lost, or the outcome unknown at
	// the last): the call stays pending (qtest.Completes judges it).
	Pending bool
	// Refused counts the call's tries that no server took: a connection
	// refused, a router's endpoint gone or none at all, a server shutting
	// down; each was retried. The call's step records it as its field
	// RefusedField when there were any (gristtest.NoClientErrors).
	Refused int
	// Older counts the call's tries a node of an older release refused,
	// a read meeting an event a newer one appended (a rollout's, writers
	// before readers: grist.NewerVersionError); each was retried. The
	// call's step records it as its field OlderField when there were any
	// (gristtest.ReadersFirst).
	Older int
}

// RefusedField is a client call's step field counting its refused tries
// (Answer.Refused), absent when none was: no model parameter takes it.
const RefusedField = "refused"

// OlderField is a client call's step field counting its tries an older
// release's node refused (Answer.Older), absent when none was.
const OlderField = "olderRefused"

// IsQuery reports whether name is one of app's queries.
func IsQuery(app *grist.App, name string) bool {
	for _, q := range app.Queries() {
		if q == name {
			return true
		}
	}
	return false
}

// Record records a call of app's command or query name for env, with
// JSON arguments args, under Actor(app.Name, env.Tenant) at the app's store
// (process app.Name): qobs.Invoke before do (its arguments, a command's
// id), Call.Done with do's answer (a refusal as the outcome, a query's
// result's fields as observations), nothing more for a pending one. It
// returns do's answer.
func Record(ctx context.Context, app *grist.App, env grist.Envelope, name string, args json.RawMessage, do func() Answer) Answer {
	q, _ := catalog.QuintName(name)
	query := IsQuery(app, name)
	// the arguments as the block decodes them (rec.StepFields decodes into
	// its type): a field the body names in another case is the field's,
	// recorded by its JSON name as the store's step records it. Arguments
	// that do not decode record none: the server refuses them (400)
	fields, _ := rec.StepFields(app.ArgsType(name), args, "")
	kv := append([]any{"actor", Actor(app.Name, env.Tenant), "process", app.Name}, fields...)
	if !query {
		kv = append(kv, "id", env.ID)
	}
	kv = append(kv, "caller", env.Caller) // the call's caller, as the store's step records it
	call := qobs.Invoke(ctx, q, kv...)
	a := do()
	var refused []any
	if a.Refused > 0 {
		refused = []any{RefusedField, a.Refused}
	}
	if a.Older > 0 {
		refused = append(refused, OlderField, a.Older)
	}
	switch {
	case a.Pending:
	case a.Err != "":
		call.Done(errors.New(a.Err), refused...)
	case a.Rejected != "":
		call.Done(nil, append([]any{"outcome", a.Rejected}, refused...)...)
	case query:
		obs, err := rec.StepFields(app.ResultType(name), a.Result, "obs.")
		if err != nil {
			call.Done(fmt.Errorf("grist: query %s: its result: %w", name, err), refused...)
			break
		}
		call.Done(nil, append(obs, refused...)...)
	default:
		call.Done(nil, refused...)
	}
	return a
}
