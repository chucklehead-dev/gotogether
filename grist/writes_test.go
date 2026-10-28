package grist_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// AddNoted adds an item with a note, which only version 2 of item-added
// holds.
type AddNoted struct {
	Item  string `json:"item"`
	Count int    `json:"count"`
	Note  string `json:"note"`
}

func v2ToV1(v ItemAddedV2) ItemAddedV1 { return ItemAddedV1{Item: v.Item, Qty: v.Count} }

// release is items at version 1 (no options) or 2 (an upcaster from 1,
// and opts), with a command adding an item and the count query.
func release(version int, opts ...grist.EventOption) (*grist.App, *grist.Command[AddNoted], *grist.QueryBlock[ItemQuery, int]) {
	a := grist.New("items")
	var add *grist.Command[AddNoted]
	var counts *grist.KeyedReadModel[int]
	if version == 1 {
		added := grist.NewEvent[ItemAddedV1](a, "item-added")
		counts = grist.NewKeyedReadModel(a, "counts", 1, grist.Keys("item"), func() int { return 0 },
			grist.On(added, func(s int, e ItemAddedV1, m grist.Meta) int { return s + e.Qty }))
		add = grist.NewCommand(a, "add-item", func(r *grist.Reads, c AddNoted) ([]grist.Event, error) {
			return []grist.Event{added.Of(ItemAddedV1{Item: c.Item, Qty: c.Count})}, nil
		})
	} else {
		added := grist.NewEvent[ItemAddedV2](a, "item-added", append([]grist.EventOption{grist.Upcast(1, v1ToV2)}, opts...)...)
		counts = grist.NewKeyedReadModel(a, "counts", 1, grist.Keys("item"), func() int { return 0 },
			grist.On(added, func(s int, e ItemAddedV2, m grist.Meta) int { return s + e.Count }))
		add = grist.NewCommand(a, "add-item", func(r *grist.Reads, c AddNoted) ([]grist.Event, error) {
			counts.For(r, c.Item)
			return []grist.Event{added.Of(ItemAddedV2{Item: c.Item, Count: c.Count, Note: c.Note})}, nil
		})
	}
	q := grist.NewQuery(a, "count", func(r *grist.Reads, c ItemQuery) (int, error) { return counts.For(r, c.Item), nil })
	return a, add, q
}

// A release that reads version 2 and writes version 1 (WritesVersion)
// rolls out over one that reads only version 1: its appends are stored
// as version 1, which the older release folds; its steps are recorded in
// version 2's shape, the model's; and an append version 1 cannot hold (a
// note, which only version 2 has) fails, naming the fix, instead of
// being stored without it. The release after it, without WritesVersion,
// writes version 2, which the older refuses by name.
func TestWritesVersion(t *testing.T) {
	ctx := context.Background()
	s := newStoreNamed(t, "items")
	r2, add2, q2 := release(2, grist.WritesVersion(1, v2ToV1))
	if err := r2.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := r2.CheckUpcasters(); err != nil {
		t.Fatal(err)
	}
	if _, err := add2.Execute(ctx, s, grist.Envelope{ID: "c1", Tenant: "t1"}, AddNoted{Item: "x", Count: 2}); err != nil {
		t.Fatal(err)
	}
	evs, _, _ := s.Read(ctx, "t1", grist.Selector{Types: []string{"item-added"}}, 0)
	if len(evs) != 1 || evs[0].Version != 1 || string(evs[0].Body) != `{"item":"x","qty":2}` {
		t.Fatalf("stored %+v, want version 1's body {item x, qty 2}", evs)
	}
	_, _, q1 := release(1)
	if n, _, err := q1.Run(ctx, s, grist.Envelope{ID: "q1", Tenant: "t1"}, ItemQuery{Item: "x"}); err != nil || n != 2 {
		t.Errorf("the older release's count of x: %d (%v), want 2: it reads what this one writes", n, err)
	}
	if n, _, err := q2.Run(ctx, s, grist.Envelope{ID: "q2", Tenant: "t1"}, ItemQuery{Item: "x"}); err != nil || n != 2 {
		t.Errorf("this release's count of x: %d (%v), want 2", n, err)
	}
	_, err := add2.Execute(ctx, s, grist.Envelope{ID: "c2", Tenant: "t1"}, AddNoted{Item: "x", Count: 1, Note: "n"})
	if err == nil || !strings.Contains(err.Error(), "which cannot hold it") || !strings.Contains(err.Error(), "grist.WritesVersion") {
		t.Errorf("an append of a note while writing version 1: %v, want it refused with the fix", err)
	}
	if evs, _, _ = s.Read(ctx, "t1", grist.Selector{Types: []string{"item-added"}}, 0); len(evs) != 1 {
		t.Errorf("%d events stored, want the one: the note's append failed", len(evs))
	}
	// recorded in version 2's shape: item, note; count
	sink := &qobs.MemorySink{}
	relay, err := grist.NewRelay(s)
	if err != nil {
		t.Fatal(err)
	}
	grist.RelayFor(relay, r2)
	if err := relay.Flush(qobs.WithRecorder(ctx, qobs.NewRecorder(sink))); err != nil {
		t.Fatal(err)
	}
	if steps := sink.Steps(); len(steps) != 1 || steps[0].Obs["strs"] != `["x",""]` || steps[0].Obs["ints"] != `[2]` {
		t.Errorf("relayed %v, want the commit with strs [x, ''] and ints [2], version 2's shape", steps)
	}
	if c := r2.Catalog(); c.Events[2].Writes != 1 || c.Events[2].Version != 2 {
		t.Errorf("catalog: %+v, want version 2 writing 1", c.Events[2])
	}
	// the next release writes version 2: the older refuses it by name
	_, add3, _ := release(2)
	if _, err := add3.Execute(ctx, s, grist.Envelope{ID: "c3", Tenant: "t1"}, AddNoted{Item: "x", Count: 1, Note: "n"}); err != nil {
		t.Fatal(err)
	}
	_, _, err = q1.Run(ctx, s, grist.Envelope{ID: "q3", Tenant: "t1"}, ItemQuery{Item: "x"})
	if !errors.As(err, new(*grist.NewerVersionError)) || !errors.Is(err, grist.ErrUnavailable) || !strings.Contains(err.Error(), "grist.WritesVersion(1, downcaster)") {
		t.Errorf("the older release reading version 2: %v, want a *NewerVersionError naming WritesVersion, unavailable (a node of the newer release serves it)", err)
	}
}

