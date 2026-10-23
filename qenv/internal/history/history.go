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
// read), forbidden at every level. A cycle with no rw edge is G1c (G0 if every edge is ww): forbidden from
// read committed up. A cycle with exactly one rw edge is G-single (a lost
// update, read skew): forbidden from snapshot isolation up. Any cycle is
// G2: forbidden under serializability; snapshot isolation allows it with
// two rw edges or more (write skew). The checker is incremental: every
// new cycle goes through the transaction that closed it, so each commit
// is checked once, by a search from it.
//
// A store's own tests show the anomalies its level allows can happen
// (completeness); this shows those it forbids never do (soundness).
package history

import (
	"fmt"
	"sort"
	"strings"
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

// MaxRW is how many rw edges a forbidden cycle may have, by level: a
// cycle with at most MaxRW of them violates the level.
const (
	ReadCommitted     = 0
	SnapshotIsolation = 1
	Serializable      = 1 << 30
)

type edge struct {
	to   int64
	kind string // "ww", "wr", "rw"
	key  string
}

// History is a store's committed transactions and the dependencies
// between them.
type History struct {
	maxRW    int
	versions map[string][]Version // per key, in order
	writer   map[Version]int64    // version -> the transaction that installed it
	readers  map[string]map[Version][]int64
	scans    []scanOf
	out      map[int64][]edge
}

type scanOf struct {
	tx int64
	Scan
}

// New returns an empty history checked at level (ReadCommitted,
// SnapshotIsolation or Serializable).
func New(level int) *History {
	return &History{maxRW: level, versions: map[string][]Version{}, writer: map[Version]int64{},
		readers: map[string]map[Version][]int64{}, out: map[int64][]edge{}}
}

// Load records a version installed outside any transaction (initial data).
func (h *History) Load(key string, v Version) {
	h.versions[key] = append(h.versions[key], v)
	h.writer[v] = 0 // the initial "transaction"
}

func (h *History) add(from, to int64, kind, key string) {
	if from == to {
		return
	}
	h.out[from] = append(h.out[from], edge{to, kind, key})
}

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

// cycle finds the cycle through id with the fewest rw edges (a 0-1
// breadth-first search, rw edges weighing 1) and reports it if the level
// forbids it.
func (h *History) cycle(id int64) error {
	type step struct {
		prev int64
		e    edge
	}
	dist := map[int64]int{}
	via := map[int64]step{}
	type item struct {
		n int64
		d int
	}
	var deque []item
	push := func(it item, front bool) {
		if front {
			deque = append([]item{it}, deque...)
		} else {
			deque = append(deque, it)
		}
	}
	best, bestVia := -1, step{}
	for _, e := range h.out[id] {
		w := 0
		if e.kind == "rw" {
			w = 1
		}
		if e.to == id {
			continue
		}
		if d, ok := dist[e.to]; !ok || w < d {
			dist[e.to], via[e.to] = w, step{id, e}
			push(item{e.to, w}, w == 0)
		}
	}
	for len(deque) > 0 {
		it := deque[0]
		deque = deque[1:]
		if it.d != dist[it.n] {
			continue
		}
		if it.d > h.maxRW || best >= 0 && it.d >= best {
			continue
		}
		for _, e := range h.out[it.n] {
			w := it.d
			if e.kind == "rw" {
				w++
			}
			if e.to == id {
				if best < 0 || w < best {
					best, bestVia = w, step{it.n, e}
				}
				continue
			}
			if d, ok := dist[e.to]; !ok || w < d {
				dist[e.to], via[e.to] = w, step{it.n, e}
				push(item{e.to, w}, w == it.d)
			}
		}
	}
	if best < 0 || best > h.maxRW {
		return nil
	}
	// the cycle, from id back to id
	var path []string
	allWW := true
	cur, st := id, bestVia
	for len(path) <= len(h.out) {
		path = append(path, fmt.Sprintf("T%d -%s(%s)-> T%d", st.prev, st.e.kind, st.e.key, st.e.to))
		if st.e.kind != "ww" {
			allWW = false
		}
		cur = st.prev
		if cur == id {
			break
		}
		st = via[cur]
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	name := "G2"
	switch {
	case best == 0 && allWW:
		name = "G0 (dirty write)"
	case best == 0:
		name = "G1c (circular information flow)"
	case best == 1:
		name = "G-single (a lost update or read skew)"
	}
	return &Violation{Phenomenon: name, Cycle: path}
}

// A Violation is a forbidden cycle.
type Violation struct {
	Phenomenon string
	Cycle      []string // its edges, from the transaction that closed it
}

func (v *Violation) Error() string {
	return fmt.Sprintf("%s: %s", v.Phenomenon, strings.Join(v.Cycle, ", "))
}
