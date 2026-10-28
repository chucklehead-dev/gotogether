package grist

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"weak"
)

// The memo is a node's process-local cache of read models' states: what
// a read folded, kept by (tenant, read model, key, fingerprint) with the
// position and identity of its last folded event (its anchor), one table
// per store. A read of a state it holds reads the read model's events
// from the anchor on: the anchor first, which must be the event the
// state folded (a log rewound and refilled, a failover's lost tail or a
// restore, gives the position another event, or none: the state is
// dropped and the read folds as it would without it), then the events
// after it. The read set is the read model's whole selector either way,
// as a snapshot's is, so the memo changes what a read costs, never what
// it conditions. Tables are per store, so two simulations of one App in
// parallel never share one, and a table goes with its store (a weak
// reference).
type memos struct {
	mu     sync.Mutex // tables only: held reaching no seam
	tables map[weak.Pointer[byte]]*memoTable
}

// A memoKey is a memoized state's key.
type memoKey struct{ tenant, name, key, fp string }

// A memoEntry is a memoized state: the read model's state as JSON after
// folding its events through anchor (the last, whose identity is id), and
// since, the events folded since a snapshot was taken or read (a read
// that brings it past Snapshots' every takes one).
type memoEntry struct {
	k      memoKey
	anchor Position
	id     string
	state  []byte
	since  int
}

// A memoTable is one store's memo: at most its size's entries, the least
// recently used evicted first.
type memoTable struct {
	mu  sync.Mutex // held reaching no seam
	m   map[memoKey]*list.Element
	lru list.List
}

// of is st's memo table, nil when st is no pointer (a store value has no
// identity to key one by).
func (ms *memos) of(st Store) *memoTable {
	v := reflect.ValueOf(st)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil
	}
	p := (*byte)(v.UnsafePointer())
	w := weak.Make(p)
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if t := ms.tables[w]; t != nil {
		return t
	}
	if ms.tables == nil {
		ms.tables = map[weak.Pointer[byte]]*memoTable{}
	}
	t := &memoTable{m: map[memoKey]*list.Element{}}
	ms.tables[w] = t
	runtime.AddCleanup(p, func(w weak.Pointer[byte]) {
		ms.mu.Lock()
		defer ms.mu.Unlock()
		delete(ms.tables, w)
	}, w)
	return t
}

// get is k's entry, if the table holds it.
func (t *memoTable) get(k memoKey) (memoEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	el := t.m[k]
	if el == nil {
		return memoEntry{}, false
	}
	t.lru.MoveToFront(el)
	return el.Value.(memoEntry), true
}

// put keeps e, evicting the least recently used past size entries.
func (t *memoTable) put(e memoEntry, size int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if el := t.m[e.k]; el != nil {
		el.Value = e
		t.lru.MoveToFront(el)
	} else {
		t.m[e.k] = t.lru.PushFront(e)
	}
	for t.lru.Len() > size {
		old := t.lru.Back()
		t.lru.Remove(old)
		delete(t.m, old.Value.(memoEntry).k)
	}
}

// drop forgets k.
func (t *memoTable) drop(k memoKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if el := t.m[k]; el != nil {
		t.lru.Remove(el)
		delete(t.m, k)
	}
}

// eventID is a stored event's identity: its type, version, tags, body,
// append time and cause, hashed. A position a rewind gave back and the
// log filled again holds another event (another command's, at a later
// time), so a state anchored at the position is seen to be stale.
func eventID(e Stored) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00%d\x00%s\x00", e.Type, e.Version, e.Time, e.Cause)
	for _, t := range e.Tags {
		fmt.Fprintf(h, "%s=%s\x00", t.Key, t.Value)
	}
	h.Write(e.Body)
	return hex.EncodeToString(h.Sum(nil))[:24]
}
