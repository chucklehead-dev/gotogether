// Package history checks a transactional store's committed history
// against its isolation level, in Adya's terms: each committed
// transaction's reads (the versions it saw) and writes (the versions it
// installed) give dependencies between transactions, and each level
// forbids some cycles of them.
//
//   - ww: T2 installed the version after T1's of a key;
//   - wr: T2 read a version T1 installed;
//   - rw (anti-dependency): T2 installed the version after the one T1
//     read, or a version of a key matching a prefix T1 scanned, after
//     T1's view of it.
//
// A read of a version no committed transaction installed is G1a (a dirty
// read), forbidden at every level. A cycle is classified as qhistory
// classifies a real database's (package internal/depgraph, one
// classifier for both): G0 and G1c (no rw edge) are forbidden from read
// committed up; G-single (one rw edge: a lost update, read skew) and
// G-nonadjacent (two or more, never two in a row) from snapshot isolation
// up; G2 (write skew, whose rw edges are adjacent) under serializability.
// The checker is incremental: every new cycle goes through the
// transaction that closed it, so each commit classifies only that
// transaction's strongly connected component (an older forbidden cycle
// there would have failed its own commit).
//
// A store's own tests show the anomalies its level allows can happen
// (completeness); this shows those it forbids never do (soundness).
package history

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/depgraph"
)

// Version identifies a key's version: the commit number of the
// transaction that installed it, 0 for "never written".
type Version int64

// A Read is a key and the version a transaction saw.
type Read struct {
	Key     string
	Version Version
}

// A Scan is a prefix a transaction read every key of, as of Limit: it saw,
// for each key with the prefix, the last version at or before Limit
// (none: the key did not exist yet).
type Scan struct {
	Prefix string
	Limit  Version
}

// A Txn is a committed transaction.
type Txn struct {
	ID     int64
	Commit Version // the version its writes installed (0: it wrote nothing)
	Reads  []Read
	Scans  []Scan
	Writes []string
}

// The levels a history is checked at, weakest first.
const (
	ReadCommitted = iota
	SnapshotIsolation
	Serializable
)

// History is a store's committed transactions and the dependencies
// between them.
type History struct {
	level    int
	versions map[string][]Version // per key, in order
	writer   map[Version]int64    // version -> the transaction that installed it
	readers  map[string]map[Version][]int64
	scans    []scanOf
	g        *depgraph.Graph
}

type scanOf struct {
	tx int64
	Scan
}

// New returns an empty history checked at level (ReadCommitted,
// SnapshotIsolation or Serializable).
func New(level int) *History {
	return &History{level: level, versions: map[string][]Version{}, writer: map[Version]int64{},
		readers: map[string]map[Version][]int64{}, g: depgraph.New()}
}

// Load records a version installed outside any transaction (initial data).
func (h *History) Load(key string, v Version) {
	h.versions[key] = append(h.versions[key], v)
	h.writer[v] = 0 // the initial "transaction"
}

func (h *History) add(from, to int64, kind, key string) { h.g.Add(from, to, kind, key) }

// next is the version of key installed after v, if any.
func (h *History) next(key string, v Version) (Version, bool) {
	vs := h.versions[key]
	i := sort.Search(len(vs), func(i int) bool { return vs[i] > v })
	if i < len(vs) {
		return vs[i], true
	}
	return 0, false
}

// at is the last version of key at or before limit (0: none).
func (h *History) at(key string, limit Version) Version {
	vs := h.versions[key]
	i := sort.Search(len(vs), func(i int) bool { return vs[i] > limit })
	if i == 0 {
		return 0
	}
	return vs[i-1]
}

