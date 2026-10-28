package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// An item's addition, three times over: v1 counted Qty; v2 renamed it
// Count and gained a Note; v3 (ItemAdded, the struct the app appends)
// gained a Unit.
type (
	ItemAddedV1 struct {
		Item string `json:"item" grist:"tag"`
		Qty  int    `json:"qty"`
	}
	ItemAddedV2 struct {
		Item  string `json:"item" grist:"tag"`
		Count int    `json:"count"`
		Note  string `json:"note"`
	}
	ItemAdded struct {
		Item  string `json:"item" grist:"tag"`
		Count int    `json:"count"`
		Note  string `json:"note"`
		Unit  string `json:"unit"`
	}
	AddItem struct {
		Item  string `json:"item"`
		Count int    `json:"count"`
	}
	ItemQuery struct {
		Item string `json:"item"`
	}
)

func v1ToV2(v ItemAddedV1) ItemAddedV2 { return ItemAddedV2{Item: v.Item, Count: v.Qty} }
func v2ToV3(v ItemAddedV2) ItemAdded {
	return ItemAdded{Item: v.Item, Count: v.Count, Note: v.Note, Unit: "each"}
}

// items is an app of the event at its newest version, with a keyed read
// model of each item's count and a query of it.
func items(ups ...grist.EventOption) (*grist.App, *grist.EventType[ItemAdded], *grist.QueryBlock[ItemQuery, int]) {
	a := grist.New("items")
	added := grist.NewEvent[ItemAdded](a, "item-added", ups...)
	counts := grist.NewKeyedReadModel(a, "counts", 1, grist.Keys("item"), func() int { return 0 },
		grist.On(added, func(s int, e ItemAdded, m grist.Meta) int { return s + e.Count }))
	q := grist.NewQuery(a, "count", func(r *grist.Reads, c ItemQuery) (int, error) { return counts.For(r, c.Item), nil })
	return a, added, q
}

// raw is an event of item-added as an app of version v appended it.
func raw(v int, body any) grist.Event {
	b, _ := json.Marshal(body)
	var tags struct {
		Item string `json:"item"`
	}
	json.Unmarshal(b, &tags)
	return grist.Event{Type: "item-added", Version: v, Tags: []grist.TagPair{{Key: "item", Value: tags.Item}}, Body: b}
}

// A log of all three versions folds as the newest: each stored event is
// decoded as the version it was stored at and upcast through the chain
// (v1 through two upcasters), whatever order the upcasters were given
// in; an append writes the newest version only.
func TestUpcastChain(t *testing.T) {
	a, added, q := items(grist.Upcast(2, v2ToV3), grist.Upcast(1, v1ToV2))
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckUpcasters(); err != nil {
		t.Fatal(err)
	}
	if added.Version() != 3 {
		t.Fatalf("version %d, want 3: one more than its upcasters", added.Version())
	}
	s := newStoreNamed(t, "items")
	ctx := context.Background()
	for _, e := range []grist.Event{
		raw(1, ItemAddedV1{Item: "x", Qty: 2}),
		raw(2, ItemAddedV2{Item: "x", Count: 3, Note: "n"}),
		raw(3, ItemAdded{Item: "x", Count: 4, Unit: "kg"}),
		added.Of(ItemAdded{Item: "x", Count: 5}),
		raw(0, ItemAdded{Item: "x", Count: 6}), // an Event written by hand, no version: the first's
	} {
		if _, err := s.Append(ctx, "t1", storeBatch(e)); err != nil {
			t.Fatal(err)
		}
	}
	// version 0, written by hand, is version 1: Qty, not Count
	if n, _, err := q.Run(ctx, s, grist.Envelope{ID: "q", Tenant: "t1"}, ItemQuery{Item: "x"}); err != nil || n != 2+3+4+5 {
		t.Errorf("count of x: %d (%v), want 14 (v1's qty, v2's and v3's count, the append's; the unversioned event's qty, none)", n, err)
	}
	evs, _, _ := s.Read(ctx, "t1", grist.Selector{Types: []string{"item-added"}}, 0)
	if evs[3].Version != 3 {
		t.Errorf("Of wrote version %d, want the newest, 3", evs[3].Version)
	}
	e, err := added.Decode(evs[0])
	if want := (ItemAdded{Item: "x", Count: 2, Unit: "each"}); err != nil || e != want {
		t.Errorf("Decode of v1 %s: %+v (%v), want %+v", evs[0].Body, e, err, want)
	}
}

