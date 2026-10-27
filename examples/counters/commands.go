package counters

import "github.com/chucklehead-dev/gotogether/grist"

// CreateCounter creates a counter under a name no other counter has.
type CreateCounter struct {
	Counter string `json:"counter"`
	Name    string `json:"name"`
}

// ErrNameTaken refuses a name another counter has.
var ErrNameTaken = grist.Rejection("name-taken")

// decideCreate refuses a taken name: its read of the name tag is in the
// read set, so its append commits only if no counter took the name since
// (two concurrent creates of one name: one commits, the other decides
// again and is refused).
func decideCreate(r *grist.Reads, c CreateCounter) ([]grist.Event, error) {
	if r.Exists(Created, grist.Tag("name", c.Name)) {
		return nil, ErrNameTaken
	}
	return []grist.Event{Created.Of(CounterCreated{Counter: c.Counter, Name: c.Name})}, nil
}

// Create is the create-counter command.
var Create = grist.NewCommand(App, "create-counter", decideCreate)

// IncrementCounter adds By to a counter's total.
type IncrementCounter struct {
	Counter string `json:"counter"`
	By      int    `json:"by"`
}

// ErrUnknownCounter refuses an increment of a counter never created.
var ErrUnknownCounter = grist.Rejection("unknown-counter")

func decideIncrement(r *grist.Reads, c IncrementCounter) ([]grist.Event, error) {
	if !r.Exists(Created, grist.Tag("counter", c.Counter)) {
		return nil, ErrUnknownCounter
	}
	Totals.For(r, c.Counter) // read through the keyed fold: conditioned on c's increments alone
	return []grist.Event{Incremented.Of(CounterIncremented{Counter: c.Counter, By: c.By})}, nil
}

// Increment is the increment-counter command.
var Increment = grist.NewCommand(App, "increment-counter", decideIncrement)

// ShowCounter asks for a counter.
type ShowCounter struct {
	Counter string `json:"counter"`
}

// CounterView is a counter as shown: its total and how many names name
// it.
type CounterView struct {
	Total int `json:"total"`
	Names int `json:"names"`
}

// Show is the show-counter query.
var Show = grist.NewQuery(App, "show-counter", func(r *grist.Reads, q ShowCounter) (CounterView, error) {
	return CounterView{Total: Totals.For(r, q.Counter), Names: NamesOf(CounterNames.Get(r), q.Counter)}, nil
})
