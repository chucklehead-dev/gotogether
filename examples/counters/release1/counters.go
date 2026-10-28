// Package release1 is counters as deployed at release 1, kept as it was
// for the rolling update to release 2 (the package counters): its events
// at version 1 (counter-created's creator), its read models, commands,
// queries, processors and dependencies. gristtest.Upgrade runs its nodes
// beside release 2's on one store (rollout_test.go). Never edit it but to
// mirror a fix the deployed release got; delete it once no node of
// release 1 runs anywhere.
package release1

import (
	"sort"

	"github.com/chucklehead-dev/gotogether/grist"
)

// The events.
type (
	// CounterCreated: a counter, under a name no other counter has, by
	// its creator (the caller that created it: the one who may retire it).
	CounterCreated struct {
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
	Created     = grist.NewEvent[CounterCreated](App, "counter-created")
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
