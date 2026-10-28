package grist

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/store"
)

// snapshotSum is a snapshot's checksum: a hash of every field but Sum,
// and of anchor, the identity (eventID) of the event at its position,
// the last it folded: a torn or altered snapshot is never read, nor one
// whose last event a rewound log no longer holds (a put of a fold begun
// before a restore and committed after it).
func snapshotSum(s Snapshot, anchor string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00%s\x00", s.Name, s.Key, s.Fingerprint, s.Pos, anchor)
	h.Write(s.State)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// snapshotKey is a keyed read's key: its tags as key=value pairs, each
// query-escaped, joined by '&'; "" for a plain read model.
func snapshotKey(tags []TagPair) string {
	parts := make([]string, len(tags))
	for i, t := range tags {
		parts[i] = url.QueryEscape(t.Key) + "=" + url.QueryEscape(t.Value)
	}
	return strings.Join(parts, "&")
}

// get is the read model's state for tags through r's position: from the
// memo (memo.go), else its latest sound snapshot within that position and
// the events after it, else every event. Either way the read set is the
// read model's whole selector (a condition on any of its events appended
// after the position), so the memo and snapshots change what a read
// costs, never what it conditions or answers. A snapshot is at the read
// model's last folded event and checked against it (snapshotSum), as a
// memoized state is: a rewound log invalidates both. A read that folded
// Snapshots' every events (since the last snapshot, for the memo's) takes
// a snapshot there (a cache: a failed put is counted, ReadStats).
//
// Only a read that folded every events takes a snapshot or memoizes, so
// a read model with fewer events has neither: the read's first every
// events say which (and pin the position, as the read would), and a
// short log's read is that one read, as it is with snapshots off (a
// simulation's schedule unchanged by them). A long log's costs those
// events, the snapshot and the events after it: about twice every's at
// most, whatever the log's length; from the memo, the events since.
func (m *ReadModel[S]) get(r *Reads, tags []TagPair) S {
	return m.getWith(r, tags, r.app.snapshotEvery(r.ctx), false)
}

// getWith is get with snapshots every events; check is CheckSnapshots'
// read, which looks for a snapshot whatever the log's length, takes none
// and reads no memo.
func (m *ReadModel[S]) getWith(r *Reads, tags []TagPair, every int, check bool) S {
	q := m.selector(tags...)
	fp, survives := m.info.fp()
	if every <= 0 || !survives || r.err != nil || r.bad != nil {
		return m.foldRead(r, q, m.init())
	}
	key := snapshotKey(tags)
	var memo *memoTable
	mk := memoKey{tenant: r.tenant, name: m.info.name, key: key, fp: fp}
	size := r.app.memoEntries(r.ctx)
	if !check && size > 0 {
		memo = r.app.memos.of(r.store)
	}
	if memo != nil {
		if s, ok := m.fromMemo(r, memo, mk, q, every, size); ok {
			return s
		}
		if r.err != nil || r.bad != nil {
			return m.init()
		}
	}
	ss, ok := r.store.(SnapshotStore)
	var first []Stored
	if !check {
		short := q
		short.Limit = every
		if first = r.read(short); len(first) < every || r.err != nil || r.bad != nil {
			return m.foldEvents(r, m.init(), first)
		}
	}
	var snap Snapshot
	found, sound := false, false
	s := m.init()
	var evs []Stored
	var anchor *Stored // the last event folded
	if ok {
		var err error
		snap, found, err = ss.GetSnapshot(r.ctx, r.tenant, m.info.name, key, fp)
		found = found && err == nil
		if found && snap.Fingerprint == fp && snap.Pos >= 1 && (!r.pinned || snap.Pos <= r.pos) {
			// from its last event on: that event first, the snapshot's anchor
			from := q
			from.After = snap.Pos - 1
			evs = r.read(from)
			if r.err != nil || r.bad != nil {
				return s
			}
			if len(evs) > 0 && evs[0].Pos == snap.Pos && snap.Pos <= r.pos && snap.Sum == snapshotSum(snap, eventID(evs[0])) &&
				json.Unmarshal(snap.State, &s) == nil {
				sound, anchor, evs = true, &evs[0], evs[1:]
			}
		}
		if !check {
			if sound {
				r.app.stats.snapshotHits.Add(1)
			} else {
				r.app.stats.snapshotMisses.Add(1)
			}
		}
	}
	folded := 0
	if !sound {
		s, q.After = m.init(), 0
		if len(first) > 0 {
			if s = m.foldEvents(r, s, first); r.bad != nil {
				return s
			}
			q.After, folded, anchor = first[len(first)-1].Pos, len(first), &first[len(first)-1]
		}
		evs = r.read(q)
	}
	folded += len(evs)
	if s = m.foldEvents(r, s, evs); r.bad != nil || r.err != nil {
		return s
	}
	if len(evs) > 0 {
		anchor = &evs[len(evs)-1]
	}
	if anchor == nil {
		return s // nothing folded: nothing to keep
	}
	if sound && !check && r.app.audited(r.ctx, m.info.name, key, snap.Pos, r.pos) {
		if full, same := m.audit(r, m.selector(tags...), s); !same {
			m.save(r, ss, key, fp, full, *anchor)
			if memo != nil {
				memo.drop(mk)
			}
			return full
		}
	}
	since := folded
	// never replace a later snapshot with this earlier one (a processor
	// reading behind the head)
	later := found && snap.Fingerprint == fp && (snap.Pos > r.pos || sound && snap.Pos >= anchor.Pos)
	if ok && !check && folded >= every && !later {
		m.save(r, ss, key, fp, s, *anchor)
		since = 0
	}
	if memo != nil && (sound || folded >= every) {
		m.memoize(memo, mk, s, *anchor, since, size)
	}
	return s
}

// fromMemo is the read model's state from the memo, if memo holds a
// sound one for mk: its anchor still the log's (rewound and refilled, it
// is not, and the state is dropped), within the read's position; the
// events after the anchor folded onto it. ok false: the read goes on as
// without a memo (r's position may be pinned by the anchor's read).
func (m *ReadModel[S]) fromMemo(r *Reads, memo *memoTable, mk memoKey, q Selector, every, size int) (S, bool) {
	var zero S
	e, ok := memo.get(mk)
	if !ok || r.pinned && e.anchor > r.pos {
		return zero, false
	}
	from := q
	from.After = e.anchor - 1
	evs := r.read(from)
	if r.err != nil || r.bad != nil {
		return zero, false
	}
	s := m.init()
	if len(evs) == 0 || evs[0].Pos != e.anchor || eventID(evs[0]) != e.id || json.Unmarshal(e.state, &s) != nil {
		memo.drop(mk)
		r.app.stats.memoStale.Add(1)
		return zero, false
	}
	tail := evs[1:]
	if s = m.foldEvents(r, s, tail); r.bad != nil {
		return s, true
	}
	r.app.stats.memoHits.Add(1)
	anchor := evs[len(evs)-1]
	if r.app.audited(r.ctx, mk.name, mk.key, e.anchor, r.pos) {
		all := q
		all.After = 0
		if full, same := m.audit(r, all, s); !same {
			memo.drop(mk)
			return full, true
		}
	}
	since := e.since + len(tail)
	if ss, ok := r.store.(SnapshotStore); ok && since >= every {
		m.save(r, ss, mk.key, mk.fp, s, anchor)
		since = 0
	}
	if len(tail) > 0 || since != e.since {
		m.memoize(memo, mk, s, anchor, since, size)
	}
	return s, true
}

// audit refolds q (the read model's selector, from the start) through
// r's position, the read set unchanged, and compares it with s, counting
// the audit (AuditSnapshots): the refold, and whether s was it (true when
// the refold failed: the read's error says why).
func (m *ReadModel[S]) audit(r *Reads, q Selector, s S) (S, bool) {
	full := m.foldRead(r, q, m.init())
	if r.err != nil || r.bad != nil {
		return s, true
	}
	r.app.stats.audits.Add(1)
	a, _ := json.Marshal(s)
	b, _ := json.Marshal(full)
	if bytes.Equal(a, b) {
		return s, true
	}
	r.app.stats.auditMisses.Add(1)
	return full, false
}

// memoize keeps s, the state through anchor, in memo under mk.
func (m *ReadModel[S]) memoize(memo *memoTable, mk memoKey, s S, anchor Stored, since, size int) {
	state, err := json.Marshal(s)
	if err != nil {
		return // the probe found it survives JSON
	}
	memo.put(memoEntry{k: mk, anchor: anchor.Pos, id: eventID(anchor), state: state, since: since}, size)
}

// foldEvents folds evs onto s, a failure the read's misuse.
func (m *ReadModel[S]) foldEvents(r *Reads, s S, evs []Stored) S {
	r.app.stats.folded.Add(int64(len(evs)))
	s, err := m.foldOnto(s, evs)
	if err != nil {
		r.misuse("%w", err)
	}
	return s
}

// foldRead folds q's events through r's position onto s.
func (m *ReadModel[S]) foldRead(r *Reads, q Selector, s S) S { return m.foldEvents(r, s, r.read(q)) }

// save stores s, the read model's state for key folded through anchor
// (its last folded event), as its snapshot there, then prunes the key's
// snapshots of other fingerprints far behind it (pruneSnapshots). A put
// the store fails is counted (ReadStats): a cache, so the read goes on.
func (m *ReadModel[S]) save(r *Reads, ss SnapshotStore, key, fp string, s S, anchor Stored) {
	state, err := json.Marshal(s)
	if err != nil || ss == nil {
		return // the probe found it survives JSON
	}
	snap := Snapshot{Name: m.info.name, Key: key, Fingerprint: fp, Pos: anchor.Pos, State: state}
	snap.Sum = snapshotSum(snap, eventID(anchor))
	if err := ss.PutSnapshot(r.ctx, r.tenant, snap); err != nil {
		r.app.stats.putFailures.Add(1)
		return
	}
	r.app.pruneSnapshots(r, ss, snap)
}

// pruneLag is how far behind a put a snapshot of another fingerprint of
// the same key must be for the put to prune it, in Snapshots' every:
// during a rollout each release's nodes keep their own fingerprint's
// snapshots near the head of the keys they read, so one this far behind
// is a retired release's (or a key its nodes no longer read: pruning it
// costs one longer fold, never a wrong answer).
const pruneLag = 10

// pruneSnapshots deletes the snapshots of snap's read model and key held
// under other fingerprints and more than pruneLag × every events behind
// it, when the store can (store.SnapshotPruner).
func (a *App) pruneSnapshots(r *Reads, ss SnapshotStore, snap Snapshot) {
	p, ok := ss.(store.SnapshotPruner)
	every := a.snapshotEvery(r.ctx)
	if !ok || every <= 0 || snap.Pos <= Position(pruneLag*every) {
		return
	}
	n, err := p.PruneSnapshots(r.ctx, r.tenant, snap.Name, snap.Key, snap.Fingerprint, snap.Pos-Position(pruneLag*every))
	if err == nil {
		a.stats.pruned.Add(int64(n))
	}
}
