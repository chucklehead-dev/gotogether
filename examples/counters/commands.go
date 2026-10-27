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
var Create = grist.NewCommand(App, "create-counter", decideCreate, grist.Authorize(grist.Authenticated))

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
var Increment = grist.NewCommand(App, "increment-counter", decideIncrement, grist.Authorize(grist.Authenticated))

// RetireCounter retires a counter: its total is paid out (Payout).
type RetireCounter struct {
	Counter string `json:"counter"`
}

func decideRetire(r *grist.Reads, c RetireCounter) ([]grist.Event, error) {
	if !r.Exists(Created, grist.Tag("counter", c.Counter)) {
		return nil, ErrUnknownCounter
	}
	return []grist.Event{Retired.Of(CounterRetired{Counter: c.Counter, Total: Totals.For(r, c.Counter)})}, nil
}

// Retire is the retire-counter command: its total read through the keyed
// fold, so it commits only if no increment of the counter came since.
var Retire = grist.NewCommand(App, "retire-counter", decideRetire, grist.Authorize(grist.Authenticated))

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
}, grist.Authorize(grist.Authenticated))
