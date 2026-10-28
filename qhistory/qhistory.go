// Package qhistory checks a transactional database's history against an
// isolation level, from what its clients saw: Elle's list-append
// workload (Kingsbury and Alvaro, VLDB 2020) and Adya's anomalies, for a
// real database as qenv's Store checks its own commits.
//
// Each transaction appends unique values to lists, one per key, and reads
// whole lists. A database that appends atomically then gives away its
// version order: every committed read of a key is a prefix of the key's
// final list, and a value names the transaction that appended it. From
// the reads come the dependencies between transactions:
//
//   - ww: T2 appended the value after T1's in the key's order;
//   - wr: T2 read a list whose last value T1 appended;
//   - rw (anti-dependency): T2 appended the value after the last one T1
//     read.
//
// A cycle of them is an anomaly, named as Adya does: G0 (ww only), G1c
// (ww and wr), G-single (exactly one rw: read skew, a lost update),
// G-nonadjacent (two or more rw, never two in a row), G2 (any other
// cycle: write skew). With them, from the reads alone: G1a
// (a read saw an aborted transaction's value), G1b (a read saw a
// transaction's intermediate value), an incompatible order (two reads of
// a key that are not prefixes of one another), garbage (a value no
// transaction appended) and internal (a transaction's read misses its
// own appends), a duplicate (a value twice in one read). Read committed
// forbids all but G-single, G-nonadjacent and G2, snapshot isolation all
// but G2, serializability every one.
//
// Generate makes a workload; the test runs each transaction against the
// database, fills in each read's List and the transaction's Outcome, and
// Checks the history:
//
//	txns := qhistory.Workload{Keys: 3, Txns: 200, Seed: 1}.Generate()
//	/* run them, concurrently, against the database */
//	rep, err := qhistory.Check(qhistory.Serializable, txns)
package qhistory

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/depgraph"
)

// OpKind is what an operation does.
type OpKind int

const (
	// Append appends Value to Key's list.
	Append OpKind = iota
	// Read reads Key's whole list into List.
	Read
)

// An Op is one operation of a transaction.
type Op struct {
	Kind  OpKind
	Key   string
	Value int   // Append's value, unique per key across the history
	List  []int // Read's result, filled in by the run
}

func (o Op) String() string {
	if o.Kind == Append {
		return fmt.Sprintf("append(%s, %d)", o.Key, o.Value)
	}
	return fmt.Sprintf("read(%s) = %v", o.Key, o.List)
}

// Outcome is how a transaction ended, as its client saw it.
type Outcome int

const (
	// Unknown: the client never learned (a timeout, a lost connection). It
	// committed if a read saw one of its values, else it is left out.
	Unknown Outcome = iota
	// Committed: the commit succeeded.
	Committed
	// Aborted: it rolled back (or the commit failed): no read may see its
	// values.
	Aborted
)

// A Txn is one transaction of the history: its operations in the order
// it ran them, and its outcome. Invoked and Completed, when known (both,
// from one clock, the clients' or a simulation's), are when the client
// began it and learned its outcome: strict serializability orders a
// transaction completed before another was invoked first. An Unknown
// transaction's Completed is ignored, as Elle ignores an :info
// operation's: a client that timed out at t learned nothing, and the
// transaction may have committed after t (review 11, F4: a timeout taken
// as its completion made a G-single-realtime of a history that has none).
type Txn struct {
	ID                 int
	Ops                []Op
	Outcome            Outcome
	Invoked, Completed time.Time
}

// Level is the isolation level a history is checked against.
type Level int

const (
	// ReadCommitted (Adya's PL-2) forbids G0, G1a, G1b and G1c.
	ReadCommitted Level = iota
	// SnapshotIsolation also forbids G-single (read skew, lost updates).
	SnapshotIsolation
	// Serializable (PL-3) forbids every cycle, G2 (write skew) too.
	Serializable
	// StrictSerializable also forbids a cycle through real time: a
	// transaction that completed before another was invoked precedes it
	// (Txn.Invoked, Completed), so a read of a value older than one a
	// transaction completed before it began is a G-single-realtime (Elle's
	// names: the cycle's kind with "-realtime").
	StrictSerializable
)