// Commit adds t to the history and returns the cycle it closed that the
// level forbids, or nil.
func (h *History) Commit(t Txn) error {
	// reads: wr from the version's writer, and rw to the writer of the
	// version after it, if one is already installed
	reads := append([]Read(nil), t.Reads...)
	for _, s := range t.Scans {
		for key := range h.versions {
			if strings.HasPrefix(key, s.Prefix) {
				reads = append(reads, Read{key, h.at(key, s.Limit)})
			}
		}
	}
	sort.Slice(reads, func(i, j int) bool { return reads[i].Key < reads[j].Key })
	for _, r := range reads {
		if _, ok := h.writer[r.Version]; r.Version != 0 && !ok {
			return &Violation{Phenomenon: "G1a (a dirty read)", Cycle: []string{fmt.Sprintf("T%d read version %d of %s, which no committed transaction installed", t.ID, r.Version, r.Key)}}
		}
		if r.Version != 0 {
			h.add(h.writer[r.Version], t.ID, "wr", r.Key)
		}
		if n, ok := h.next(r.Key, r.Version); ok && n != t.Commit {
			h.add(t.ID, h.writer[n], "rw", r.Key)
		}
		if h.readers[r.Key] == nil {
			h.readers[r.Key] = map[Version][]int64{}
		}
		h.readers[r.Key][r.Version] = append(h.readers[r.Key][r.Version], t.ID)
	}
	// writes: ww from the previous version's writer, rw from those who
	// read it (or scanned the key's prefix before this version)
	if t.Commit != 0 {
		h.writer[t.Commit] = t.ID
		writes := append([]string(nil), t.Writes...)
		sort.Strings(writes)
		for _, key := range writes {
			prev := h.at(key, t.Commit-1)
			if prev != 0 {
				h.add(h.writer[prev], t.ID, "ww", key)
			}
			for _, r := range h.readers[key][prev] {
				h.add(r, t.ID, "rw", key)
			}
			if prev == 0 { // a new key: a phantom for earlier scans of its prefix
				for _, s := range h.scans {
					if strings.HasPrefix(key, s.Prefix) && s.Limit < t.Commit {
						h.add(s.tx, t.ID, "rw", key)
					}
				}
			}
			vs := append(h.versions[key], t.Commit)
			sort.Slice(vs, func(i, j int) bool { return vs[i] < vs[j] })
			h.versions[key] = vs
		}
	}
	for _, s := range t.Scans {
		h.scans = append(h.scans, scanOf{t.ID, s})
	}
	return h.cycle(t.ID)
}

// phenomena names each anomaly as a violation reports it, with the
// weakest level that forbids it.
var phenomena = map[string]struct {
	name  string
	level int
}{
	depgraph.G0:           {"G0 (dirty write)", ReadCommitted},
	depgraph.G1c:          {"G1c (circular information flow)", ReadCommitted},
	depgraph.GSingle:      {"G-single (a lost update or read skew)", SnapshotIsolation},
	depgraph.GNonadjacent: {"G-nonadjacent (anti-dependencies, never two in a row)", SnapshotIsolation},
	depgraph.G2:           {"G2", Serializable},
}

// cycle classifies id's strongly connected component and reports its
// strongest cycle if the level forbids it, from id when id is on it.
func (h *History) cycle(id int64) error {
	scc := h.g.Component(id)
	if scc == nil {
		return nil
	}
	kind, cyc := h.g.Classify(scc)
	p := phenomena[kind]
	if p.level > h.level {
		return nil
	}
	if i := slices.IndexFunc(cyc, func(e depgraph.Hop) bool { return e.From == id }); i > 0 {
		cyc = append(cyc[i:], cyc[:i]...)
	}
	path := make([]string, len(cyc))
	for i, e := range cyc {
		path[i] = fmt.Sprintf("T%d -%s(%s)-> T%d", e.From, e.Kind, e.Key, e.To)
	}
	return &Violation{Phenomenon: p.name, Cycle: path}
}

// A Violation is a forbidden cycle.
type Violation struct {
	Phenomenon string
	Cycle      []string // its edges, from the transaction that closed it if it is on it
}

func (v *Violation) Error() string {
	return fmt.Sprintf("%s: %s", v.Phenomenon, strings.Join(v.Cycle, ", "))
}