// WritesVersion is checked at boot (Verify): an older version than the
// newest, the downcaster's types those of the event and of that version,
// and over generated values total, pure and keeping every tag;
// CheckUpcasters also requires it to undo the upcaster.
func TestWritesVersionVerified(t *testing.T) {
	calls := 0
	for _, c := range []struct {
		name  string
		opt   grist.EventOption
		want  string
		check bool // CheckUpcasters', not Verify's
	}{
		{"the newest", grist.WritesVersion(2, func(v ItemAddedV2) ItemAddedV2 { return v }), "appends write the newest already", false},
		{"another struct", grist.WritesVersion(1, func(v ItemAddedV2) ItemAdded { return ItemAdded{} }), "but version 1's struct is grist_test.ItemAddedV1", false},
		{"a tag changed", grist.WritesVersion(1, func(v ItemAddedV2) ItemAddedV1 { return ItemAddedV1{Item: strings.ToUpper(v.Item)} }), "a downcaster keeps every tag", false},
		{"a panic", grist.WritesVersion(1, func(v ItemAddedV2) ItemAddedV1 { return ItemAddedV1{Item: v.Item, Qty: 10 / v.Count} }), "downcaster panicked", false},
		{"impure", grist.WritesVersion(1, func(v ItemAddedV2) ItemAddedV1 { calls++; return ItemAddedV1{Item: v.Item, Qty: calls} }), "a downcaster is a pure function", false},
		{"no inverse", grist.WritesVersion(1, func(v ItemAddedV2) ItemAddedV1 { return ItemAddedV1{Item: v.Item, Qty: v.Count + 1} }), "the downcaster undoes the upcasters", true},
	} {
		a, _, _ := release(2, c.opt)
		err := a.Verify()
		if c.check {
			if err != nil {
				t.Errorf("%s: Verify: %v, want CheckUpcasters' finding only", c.name, err)
			}
			err = a.CheckUpcasters()
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

// catalogOf is a's catalog's JSON in a file, as the deployed release's
// binary writes it (`<app> catalog > deployed.json`).
func catalogOf(t *testing.T, a *grist.App) string {
	var out strings.Builder
	if err := grist.MainArgs(context.Background(), a, []string{"catalog"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	p := t.TempDir() + "/deployed.json"
	if err := os.WriteFile(p, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// catalog -diff classifies a release against the deployed one: a new
// version written at once is readers first (the deployed nodes refuse
// it), the same version written as the deployed newest (WritesVersion)
// safe, and a release that reads less than the deployed one writes, or
// whose version changed shape, breaking; each with its fix, the exit
// status saying which.
func TestCatalogDiff(t *testing.T) {
	r1, _, _ := release(1)
	eager, _, _ := release(2)
	held, _, _ := release(2, grist.WritesVersion(1, v2ToV1))
	reshaped := grist.New("items") // version 1 changed in place
	grist.NewEvent[ItemAddedV2](reshaped, "item-added")
	extra, _, _ := release(1)
	grist.NewQuery(extra, "noted", func(r *grist.Reads, c ItemQuery) (int, error) { return 0, nil })
	for _, c := range []struct {
		name     string
		deployed *grist.App
		app      *grist.App
		status   int
		want     []string
	}{
		{"a version written at once", r1, eager, 3, []string{"readers first\n", "this release writes version 2, and the deployed nodes read it up to version 1", "grist.WritesVersion(1, "}},
		{"readers first", r1, held, 0, []string{"safe\n", "reads version 2 and writes version 1"}},
		{"then writers", held, eager, 0, []string{"safe\n"}},
		{"a rollback", eager, r1, 4, []string{"breaking\n", "this release reads it up to version 1, and the deployed one appends version 2"}},
		{"a version changed in place", r1, reshaped, 4, []string{"event item-added version 1 changed shape (removed qty)"}},
		{"a new query", r1, extra, 0, []string{"safe\n", "query noted is new"}},
		{"a query removed", extra, r1, 4, []string{"query noted is removed"}},
	} {
		var out strings.Builder
		err := grist.MainArgs(context.Background(), c.app, []string{"catalog", "-diff", catalogOf(t, c.deployed)}, &out, &out)
		status := 0
		var es grist.ExitStatus
		if errors.As(err, &es) {
			status = int(es)
		} else if err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if status != c.status {
			t.Errorf("%s: exit status %d, want %d:\n%s", c.name, status, c.status, out.String())
		}
		for _, w := range c.want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: %q missing from\n%s", c.name, w, out.String())
			}
		}
	}
}
