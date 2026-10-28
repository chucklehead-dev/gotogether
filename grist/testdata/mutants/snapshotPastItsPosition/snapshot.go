package grist

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// DefaultSnapshotEvery is how many events a read folds before it takes a
// snapshot, by default (SnapshotEvery).
const DefaultSnapshotEvery = 1000

// snapshotEveryKey carries SnapshotEvery's n.
type snapshotEveryKey struct{}

// SnapshotEvery is ctx with reads taking a read model's snapshot once
// they folded n events past the last one (DefaultSnapshotEvery without
// it); n <= 0 turns snapshots off: every read folds from the start of the
// log, and none is taken. A read under ctx (a command's, a query's, a
// processor's: App.Start's tasks run with their context) uses it; a
// store that keeps no snapshots (no store.SnapshotStore) has none.
// gristsim's Config.SnapshotEvery sets it for a simulation.
func SnapshotEvery(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, snapshotEveryKey{}, n)
}

func snapshotEvery(ctx context.Context) int {
	if n, ok := ctx.Value(snapshotEveryKey{}).(int); ok {
		return n
	}
	return DefaultSnapshotEvery
}

// snapshotSum is a snapshot's checksum: a hash of every field but Sum, so
// a torn or altered one is never read.
func snapshotSum(s Snapshot) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00", s.Name, s.Key, s.Fingerprint, s.Pos)
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

// get is the read model's state for tags through r's position: its
// latest snapshot within that position, if it has a sound one, and the
// events after it; else every event. Either way the read set is the read
// model's whole selector (a condition on any of its events appended after
// the position), so a snapshot changes what a read costs, never what it
// conditions or answers. A read that folded SnapshotEvery's events or
// more takes a snapshot at its position (a cache: a failed put is
// ignored).
//
// Only a read that folded SnapshotEvery's events takes a snapshot, so a
// read model with fewer events has none: the read's first SnapshotEvery
// events say which (and pin the position, as the read would), and a
// short log's read is that one read, as it is with snapshots off (a
// simulation's schedule unchanged by them). A long log's costs those
// events, the snapshot and the events after it: about twice
// SnapshotEvery's at most, whatever the log's length.
func (m *ReadModel[S]) get(r *Reads, tags []TagPair) S {
	return m.getWith(r, tags, snapshotEvery(r.ctx), false)
}

// getWith is get with snapshots every events; check is CheckSnapshots'
// read, which looks for a snapshot whatever the log's length and takes
// none.
func (m *ReadModel[S]) getWith(r *Reads, tags []TagPair, every int, check bool) S {
	q := m.selector(tags...)
	ss, ok := r.store.(SnapshotStore)
	fp, survives := m.info.fp()
	if !ok || every <= 0 || !survives || r.err != nil || r.bad != nil {
		return m.foldRead(r, q, m.init())
	}
	var first []Stored
	if !check {
		short := q
		short.Limit = every
		if first = r.read(short); len(first) < every || r.err != nil || r.bad != nil {
			return m.foldEvents(r, m.init(), first)
		}
	}
	key := snapshotKey(tags)
	snap, found, err := ss.GetSnapshot(r.ctx, r.tenant, m.info.name, key, fp)
	intact := err == nil && found && snap.Sum == snapshotSum(snap)
	s := m.init()
	sound := intact && snap.Fingerprint == fp && json.Unmarshal(snap.State, &s) == nil &&
		(!r.pinned || snap.Pos <= r.pos) // past the read's position: a log restored to an earlier point
	var evs []Stored
	folded := 0
	if sound {
		q.After = snap.Pos
		evs = r.read(q)
		if snap.Pos > r.pos { // CheckSnapshots' read, pinned by this one
			sound = false
		}
	}
	if !sound {
		s, q.After = m.init(), 0
		if len(first) > 0 {
			if s = m.foldEvents(r, s, first); r.bad != nil {
				return s
			}
			q.After, folded = first[len(first)-1].Pos, len(first)
		}
		evs = r.read(q)
	}
	folded += len(evs)
	if s = m.foldEvents(r, s, evs); r.bad != nil {
		return s
	}
	if sound && !check && audited(r.ctx, snap, r.pos) {
		// refold from the start, through the same position: the same
		// selector, so the read set is what it was
		all := m.selector(tags...)
		full := m.foldRead(r, all, m.init())
		if r.err != nil || r.bad != nil {
			return full
		}
		r.app.audits.Add(1)
		a, _ := json.Marshal(s)
		b, _ := json.Marshal(full)
		if !bytes.Equal(a, b) {
			r.app.auditMisses.Add(1)
			m.save(r, ss, key, fp, full)
			return full
		}
	}
	// never replace an intact later snapshot with this earlier one (a
	// processor reading behind the head)
	if !check && folded >= every && r.err == nil && !(intact && snap.Pos >= r.pos) {
		m.save(r, ss, key, fp, s)
	}
	return s
}

// foldEvents folds evs onto s, a failure the read's misuse.
func (m *ReadModel[S]) foldEvents(r *Reads, s S, evs []Stored) S {
	s, err := m.foldOnto(s, evs)
	if err != nil {
		r.misuse("%w", err)
	}
	return s
}

// foldRead folds q's events through r's position onto s.
func (m *ReadModel[S]) foldRead(r *Reads, q Selector, s S) S { return m.foldEvents(r, s, r.read(q)) }

// save stores s, the read model's state for key folded through r's
// position, as its snapshot there.
func (m *ReadModel[S]) save(r *Reads, ss SnapshotStore, key, fp string, s S) {
	state, err := json.Marshal(s)
	if err != nil {
		return // the probe found it survives JSON
	}
	// mutant: the snapshot labelled with the next position to read, not
	// the last one folded (an off-by-one between "through" and "after")
	snap := Snapshot{Name: m.info.name, Key: key, Fingerprint: fp, Pos: r.pos + 1, State: state}
	snap.Sum = snapshotSum(snap)
	ss.PutSnapshot(r.ctx, r.tenant, snap) // a cache: a lost put costs the next read a longer fold
}
