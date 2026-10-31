package gristsim_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// NotedBy is noted's version 2: who noted it ("anyone" for version 1's).
type NotedBy struct {
	Key string `json:"topic" grist:"tag"`
	By  string `json:"by"`
}

// notes is the notes app at release 1 (noted at version 1) or 2 (version
// 2, writing version 1 if held), its count query folding the topic's
// events (a read that decodes them).
func notes(release int, held bool) *grist.App {
	a := grist.New("notes")
	count := func(n func(r *grist.Reads, topic string) int) {
		grist.NewQuery(a, "count", func(r *grist.Reads, q Note) (Count, error) { return Count{N: n(r, q.Key)}, nil }, grist.Authorize(grist.Authenticated))
	}
	if release == 1 {
		noted := grist.NewEvent[Noted](a, "noted")
		grist.NewCommand(a, "note", func(r *grist.Reads, c Note) ([]grist.Event, error) {
			grist.Events(r, noted, grist.Tag("topic", c.Key))
			return []grist.Event{noted.Of(Noted{Key: c.Key})}, nil
		}, grist.Authorize(grist.Authenticated))
		count(func(r *grist.Reads, topic string) int { return len(grist.Events(r, noted, grist.Tag("topic", topic))) })
		return a
	}
	opts := []grist.EventOption{grist.Upcast(1, func(v Noted) NotedBy { return NotedBy{Key: v.Key, By: "anyone"} })}
	if held {
		opts = append(opts, grist.WritesVersion(1, func(v NotedBy) Noted { return Noted{Key: v.Key} }))
	}
	noted := grist.NewEvent[NotedBy](a, "noted", opts...)
	grist.NewCommand(a, "note", func(r *grist.Reads, c Note) ([]grist.Event, error) {
		grist.Events(r, noted, grist.Tag("topic", c.Key))
		return []grist.Event{noted.Of(NotedBy{Key: c.Key, By: "anyone"})}, nil
	}, grist.Authorize(grist.Authenticated))
	count(func(r *grist.Reads, topic string) int { return len(grist.Events(r, noted, grist.Tag("topic", topic))) })
	return a
}

