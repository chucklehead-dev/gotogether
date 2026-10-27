package gristsim

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

type note struct {
	Topic string `json:"topic" grist:"tag"`
}

// unknown commits nothing and answers every append with an outcome
// unknown.
type unknown struct{ *memstore.Store }

func (unknown) Append(context.Context, string, store.Batch) (store.Position, error) {
	return 0, errors.New("connection lost: outcome unknown")
}

// A command whose outcome is unknown when its tries run out is answered
// with Retry, which the client sends again under the same id (as for a
// lost answer), never an error the client records as the call's
// outcome.
func TestServeRetriesUnknownOutcome(t *testing.T) {
	a := grist.New("notes")
	ev := grist.NewEvent[note](a, "noted")
	grist.NewCommand(a, "note", func(r *grist.Reads, n note) ([]grist.Event, error) {
		return []grist.Event{ev.Of(n)}, nil
	}, grist.TransientTries(2))
	st := unknown{memstore.New("notes")}
	st.CreateTenant(context.Background(), "t1")
	args, _ := json.Marshal(note{Topic: "a"})
	resp := serve(context.Background(), a, st, request{Name: "note", Env: grist.Envelope{ID: "c1", Tenant: "t1"}, Args: args})
	if !resp.Retry || resp.Err != "" {
		t.Errorf("an unknown outcome: %+v, want Retry", resp)
	}
}
