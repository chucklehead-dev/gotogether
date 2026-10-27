package grist

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Store is grist's event store: one log per tenant, appends serialized
// per tenant, conditions checked under the tenant's lock. It is grist's
// own interface (no aggregates: a condition is a set of queries through
// a position). Implementations: grist/memstore, grist/simstore; every
// one passes grist/storetest's contract suite.
type Store interface {
	// Name is the store's name: the process of every step recorded at it
	// (docs/grist.md §3.1).
	Name() string
	// CreateTenant creates a tenant, its head at 0. Creating one that
	// exists does nothing.
	CreateTenant(ctx context.Context, tenant string) error
	// Tenants are the tenants, sorted.
	Tenants(ctx context.Context) ([]string, error)
	// Read returns the events of tenant matching q through at (0: the
	// head), in position order or newest first (q.Reverse), at most
	// q.Limit (0: all), and the position read through (at, or the head).
	Read(ctx context.Context, tenant string, q Query, at Position) ([]Stored, Position, error)
	// Append commits the batch at the tenant's next positions if its
	// condition holds, its marker is new and its checkpoint is as
	// expected, and returns the head after it. A *ConditionFailed or a
	// *CheckpointMoved says which did not hold; any other error is
	// transient (a lost race for the tenant's lock, a commit whose
	// outcome is unknown, which may have committed).
	Append(ctx context.Context, tenant string, b Batch) (Position, error)
	// Marker is the marker a committed batch wrote under id, if any.
	Marker(ctx context.Context, tenant, id string) (Marker, bool, error)
	// Checkpoint is processor's checkpoint in tenant (0: none).
	Checkpoint(ctx context.Context, tenant, processor string) (Position, error)
	// Heads are every tenant's head: a change feed's poll.
	Heads(ctx context.Context) (map[string]Position, error)
	// Wait blocks until tenant's head is past p, or ctx is done.
	Wait(ctx context.Context, tenant string, p Position) error
}

// A Query selects events: of one of Types (at least one), carrying every
// tag of Tags, after position After, newest first if Reverse, at most
// Limit (0: all). A query with no types matches nothing (a read of the
// head alone).
type Query struct {
	Types   []string
	Tags    []TagPair
	After   Position
	Reverse bool
	Limit   int
}

// Matches reports whether e is of one of the query's types and carries
// its tags (After, Reverse and Limit select among matches by position;
// they do not change what matches).
func (q Query) Matches(e Event) bool {
	ok := false
	for _, t := range q.Types {
		if t == e.Type {
			ok = true
			break
		}
	}
	return ok && e.Tagged(q.Tags)
}

func (q Query) String() string {
	var tags []string
	for _, t := range q.Tags {
		tags = append(tags, t.Key+"="+t.Value)
	}
	s := strings.Join(q.Types, "|")
	if len(tags) > 0 {
		s += "{" + strings.Join(tags, ",") + "}"
	}
	return s
}

// A Condition is DCB's: no event matching any of Queries appended after
// After.
type Condition struct {
	After   Position
	Queries []Query
}

// A Batch is what one Append commits, atomically.
type Batch struct {
	Events []Event
	// Condition, if set, must hold (nil: unconditioned).
	Condition *Condition
	// Marker, if set, is written under its ID, which must be new: a key
	// names at most one committed outcome (Append fails with a
	// *ConditionFailed naming the marker otherwise). Its Receipt is filled
	// in by the store.
	Marker *Marker
	// Checkpoint, if set, moves a processor's checkpoint, a
	// compare-and-set: from From (else *CheckpointMoved), to To.
	Checkpoint *CheckpointMove
	// Time is the append's time, Unix milliseconds (qenv.Now).
	Time int64
	// Cause is what the events are stamped with (Stored.Cause).
	Cause string
	// OnCommit runs at the commit, with the head after it, before Append
	// returns, even when the caller is then told the outcome is unknown:
	// where grist records the step, so a commit whose answer is lost is
	// recorded.
	OnCommit func(head Position)
}