func (l Level) String() string {
	return [...]string{"read committed", "snapshot isolation", "serializable", "strict serializability"}[l]
}

// An Anomaly is one thing the history shows that some level forbids.
type Anomaly struct {
	Kind string // G0, G1a, G1b, G1c, G-single, G-nonadjacent, G2, incompatible-order, duplicate, garbage, internal
	Txns []int  // the transactions involved (a cycle's, in its order)
	Msg  string // what was seen: the cycle's edges, the reads
	// Forbidden: the level checked forbids it (Report.OK fails); an
	// allowed one is reported for what it shows (write skew under
	// snapshot isolation).
	Forbidden bool
}

func (a Anomaly) String() string {
	f := "allowed"
	if a.Forbidden {
		f = "forbidden"
	}
	return fmt.Sprintf("%s (%s): %s", a.Kind, f, a.Msg)
}

// A Report is the anomalies a history shows, against a level.
type Report struct {
	Level     Level
	Anomalies []Anomaly
}

// OK reports whether no anomaly the level forbids was found.
func (r *Report) OK() bool {
	for _, a := range r.Anomalies {
		if a.Forbidden {
			return false
		}
	}
	return true
}

// Has reports whether the history shows an anomaly of kind, forbidden or
// not.
func (r *Report) Has(kind string) bool {
	return slices.ContainsFunc(r.Anomalies, func(a Anomaly) bool { return a.Kind == kind })
}