// A stored version newer than the app knows (a v2 node's event read by a
// v1 node during a rollout) is refused by name, never decoded into the
// older struct: the fold fails with a *NewerVersionError saying to finish
// the rollout, readers first.
func TestNewerVersionRefused(t *testing.T) {
	_, added, q := items(grist.Upcast(1, v1ToV2), grist.Upcast(2, v2ToV3))
	s := newStoreNamed(t, "items")
	ctx := context.Background()
	s.Append(ctx, "t1", storeBatch(raw(4, ItemAdded{Item: "x", Count: 1})))
	_, _, err := q.Run(ctx, s, grist.Envelope{ID: "q", Tenant: "t1"}, ItemQuery{Item: "x"})
	if err == nil || !strings.Contains(err.Error(), "item-added is version 4, and this app reads item-added up to version 3") ||
		!strings.Contains(err.Error(), "readers") {
		t.Errorf("a read of a v4 event by a v3 app: %v, want the newer version refused by name", err)
	}
	evs, _, _ := s.Read(ctx, "t1", grist.Selector{Types: []string{"item-added"}}, 0)
	if _, err := added.Decode(evs[0]); !errors.As(err, new(*grist.NewerVersionError)) {
		t.Errorf("Decode of a v4 event: %v, want a *NewerVersionError", err)
	}
}

// What the chain must be is checked at registration (Verify, so at boot):
// one upcaster from each old version, each returning what the next takes,
// the last the event's own struct; a dropped field the old struct has.
func TestUpcastChainVerified(t *testing.T) {
	for _, c := range []struct {
		name string
		ups  []grist.EventOption
		want string
	}{
		{"a version's upcaster missing", []grist.EventOption{grist.Upcast(2, v2ToV3)},
			"its 1 upcasters are from versions [2], where version 2 needs one from each of 1 to 1"},
		{"a gap", []grist.EventOption{grist.Upcast(1, v1ToV2), grist.Upcast(3, v2ToV3)},
			"from versions [1 3], where version 3 needs one from each of 1 to 2"},
		{"version 0", []grist.EventOption{grist.Upcast(0, v1ToV2)}, "Upcast(0, ...): versions start at 1"},
		{"a link that does not chain", []grist.EventOption{grist.Upcast(1, v1ToV2), grist.Upcast(2, func(v ItemAddedV1) ItemAdded { return ItemAdded{} })},
			"the upcaster from version 1 returns grist_test.ItemAddedV2, but the upcaster from version 2 takes grist_test.ItemAddedV1"},
		{"the last not the event", []grist.EventOption{grist.Upcast(1, v1ToV2)},
			"the upcaster from version 1 returns grist_test.ItemAddedV2, but the event's own struct is grist_test.ItemAdded"},
		{"a dropped field it does not have", []grist.EventOption{grist.Upcast(1, v1ToV2).Drops("quantity"), grist.Upcast(2, v2ToV3)},
			`the upcaster from version 1 drops "quantity", which grist_test.ItemAddedV1 has no field of (its fields by JSON name: [item qty])`},
	} {
		a, _, _ := items(c.ups...)
		if err := a.Verify(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Verify: %v, want %q", c.name, err, c.want)
		}
	}
}

// Every upcaster runs over generated values of the struct it takes at
// boot: one that changes a tag (the store indexed the event by the tags
// it was appended with), panics on a value (not total) or answers twice
// differently (not pure) is Verify's problem, naming the value.
func TestUpcastersTotalPureAndTagKeeping(t *testing.T) {
	calls := 0
	for _, c := range []struct {
		name string
		up   grist.Upcaster
		want string
	}{
		{"a tag changed", grist.Upcast(1, func(v ItemAddedV1) ItemAddedV2 { return ItemAddedV2{Item: strings.ToUpper(v.Item), Count: v.Qty} }),
			"an upcaster keeps every tag (key and value)"},
		{"a panic", grist.Upcast(1, func(v ItemAddedV1) ItemAddedV2 { return ItemAddedV2{Item: v.Item, Count: 10 / v.Qty} }),
			"panicked: runtime error: integer divide by zero (an upcaster is total"},
		{"impure", grist.Upcast(1, func(v ItemAddedV1) ItemAddedV2 { calls++; return ItemAddedV2{Item: v.Item, Count: v.Qty + calls} }),
			"an upcaster is a pure function of the stored event"},
	} {
		a, _, _ := items(c.up, grist.Upcast(2, v2ToV3))
		if err := a.Verify(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Verify: %v, want %q", c.name, err, c.want)
		}
	}
}

