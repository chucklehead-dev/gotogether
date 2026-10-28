// Package counters is grain's counter example on grist (docs/grist.md
// §8.1), without float averages: counters created under unique names,
// incremented, shown; a processor that emits the tenant's running sum
// and count on each increment, and one that welcomes each new counter by
// mail, at least once; a retired counter's total is paid out, at most
// once. Its model is model/counters.qnt; gristsim runs it
// against a simulated store, and counters_test.go states which check
// catches which mutant (testdata/mutants: grain's two bugs).
package counters

import (
	"sort"

	"github.com/chucklehead-dev/gotogether/grist"
)

// The events.
type (
	// CounterCreated: a counter, under a name no other counter has, and
	// its owner (the caller that created it: the one who may retire it).
	// Version 2: version 1 (CounterCreatedV1) called the owner creator.
	CounterCreated struct {
		Counter string `json:"counter" grist:"tag"`
		Name    string `json:"name" grist:"tag"`
		Owner   string `json:"owner"`
	}
	// CounterCreatedV1 is counter-created as release 1 stored it, kept
	// as it was: the owner was its creator.
	CounterCreatedV1 struct {
		Counter string `json:"counter" grist:"tag"`
		Name    string `json:"name" grist:"tag"`
		Creator string `json:"creator"`
	}
	// CounterIncremented: a counter's total grew by By.
	CounterIncremented struct {
		Counter string `json:"counter" grist:"tag"`
		By      int    `json:"by"`
	}
	// AverageComputed: the tenant's sum and count of increments as of one
	// increment (integers: the mean is the reader's).
	AverageComputed struct {
		Sum   int `json:"sum"`
		Count int `json:"count"`
	}
	// Notified: a counter's welcome was sent.
	Notified struct {
		Counter string `json:"counter" grist:"tag"`
	}
	// CounterRetired: a counter retired with its total, to be paid out.
	CounterRetired struct {
		Counter string `json:"counter" grist:"tag"`
		Total   int    `json:"total"`
	}
	// PaidOut: a retirement's payout was made (Retired: the retirement's
	// position, so each retirement's outcome is its own).
	PaidOut struct {
		Counter string `json:"counter" grist:"tag"`
		Retired int64  `json:"retired"`
	}
)

// App is the application; its blocks are below and in commands.go and
// processors.go.
var App = grist.New("counters")

// The event types.
var (
	// Created is at version 2, read by this release (release 2) and
	// written as version 1 (WritesVersion), which release 1's nodes read,
	// while the rolling update from release 1 lasts; release 3 drops
	// WritesVersion and writes version 2 (rollout_test.go, docs/grist.md
	// §2.1: readers first).
	Created = grist.NewEvent[CounterCreated](App, "counter-created",
		grist.Upcast(1, func(v CounterCreatedV1) CounterCreated {
			return CounterCreated{Counter: v.Counter, Name: v.Name, Owner: v.Creator}
		}),
		grist.WritesVersion(1, func(v CounterCreated) CounterCreatedV1 {
			return CounterCreatedV1{Counter: v.Counter, Name: v.Name, Creator: v.Owner}
		}))
	Incremented = grist.NewEvent[CounterIncremented](App, "counter-incremented")
	Averaged    = grist.NewEvent[AverageComputed](App, "average-computed")
	Welcomed    = grist.NewEvent[Notified](App, "notified")
	Retired     = grist.NewEvent[CounterRetired](App, "counter-retired")
	Paid        = grist.NewEvent[PaidOut](App, "paid-out")
)

// Names is the counter-names read model: each name's counter.
type Names map[string]string

// The read models.
var (
	// CounterNames folds every creation (plain).
	CounterNames = grist.NewReadModel(App, "counter-names", 1, func() Names { return Names{} },
		grist.On(Created, func(s Names, e CounterCreated, m grist.Meta) Names {
			s[e.Name] = e.Counter
			return s
		}))
	// Totals is each counter's total, keyed by counter: a decision about
	// counter c reads, and is conditioned on, c's increments alone.
	Totals = grist.NewKeyedReadModel(App, "totals", 1, grist.Keys("counter"), func() int { return 0 },
		grist.On(Incremented, func(s int, e CounterIncremented, m grist.Meta) int { return s + e.By }))
)

// NamesOf is how many names name counter, in n.
func NamesOf(n Names, counter string) int {
	names := make([]string, 0, len(n))
	for name := range n {
		names = append(names, name)
	}
	sort.Strings(names)
	count := 0
	for _, name := range names {
		if n[name] == counter {
			count++
		}
	}
	return count
}
