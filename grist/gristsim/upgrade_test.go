package gristsim_test

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
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

// A rolling update from release 1 to a release 2 that writes version 2 at
// once: a node of release 1 still running meets a version 2 event, and
// refuses the call (retried, a newer node answering it), never deciding
// without the event. Held back (WritesVersion(1)), no node refuses
// anything. Either way every commit is recorded in release 2's shape,
// whichever release's node made it: the relay renders as release 2 from
// the start.
func TestRollout(t *testing.T) {
	var eager, held, shapes int
	for seed := uint64(1); seed <= 6; seed++ {
		r, s := olderRefused(rollout(t, seed, notes(2, false)))
		eager, shapes = eager+r, shapes+s
		r, s = olderRefused(rollout(t, seed, notes(2, true)))
		held, shapes = held+r, shapes+s
	}
	if eager == 0 {
		t.Errorf("no call refused by a node of release 1 in 6 rollouts of a release writing version 2 at once")
	}
	if held != 0 {
		t.Errorf("%d calls refused by a node of release 1 in rollouts of a release writing version 1 (WritesVersion): it reads every event", held)
	}
	if shapes != 0 {
		t.Errorf("%d commits recorded in another shape than release 2's", shapes)
	}
}
