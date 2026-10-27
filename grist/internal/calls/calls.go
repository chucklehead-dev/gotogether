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

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Actor is the actor of tenant's client-side steps: tenant + "/clients"
// (no tenant name holds a slash, grist.CheckTenant).
func Actor(tenant string) string { return tenant + "/clients" }

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
}

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
// JSON arguments args, under Actor(env.Tenant) at the app's store
// (process app.Name): qobs.Invoke before do (its arguments, a command's
// id), Call.Done with do's answer (a refusal as the outcome, a query's
// result's fields as observations), nothing more for a pending one. It
// returns do's answer.
func Record(ctx context.Context, app *grist.App, env grist.Envelope, name string, args json.RawMessage, do func() Answer) Answer {
	q, _ := grist.QuintName(name)
	query := IsQuery(app, name)
	// the arguments as the block decodes them (rec.StepFields decodes into
	// its type): a field the body names in another case is the field's,
	// recorded by its JSON name as the store's step records it. Arguments
	// that do not decode record none: the server refuses them (400)
	fields, _ := rec.StepFields(app.ArgsType(name), args, "")
	kv := append([]any{"actor", Actor(env.Tenant), "process", app.Name}, fields...)
	if !query {
		kv = append(kv, "id", env.ID)
	}
	call := qobs.Invoke(ctx, q, kv...)
	a := do()
	switch {
	case a.Pending:
	case a.Err != "":
		call.Done(errors.New(a.Err))
	case a.Rejected != "":
		call.Done(nil, "outcome", a.Rejected)
	case query:
		obs, err := rec.StepFields(app.ResultType(name), a.Result, "obs.")
		if err != nil {
			call.Done(fmt.Errorf("grist: query %s: its result: %w", name, err))
			break
		}
		call.Done(nil, obs...)
	default:
		call.Done(nil)
	}
	return a
}
