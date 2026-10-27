// Package store is grist's event-store contract: the Store interface,
// what an append commits (Batch: events, a Condition, a Marker, a
// checkpoint's compare-and-set, the step it records), what a read
// selects (Selector) and the errors that say why an append did not
// commit. It imports nothing of grist, so a store tier (grist/memstore,
// grist/simstore, slice 2's sqlstore) depends on it alone, and
// grist/storetest checks every tier against CheckCondition and Select,
// the reference evaluations here (docs/grist.md §2.10).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Position is a place in a tenant's log: dense, from 1, assigned under the
// tenant's lock, so within a tenant the position order is the commit
// order. 0 is "before the first event" (and, as a Read's at, "the head").
type Position int64

// A TagPair is one tag of an event: a (key, value) pair, the key a tagged
// field's JSON name.
type TagPair struct {
	Key, Value string
}

// An Event is an event to append: its registered type and version, the
// tags its tagged fields give, and its body as JSON.
type Event struct {
	Type    string
	Version int
	Tags    []TagPair
	Body    json.RawMessage
}

// Tagged reports whether the event carries every tag of tags.
func (e Event) Tagged(tags []TagPair) bool {
	for _, t := range tags {
		found := false
		for _, have := range e.Tags {
			if have == t {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Clone is e with its own copies of Tags and Body: what a store keeps of
// an appended event and hands to each reader, so that a fold mutating an
// event in place corrupts nothing.
func (e Event) Clone() Event {
	e.Tags = append([]TagPair(nil), e.Tags...)
	e.Body = append(json.RawMessage(nil), e.Body...)
	return e
}

// Stored is an event as the store holds it: the event and its envelope.
type Stored struct {
	Event
	Tenant string
	Pos    Position
	// Time is the append's time, Unix milliseconds (qenv.Now: virtual in a
	// simulation), one value per append.
	Time int64
	// Cause is what appended it: the command's id, or "processor:" and the
	// processor's name.
	Cause string
}

// Clone is s with its own copies of the event's slices (Event.Clone).
func (s Stored) Clone() Stored {
	s.Event = s.Event.Clone()
	return s
}

// Meta is a stored event's envelope, as folds and handlers see it.
type Meta struct {
	Tenant string
	Pos    Position
	Time   int64 // Unix milliseconds
	Cause  string
}

// Meta is the stored event's envelope.
func (s Stored) Meta() Meta { return Meta{Tenant: s.Tenant, Pos: s.Pos, Time: s.Time, Cause: s.Cause} }

// A Selector selects events: of one of Types, carrying every tag of Tags,
// after position After, newest first if Reverse, at most Limit (0: all).
// A selector with no types matches nothing (a read of the head alone).
// It is the raw filter a store evaluates; an application reads through
// grist.Reads, which checks each type and tag key against its App.
type Selector struct {
	Types   []string
	Tags    []TagPair
	After   Position
	Reverse bool
	Limit   int
}

// Matches reports whether e is of one of the selector's types and carries
// its tags (After, Reverse and Limit select among matches by position;
// they do not change what matches).
func (q Selector) Matches(e Event) bool {
	ok := false
	for _, t := range q.Types {
		if t == e.Type {
			ok = true
			break
		}
	}
	return ok && e.Tagged(q.Tags)
}

func (q Selector) String() string {
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

// Store is grist's event store: one log per tenant, appends serialized
// per tenant, conditions and markers checked under the tenant's lock.
// It is grist's own interface (no aggregates: a condition is a set of
// selectors through a position). Every implementation passes
// grist/storetest's contract suite. Methods added later are optional
// interfaces (StepLog), found by a type assertion, so a store written
// against this one keeps compiling; a node (grist.App.Start) needs a
// StepLog, since it records commits from it.
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
	// The events are the reader's own copies (Stored.Clone).
	Read(ctx context.Context, tenant string, q Selector, at Position) ([]Stored, Position, error)
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

// A StepLog is a store that keeps each committed batch's Step in the
// batch's own transaction, readable after the commit: what makes a
// commit's step durable where no hook can run at the commit (a database
// whose COMMIT returns and whose process then dies before the hook, a
// database/sql store). Every tier keeps one (memstore, simstore,
// sqlstore's grist_steps rows), and grist records every commit's step
// from it, never from Batch.OnCommit (grist.Relay; docs/grist.md §2.10,
// §3.1).
type StepLog interface {
	// Steps are tenant's step rows after seq (0: from the first), in commit
	// order, at most limit (0: all).
	Steps(ctx context.Context, tenant string, after int64, limit int) ([]StepRow, error)
	// WaitSteps blocks until tenant's step log has a row after seq, or ctx
	// is done. A refusal commits its marker and its step without moving
	// the head, so Store.Wait would sleep through it.
	WaitSteps(ctx context.Context, tenant string, after int64) error
	// StepCount is how many step rows tenant's log has (the last row's
	// Seq): a relay's distance behind, without reading the rows.
	StepCount(ctx context.Context, tenant string) (int64, error)
}

// A TenantFeed is a store that says when a tenant is created: a node
// (grist.App.Start) relays and handles a tenant created after it
// started, waking on WaitTenants rather than polling (which in a
// simulation would keep a settled system busy). Every tier implements
// it, and a node needs it.
type TenantFeed interface {
	// WaitTenants blocks until the store has more than n tenants, or ctx
	// is done. Tenants are never deleted, so a count is a version.
	WaitTenants(ctx context.Context, n int) error
}

// A StepRow is one committed batch's step as a StepLog keeps it.
type StepRow struct {
	// Seq numbers the tenant's step rows from 1, in commit order.
	Seq int64
	// Head is the head after the batch: what its step's head observation
	// and its order on the tenant's clock are computed from.
	Head Position
	// Time is the commit's time, Unix nanoseconds, taken by the store
	// under the tenant's lock (the simulation's true time in one, else
	// the wall clock's): rows in Seq order are in time order, so a relay
	// recording them in commit order records them in time order too.
	Time int64
	// Step is the batch's Step, as given.
	Step json.RawMessage
}

// StepTime is the time a store stamps a step row with (StepRow.Time),
// under the tenant's lock: the simulation's true time inside one (as qobs
// stamps a step recorded there, never a process's skewed clock), else the
// wall clock's, Unix nanoseconds.
func StepTime(ctx context.Context) int64 {
	if sim := qenv.From(ctx); sim != nil {
		return sim.Now().UnixNano()
	}
	return time.Now().UnixNano() //qenv:ok outside a simulation: a step's time is the wall clock's, as qobs stamps it
}

// A Condition is DCB's: no event matching any of Selectors appended after
// After.
type Condition struct {
	After     Position
	Selectors []Selector
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
	// Step, if set, is the step the batch's commit records (JSON, opaque
	// to the store). A StepLog writes it in the batch's transaction, and
	// grist records the step from that row: there is no commit hook, since
	// a database/sql store's process can die between its COMMIT and any
	// hook (slice 1's Batch.OnCommit, which only a simulation could run
	// even when the caller was told the outcome is unknown).
	Step json.RawMessage
}

// A Marker is a key's committed outcome: the hash of the command (its
// name, version and canonical arguments) and its answer, under its id. A
// commit's marker holds its receipt; a refusal's (a named rejection, a
// conflict) its Outcome, so a key's first answer is its last: a late
// duplicate finds the marker and gets the same answer.
type Marker struct {
	ID   string
	Hash string
	// Outcome is "" for a commit, else the refusal's name.
	Outcome string
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
// matching Selector was appended after the decision's position (Event),
// or the batch's marker exists already (Marker). "Decide again", or
// read the marker.
type ConditionFailed struct {
	Selector Selector
	Event    Position
	Marker   string
}

func (e *ConditionFailed) Error() string {
	if e.Marker != "" {
		return fmt.Sprintf("grist: condition failed: marker %s exists (the key has its outcome already)", e.Marker)
	}
	return fmt.Sprintf("grist: condition failed: event %d matches %s, which the decision read before it", e.Event, e.Selector)
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

// PermanentError is a store error no retry can fix (a statement the
// database cannot run, a missing table): Transient is false for it, so
// grist returns it at once instead of spending its transient budget and
// answering "outcome unknown". A tier wraps what it knows to be such
// (Permanent); anything it does not know stays transient.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string {
	return "grist: store: " + e.Err.Error() + " (permanent: no retry fixes it)"
}

// Unwrap is Err.
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as a *PermanentError (nil stays nil).
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// NotCommittedError is a transient error an Append returned before it
// tried to commit (a lost race for the tenant's lock, a lock wait that
// timed out, a connection lost before COMMIT): nothing of the batch
// committed, so the caller knows the outcome, unlike a transient error at
// or after the commit, which may have committed. A tier marks what it
// knows to be such (NotCommitted); an unmarked transient error is
// outcome unknown.
type NotCommittedError struct{ Err error }

func (e *NotCommittedError) Error() string {
	return e.Err.Error() + " (before the commit: nothing committed)"
}

// Unwrap is Err.
func (e *NotCommittedError) Unwrap() error { return e.Err }

// NotCommitted wraps err as a *NotCommittedError (nil stays nil, and an
// error no retry fixes stays as it is, since its caller does not retry).
func NotCommitted(err error) error {
	var pe *PermanentError
	if err == nil || errors.As(err, &pe) {
		return err
	}
	return &NotCommittedError{Err: err}
}

// MayHaveCommitted reports whether a failed Append may have committed:
// false for an error before the commit (a *NotCommittedError), a failed
// condition, a moved checkpoint, an unknown tenant or a permanent error;
// true for any other, a commit whose answer was lost or a context
// cancelled while it committed included.
func MayHaveCommitted(err error) bool {
	var cf *ConditionFailed
	var cm *CheckpointMoved
	var pe *PermanentError
	var nc *NotCommittedError
	return err != nil && !errors.As(err, &cf) && !errors.As(err, &cm) && !errors.As(err, &pe) && !errors.As(err, &nc) &&
		!errors.Is(err, ErrUnknownTenant)
}

// ErrUnknownTenant is a read or append of a tenant never created.
var ErrUnknownTenant = errors.New("grist: unknown tenant: create it first (Store.CreateTenant)")

// Transient reports whether err is a store error worth retrying: neither
// a failed condition nor a moved checkpoint nor an unknown tenant nor a
// permanent error nor a canceled context.
func Transient(err error) bool {
	var cf *ConditionFailed
	var cm *CheckpointMoved
	var pe *PermanentError
	return err != nil && !errors.As(err, &cf) && !errors.As(err, &cm) && !errors.As(err, &pe) &&
		!errors.Is(err, ErrUnknownTenant) && !errors.Is(err, context.Canceled)
}

// CheckCondition is the reference evaluation of c over a tenant's whole
// log (events in position order): the first event after c.After matching
// one of its selectors, as a *ConditionFailed, or nil. Stores may check
// it with indexes; storetest checks them against this.
func CheckCondition(log []Stored, c Condition) error {
	for _, e := range log {
		if e.Pos <= c.After {
			continue
		}
		for _, q := range c.Selectors {
			if q.Matches(e.Event) {
				return &ConditionFailed{Selector: q, Event: e.Pos}
			}
		}
	}
	return nil
}

// Select is the reference evaluation of a Read over a tenant's whole log
// (events in position order): q's matches through at, in q's order and
// limit, each the caller's own copy.
func Select(log []Stored, q Selector, at Position) []Stored {
	out := []Stored{}
	for _, e := range log {
		if e.Pos > q.After && e.Pos <= at && q.Matches(e.Event) {
			out = append(out, e.Clone())
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
