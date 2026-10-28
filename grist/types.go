package grist

import "github.com/chucklehead-dev/gotogether/grist/store"

// The store's types an application meets, under grist's name (the
// contract and the reference evaluations are package store's).
type (
	// Store is grist's event store (store.Store).
	Store = store.Store
	// Position is a place in a tenant's log (store.Position).
	Position = store.Position
	// TagPair is one tag of an event (store.TagPair).
	TagPair = store.TagPair
	// Event is an event to append (store.Event); build one with
	// EventType.Of.
	Event = store.Event
	// Stored is an event with its envelope (store.Stored).
	Stored = store.Stored
	// Meta is a stored event's envelope, as folds and handlers see it
	// (store.Meta).
	Meta = store.Meta
	// Selector is a raw selection of events (store.Selector), for
	// Reads.Select.
	Selector = store.Selector
	// Receipt is a committed command's result (store.Receipt).
	Receipt = store.Receipt
	// EventRef names a stored event (store.EventRef).
	EventRef = store.EventRef
	// Snapshot is a read model's state at a position (store.Snapshot).
	Snapshot = store.Snapshot
	// SnapshotStore is a store that keeps snapshots (store.SnapshotStore).
	SnapshotStore = store.SnapshotStore
)