func (r *Report) String() string {
	if len(r.Anomalies) == 0 {
		return fmt.Sprintf("no anomaly (%s)", r.Level)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d anomalies (%s):", len(r.Anomalies), r.Level)
	for _, a := range r.Anomalies {
		fmt.Fprintf(&b, "\n  %s", a)
	}
	return b.String()
}

// forbidden reports whether level forbids an anomaly of kind.
func forbidden(level Level, kind string) bool {
	if strings.HasSuffix(kind, "-realtime") {
		return level >= StrictSerializable
	}
	switch kind {
	case "G-single":
		return level >= SnapshotIsolation
	case "G2":
		return level >= Serializable
	case "G-nonadjacent":
		return level >= SnapshotIsolation
	}
	return true
}

type wkey struct {
	key   string
	value int
}

// Check checks a history against level. It returns an error, not a
// report, when the history is not a list-append workload's: a value
// appended twice to one key (Generate never does), or an operation of
// another kind.
func Check(level Level, txns []Txn) (*Report, error) {
	rep := &Report{Level: level}
	add := func(kind string, ids []int, format string, a ...any) {
		rep.Anomalies = append(rep.Anomalies, Anomaly{Kind: kind, Txns: ids, Msg: fmt.Sprintf(format, a...), Forbidden: forbidden(level, kind)})
	}
	byID := map[int]*Txn{}
	writer := map[wkey]int{}   // a value's appender
	lastOwn := map[wkey]bool{} // a transaction's last append to the key
	for i := range txns {
		t := &txns[i]
		if t.ID == 0 {
			return nil, fmt.Errorf("qhistory: a transaction with id 0: ids must be nonzero (0 is no transaction)")
		}
		if _, dup := byID[t.ID]; dup {
			return nil, fmt.Errorf("qhistory: two transactions with id %d", t.ID)
		}
		byID[t.ID] = t
		last := map[string]int{}
		for _, o := range t.Ops {
			switch o.Kind {
			case Append:
				k := wkey{o.Key, o.Value}
				if w, dup := writer[k]; dup {
					return nil, fmt.Errorf("qhistory: value %d appended to %s by transactions %d and %d: values must be unique per key", o.Value, o.Key, w, t.ID)
				}
				writer[k] = t.ID
				last[o.Key] = o.Value
			case Read:
			default:
				return nil, fmt.Errorf("qhistory: transaction %d: an operation of kind %d", t.ID, o.Kind)
			}
		}
		for key, v := range last {
			lastOwn[wkey{key, v}] = true
		}
	}

	// the reads of committed transactions, each as the prefix the others
	// installed: a transaction's own appends at the end of its read are its
	// own view, no evidence of the order. A transaction of unknown outcome
	// committed if a committed read saw one of its values: then its reads
	// count too, which may show others committed (a fixpoint).
	type obs struct {
		txn  int
		list []int
	}
	seen := map[int]bool{} // transactions a committed read saw a value of
	committed := func(id int) bool {
		t := byID[id]
		return t != nil && (t.Outcome == Committed || t.Outcome == Unknown && seen[id])
	}
	for grew := true; grew; {
		grew = false
		for _, t := range txns {
			if committed(t.ID) {
				for _, o := range t.Ops {
					if o.Kind != Read {
						continue
					}
					for _, v := range o.List {
						if w, ok := writer[wkey{o.Key, v}]; ok && w != t.ID && !seen[w] {
							seen[w], grew = true, true
						}
					}
				}
			}
		}
	}
	reads := map[string][]obs{}
	for _, t := range txns {
		if !committed(t.ID) {
			continue
		}
		var own []wkey
		for _, o := range t.Ops {
			if o.Kind == Append {
				own = append(own, wkey{o.Key, o.Value})
				continue
			}
			if d, ok := duplicated(o.List); ok {
				add("duplicate", []int{t.ID}, "transaction %d's %s holds %d twice", t.ID, o, d)
			}
			ext := o.List
			mine := 0
			for len(ext) > 0 && writer[wkey{o.Key, ext[len(ext)-1]}] == t.ID {
				ext, mine = ext[:len(ext)-1], mine+1
			}
			// its own appends to the key, in the order it made them
			var want []int
			for _, w := range own {
				if w.key == o.Key {
					want = append(want, w.value)
				}
			}
			if !slices.Equal(o.List[len(o.List)-mine:], want) {
				add("internal", []int{t.ID}, "transaction %d's %s does not end with its own appends to %s, %v in order", t.ID, o, o.Key, want)
			}
			reads[o.Key] = append(reads[o.Key], obs{t.ID, ext})
		}
	}

	// each key's version order: its longest read, of which every other must
	// be a prefix
	keys := slices.Sorted(maps.Keys(reads))
	g := depgraph.New()
	var edges []dep
	link := func(from, to int, kind, key string) {
		if committed(from) && committed(to) {
			g.Add(int64(from), int64(to), kind, key)
			edges = append(edges, dep{from, to, kind, key})
		}
	}
	for _, key := range keys {
		rs := reads[key]
		longest := rs[0]
		for _, r := range rs[1:] {
			if len(r.list) > len(longest.list) {
				longest = r
			}
		}
		order := longest.list
		for _, r := range rs {
			if !slices.Equal(r.list, order[:min(len(r.list), len(order))]) {
				add("incompatible-order", []int{r.txn, longest.txn}, "%s: transaction %d read %v, transaction %d read %v: neither is a prefix of the other", key, r.txn, r.list, longest.txn, order)
			}
		}
		bad := false
		// a value no transaction appended, in any read (one in a shorter
		// read is no version of the longest's either)
		garbage := map[int]bool{}
		for _, r := range rs {
			for _, v := range r.list {
				if _, ok := writer[wkey{key, v}]; !ok && !garbage[v] {
					garbage[v] = true
					add("garbage", []int{r.txn}, "%s: transaction %d read %d, which no transaction appended", key, r.txn, v)
					bad = true
				}
			}
		}
		for _, v := range order {
			w, ok := writer[wkey{key, v}]
			switch {
			case !ok:
			case byID[w].Outcome == Aborted:
				var by []int
				for _, r := range rs {
					if slices.Contains(r.list, v) {
						by = append(by, r.txn)
					}
				}
				add("G1a", append([]int{w}, by...), "%s: transactions %v read %d, appended by transaction %d, which aborted", key, by, v, w)
			}
		}
		if bad {
			continue // no order to take edges from
		}
		for i := 1; i < len(order); i++ {
			link(writer[wkey{key, order[i-1]}], writer[wkey{key, order[i]}], "ww", key)
		}
		for _, r := range rs {
			if len(r.list) > 0 {
				last := r.list[len(r.list)-1]
				w := writer[wkey{key, last}]
				if !lastOwn[wkey{key, last}] && w != r.txn {
					add("G1b", []int{w, r.txn}, "%s: transaction %d read %v, ending at %d, an intermediate append of transaction %d", key, r.txn, r.list, last, w)
				}
				link(w, r.txn, "wr", key)
			}
			if len(r.list) < len(order) {
				link(r.txn, writer[wkey{key, order[len(r.list)]}], "rw", key)
			}
		}
	}
	for _, scc := range g.Components() {
		kind, cyc := g.Classify(scc)
		ids := make([]int, len(cyc))
		var b strings.Builder
		for i, e := range cyc {
			ids[i] = int(e.From)
			fmt.Fprintf(&b, "%d -%s(%s)-> ", e.From, e.Kind, e.Key)
		}
		fmt.Fprintf(&b, "%d", cyc[0].From)
		add(kind, ids, "%s", b.String())
	}
	if level >= StrictSerializable {
		// real time's edges with the dependencies: a cycle through one is
		// a history no serial order that keeps real time explains
		rt := depgraph.New()
		out := map[int][]dep{}
		for _, e := range edges {
			rt.Add(int64(e.from), int64(e.to), e.kind, e.key)
			out[e.from] = append(out[e.from], e)
		}
		var timed []dep
		for _, a := range txns {
			for _, b := range txns {
				if a.ID != b.ID && committed(a.ID) && committed(b.ID) && a.Outcome != Unknown && !a.Completed.IsZero() && !b.Invoked.IsZero() && a.Completed.Before(b.Invoked) {
					e := dep{a.ID, b.ID, "rt", ""}
					rt.Add(int64(a.ID), int64(b.ID), "rt", "")
					out[a.ID] = append(out[a.ID], e)
					timed = append(timed, e)
				}
			}
		}
		for _, scc := range rt.Components() {
			in := map[int]bool{}
			for _, n := range scc {
				in[int(n)] = true
			}
			for _, e := range timed {
				if !in[e.from] || !in[e.to] {
					continue
				}
				if back := pathOf(out, in, e.to, e.from); back != nil {
					cyc := append([]dep{e}, back...)
					rws, wws := 0, 0
					ids := make([]int, len(cyc))
					var b strings.Builder
					for i, h := range cyc {
						ids[i] = h.from
						switch h.kind {
						case "rw":
							rws++
						case "ww":
							wws++
						}
						if h.key != "" {
							fmt.Fprintf(&b, "%d -%s(%s)-> ", h.from, h.kind, h.key)
						} else {
							fmt.Fprintf(&b, "%d -%s-> ", h.from, h.kind)
						}
					}
					fmt.Fprintf(&b, "%d", cyc[0].from)
					kind := "G2-realtime"
					switch {
					case rws == 0 && wws+rts(cyc) == len(cyc):
						kind = "G0-realtime"
					case rws == 0:
						kind = "G1c-realtime"
					case rws == 1:
						kind = "G-single-realtime"
					}
					add(kind, ids, "%s (rt: completed before the next was invoked)", b.String())
					break // one cycle a component, as for the others
				}
			}
		}
	}
	return rep, nil
}

// A dep is an edge between transactions: a dependency (ww, wr, rw on a
// key) or real time's (rt).
type dep struct {
	from, to  int
	kind, key string
}

// pathOf is a shortest path from a to b over the edges out, among in's
// transactions, or nil.
func pathOf(out map[int][]dep, in map[int]bool, a, b int) []dep {
	prev := map[int]dep{}
	queue, visited := []int{a}, map[int]bool{a: true}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, e := range out[v] {
			if !in[e.to] || visited[e.to] {
				continue
			}
			visited[e.to], prev[e.to] = true, e
			if e.to == b {
				var p []dep
				for n := b; n != a; n = prev[n].from {
					p = append(p, prev[n])
				}
				slices.Reverse(p)
				return p
			}
			queue = append(queue, e.to)
		}
	}
	return nil
}

// rts counts a cycle's real-time edges.
func rts(cyc []dep) int {
	n := 0
	for _, e := range cyc {
		if e.kind == "rt" {
			n++
		}
	}
	return n
}

// duplicated returns a value list holds twice, if any.
func duplicated(list []int) (int, bool) {
	seen := map[int]bool{}
	for _, v := range list {
		if seen[v] {
			return v, true
		}
		seen[v] = true
	}
	return 0, false
}