// An upcaster that reads nothing of a field drops it from every old
// event: CheckUpcasters (not boot: dropping one may be the point) names
// the field and both fixes; Drops says it is on purpose.
func TestCheckUpcastersFieldUse(t *testing.T) {
	forgets := grist.Upcast(1, func(v ItemAddedV1) ItemAddedV2 { return ItemAddedV2{Item: v.Item} })
	a, _, _ := items(forgets, grist.Upcast(2, v2ToV3))
	if err := a.Verify(); err != nil {
		t.Fatalf("Verify: %v; a dropped field is CheckUpcasters' finding, not boot's", err)
	}
	err := a.CheckUpcasters()
	if err == nil || !strings.Contains(err.Error(), "the upcaster from version 1 ignores qty (grist_test.ItemAddedV1.Qty)") ||
		!strings.Contains(err.Error(), `Upcast(1, ...).Drops("qty")`) {
		t.Errorf("CheckUpcasters of an upcaster forgetting qty: %v, want qty named with the fix", err)
	}
	// v2's note never reaches v3 here either: only v1's qty is reported,
	// since v2ToV3 copies it
	if err != nil && strings.Contains(err.Error(), "ignores note") {
		t.Errorf("CheckUpcasters: %v: v2ToV3 copies the note", err)
	}
	a, _, _ = items(forgets.Drops("qty"), grist.Upcast(2, v2ToV3))
	if err := a.CheckUpcasters(); err != nil {
		t.Errorf("CheckUpcasters with the drop declared: %v", err)
	}
}

// A relay records a row's events stored at an older version as the
// newest: obs.strs and obs.ints are recomputed from the upcast bodies, so
// a log appended by an older app is judged by today's model, which knows
// only the newest shapes. A row of newest-version events is recorded as
// written.
func TestRelayUpcastsOldRows(t *testing.T) {
	old := grist.New("items")
	oldAdded := grist.NewEvent[ItemAddedV1](old, "item-added")
	add := grist.NewCommand(old, "add-item", func(r *grist.Reads, c AddItem) ([]grist.Event, error) {
		return []grist.Event{oldAdded.Of(ItemAddedV1{Item: c.Item, Qty: c.Count})}, nil
	})
	s := newStoreNamed(t, "items")
	ctx := context.Background()
	if _, err := add.Execute(ctx, s, grist.Envelope{ID: "c1", Tenant: "t1"}, AddItem{Item: "x", Count: 2}); err != nil {
		t.Fatal(err)
	}
	cur, added, _ := items(grist.Upcast(1, v1ToV2), grist.Upcast(2, v2ToV3))
	newAdd := grist.NewCommand(cur, "add-item", func(r *grist.Reads, c AddItem) ([]grist.Event, error) {
		return []grist.Event{added.Of(ItemAdded{Item: c.Item, Count: c.Count, Unit: "kg"})}, nil
	})
	if _, err := newAdd.Execute(ctx, s, grist.Envelope{ID: "c2", Tenant: "t1"}, AddItem{Item: "y", Count: 3}); err != nil {
		t.Fatal(err)
	}
	sink := &qobs.MemorySink{}
	rctx := qobs.WithRecorder(ctx, qobs.NewRecorder(sink))
	r, err := grist.NewRelay(s)
	if err != nil {
		t.Fatal(err)
	}
	grist.RelayFor(r, cur)
	if err := r.Flush(rctx); err != nil {
		t.Fatal(err)
	}
	steps := sink.Steps()
	if len(steps) != 2 {
		t.Fatalf("%d steps, want the two commits", len(steps))
	}
	for i, want := range []struct{ strs, ints string }{{`["x","","each"]`, `[2]`}, {`["y","","kg"]`, `[3]`}} {
		if got := steps[i].Obs; got["strs"] != want.strs || got["ints"] != want.ints {
			t.Errorf("step %d's events: strs %v, ints %v; want %s and %s (the newest shape: item, note, unit; count)", i, got["strs"], got["ints"], want.strs, want.ints)
		}
	}
}