// A Marker is a committed command's record: the hash of its canonical
// JSON and its receipt, under its id.
type Marker struct {
	ID      string
	Hash    string
	Receipt Receipt
}

// A Receipt is a committed command's result: the batch's last position
// (the head after it) and a reference to each event it appended. It is
// what the command's marker holds, so a retry's answer is the first
// attempt's exactly.
type Receipt struct {
	Pos    Position
	Events []EventRef
}

// NewReceipt is the receipt of n events appended to tenant ending at head
// (a store fills a marker's with it).
func NewReceipt(tenant string, head Position, n int) Receipt {
	r := Receipt{Pos: head, Events: []EventRef{}}
	for p := head - Position(n) + 1; p <= head; p++ {
		r.Events = append(r.Events, EventRef{Tenant: tenant, Pos: p})
	}
	return r
}

// An EventRef names a stored event: its tenant and position, never
// renumbered.
type EventRef struct {
	Tenant string
	Pos    Position
}

// A CheckpointMove is a processor checkpoint's compare-and-set.
type CheckpointMove struct {
	Processor string
	From, To  Position
}

// ConditionFailed is an Append whose condition did not hold: an event
// matching Query was appended after the decision's position (Event), or
// the batch's marker exists already (Marker). "Decide again".
type ConditionFailed struct {
	Query  Query
	Event  Position
	Marker string
}

func (e *ConditionFailed) Error() string {
	if e.Marker != "" {
		return fmt.Sprintf("grist: condition failed: marker %s exists (the command committed already)", e.Marker)
	}
	return fmt.Sprintf("grist: condition failed: event %d matches %s, which the decision read before it", e.Event, e.Query)
}

// CheckpointMoved is an Append whose checkpoint was not where the
// processor read it: another poller moved it. Re-read it.
type CheckpointMoved struct {
	Processor string
	Expected  Position
	Found     Position
}

func (e *CheckpointMoved) Error() string {
	return fmt.Sprintf("grist: checkpoint of %s is %d, not %d: another poller moved it; re-read it", e.Processor, e.Found, e.Expected)
}

// Defaults of a command's tries.
const (
	// ConditionTries is how many times a command decides on fresh reads
	// when its condition fails.
	ConditionTries = 5
	// TransientTries is the budget of transient store errors (a lost race
	// for the tenant's lock, a commit of unknown outcome), apart from the
	// condition's tries.
	TransientTries = 10
)

// ErrUnknownTenant is a read or append of a tenant never created.
var ErrUnknownTenant = errors.New("grist: unknown tenant: create it first (Store.CreateTenant)")

// Transient reports whether err is a store error worth retrying: neither
// a failed condition nor a moved checkpoint nor an unknown tenant.
func Transient(err error) bool {
	var cf *ConditionFailed
	var cm *CheckpointMoved
	return err != nil && !errors.As(err, &cf) && !errors.As(err, &cm) && !errors.Is(err, ErrUnknownTenant) && !errors.Is(err, context.Canceled)
}

// CheckCondition is the reference evaluation of c over a tenant's whole
// log (events in position order): the first event after c.After matching
// one of its queries, as a *ConditionFailed, or nil. Stores may check
// it with indexes; storetest checks them against this.
func CheckCondition(log []Stored, c Condition) error {
	for _, e := range log {
		if e.Pos <= c.After {
			continue
		}
		for _, q := range c.Queries {
			if q.Matches(e.Event) {
				return &ConditionFailed{Query: q, Event: e.Pos}
			}
		}
	}
	return nil
}

// Select is the reference evaluation of a Read over a tenant's whole log
// (events in position order): q's matches through at, in q's order and
// limit.
func Select(log []Stored, q Query, at Position) []Stored {
	out := []Stored{}
	for _, e := range log {
		if e.Pos > q.After && e.Pos <= at && q.Matches(e.Event) {
			out = append(out, e)
		}
	}
	if q.Reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out
}