// rollout runs seed of a rolling update from release 1 to to, returning
// its steps.
func rollout(t *testing.T, seed uint64, to *grist.App) []qtrace.Step {
	t.Helper()
	sink := &qobs.MemorySink{}
	c := qenv.WithContext(qenv.NewSeeded(seed), qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	cfg := gristsim.Config{Check: true, Tenants: []string{"t1"}, Clients: 2, Ops: 10, Send: []string{"note", "count"}, Alphabet: []string{"a"}, Nodes: 2,
		From: notes(1, false), Cluster: &gristsim.Cluster{Rolling: true}}
	if err := gristsim.Run(c, to, cfg); err != nil {
		t.Fatal(err)
	}
	return sink.Steps()
}

// olderRefused counts the client calls a node of release 1 refused a try
// of, and the commits relayed in another shape than release 2's (a
// noted with no by).
func olderRefused(steps []qtrace.Step) (refused, shapes int) {
	for _, s := range steps {
		if _, ok := s.Args["olderRefused"]; ok {
			refused++
		}
		if s.Action == "note" && s.Obs["strs"] != nil && s.Obs["strs"] != `["a","anyone"]` {
			shapes++
		}
	}
	return
}

// Force the overlap a random rolling schedule may never reach: a newer
// release commits first, then the older release must read that exact event
// before it can answer a query or decide a command. Refusal is retriable,
// never a committed domain rejection that poisons the command's id.
func TestRolloutReaderCompatibility(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "eager"
		if held {
			name = "held"
		}
		t.Run(name, func(t *testing.T) {
			older, newer := notes(1, false), notes(2, held)
			for _, app := range []*grist.App{older, newer} {
				if err := app.Verify(); err != nil {
					t.Fatal(err)
				}
			}
			sink := &qobs.MemorySink{}
			recorder := qobs.NewRecorder(sink, qobs.WithID("rollout-reader-"+name))
			t.Cleanup(func() {
				if err := recorder.Close(); err != nil {
					t.Error(err)
				}
			})
			ctx := qobs.WithRecorder(t.Context(), recorder)
			st := memstore.New("notes")
			if err := st.CreateTenant(ctx, "t1"); err != nil {
				t.Fatal(err)
			}
			args := json.RawMessage(`{"topic":"a"}`)
			if _, err := newer.Execute(ctx, st, grist.Envelope{ID: "new-first", Tenant: "t1", Caller: "u"}, "note", args); err != nil {
				t.Fatal(err)
			}
			stored, head, err := st.Read(ctx, "t1", grist.Selector{Types: []string{"noted"}}, 0)
			version := 2
			if held {
				version = 1
			}
			if err != nil || head != 1 || len(stored) != 1 || stored[0].Version != version {
				t.Fatalf("newer-writer prerequisite not reached: events=%+v head=%d err=%v, want one version %d event", stored, head, err, version)
			}
			answer, _, queryErr := older.Query(ctx, st, grist.Envelope{ID: "old-count", Tenant: "t1", Caller: "u"}, "count", args)
			_, commandErr := older.Execute(ctx, st, grist.Envelope{ID: "old-note", Tenant: "t1", Caller: "u"}, "note", args)
			if held {
				var count Count
				if queryErr != nil || json.Unmarshal(answer, &count) != nil || count.N != 1 || commandErr != nil {
					t.Fatalf("older reader refused held version 1: answer=%s query=%v command=%v", answer, queryErr, commandErr)
				}
			} else {
				for operation, err := range map[string]error{"query": queryErr, "command": commandErr} {
					var newerVersion *grist.NewerVersionError
					if !errors.As(err, &newerVersion) || !errors.Is(err, grist.ErrUnavailable) || newerVersion.Type != "noted" || newerVersion.Version != 2 || newerVersion.Newest != 1 {
						t.Errorf("older %s answered a newer event: %v, want typed version-2 refusal (reader supports version 1)", operation, err)
					}
				}
				remaining, head, err := st.Read(ctx, "t1", grist.Selector{Types: []string{"noted"}}, 0)
				if err != nil || head != 1 || len(remaining) != 1 || remaining[0].ID != stored[0].ID {
					t.Fatalf("older refusal changed the committed history: %+v head=%d (%v)", remaining, head, err)
				}
				if marker, exists, err := st.Marker(ctx, "t1", "old-note"); err != nil || exists {
					t.Fatalf("older refusal committed a final domain answer: marker=%+v exists=%v (%v)", marker, exists, err)
				}
				if _, err := newer.Execute(ctx, st, grist.Envelope{ID: "old-note", Tenant: "t1", Caller: "u"}, "note", args); err != nil {
					t.Fatalf("newer release could not retry the refused command's id: %v", err)
				}
			}
			answer, _, err = newer.Query(ctx, st, grist.Envelope{ID: "new-count", Tenant: "t1", Caller: "u"}, "count", args)
			var count Count
			if err != nil || json.Unmarshal(answer, &count) != nil || count.N != 2 {
				t.Fatalf("two accepted commands did not remain readable: answer=%s err=%v", answer, err)
			}
		})
	}
}

// Keep broad rolling-update schedules alongside the forced reader-overlap
// witness above. Held back (WritesVersion(1)), no old node refuses a call.
// Every commit is recorded in release 2's shape, whichever release's node
// made it: the relay renders as release 2 from the start. Positive eager
// refusal coverage does not depend on a small sample routing to an old node.
func TestRollout(t *testing.T) {
	var held, shapes int
	for seed := uint64(1); seed <= 6; seed++ {
		_, s := olderRefused(rollout(t, seed, notes(2, false)))
		shapes += s
		r, s := olderRefused(rollout(t, seed, notes(2, true)))
		held, shapes = held+r, shapes+s
	}
	if held != 0 {
		t.Errorf("%d calls refused by a node of release 1 in rollouts of a release writing version 1 (WritesVersion): it reads every event", held)
	}
	if shapes != 0 {
		t.Errorf("%d commits recorded in another shape than release 2's", shapes)
	}
}
