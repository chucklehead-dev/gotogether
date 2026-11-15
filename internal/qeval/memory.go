package qeval

import (
	"math"
	"runtime"
	"runtime/metrics"
)

// graphMemory charges modelled retained graph storage before admission. This
// is not heap/RSS accounting: shared values are charged on each occurrence,
// allocator overhead is estimated, and transient evaluation is outside it.
// Constants include room for node/graph slices, frontier and map entries.
//
// process, when set, bounds the process's memory as the Go runtime maps it
// (Bound.ProcessBytes): the estimate above misses compilation, evaluation's
// garbage and allocator overhead, and a search within its estimate once
// peaked 1.18 times past it in resident memory, enough to kill a CI
// runner sized by the estimate (golem's, #144).
type graphMemory struct {
	limit, used int64
	process     int64 // bound on processMemory(), 0: none
	reserves    int   // reservations since the last process check
	every       int   // reservations between process checks
	// ProcessSeen is the largest process reading taken; processOver says a
	// reading past process cut the search
	processSeen int64
	processOver bool
}

// processCheckEvery is how many reservations pass between two reads of the
// process's memory: a read costs about a microsecond, a reservation a
// state's or an edge's evaluation, far more.
const processCheckEvery = 256

func (b *graphMemory) reserve(n int64) bool {
	if b.limit > 0 && n > b.limit-b.used {
		return false
	}
	if b.process > 0 && !b.processWithin() {
		return false
	}
	b.used = memorySum(b.used, n)
	return true
}

// processWithin reads the process's memory every so many reservations and
// says whether it is within its bound. A reading past it is confirmed after a
// collection (garbage alone never cuts a search), and a confirmed reading
// under it backs the checks off, so a search near its bound does not
// collect at every check.
func (b *graphMemory) processWithin() bool {
	if b.every == 0 {
		b.every = processCheckEvery
	}
	if b.reserves++; b.reserves < b.every {
		return true
	}
	b.reserves = 0
	m := processMemory()
	b.processSeen = max(b.processSeen, m)
	if m <= b.process {
		b.every = processCheckEvery
		return true
	}
	runtime.GC()
	if m = processMemory(); m <= b.process {
		b.every = 16 * processCheckEvery
		return true
	}
	b.processSeen = max(b.processSeen, m)
	b.processOver = true
	return false
}

// processMemory is the memory the Go runtime has mapped for the process and
// not returned to the operating system: heap, stacks and the runtime's own,
// close to what the process holds resident.
func processMemory() int64 {
	s := []metrics.Sample{{Name: "/memory/classes/total:bytes"}, {Name: "/memory/classes/heap/released:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 || s[1].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return int64(s[0].Value.Uint64() - s[1].Value.Uint64())
}

func memorySum(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

func stateMemory(st State, key string) int64 {
	n := memorySum(512, memorySum(int64(len(key)), int64(len(key))))
	for name, v := range st {
		n = memorySum(n, memorySum(80+int64(len(name)), valueMemory(v)))
	}
	return n
}

func valueMemory(v Value) int64 {
	n := int64(32) // interface/boxed value and allocation allowance
	seq := func(xs []Value) {
		n = memorySum(n, 24)
		for _, x := range xs {
			n = memorySum(n, memorySum(16, valueMemory(x)))
		}
	}
	switch x := v.(type) {
	case Str:
		n = memorySum(n, int64(len(x)))
	case *Set:
		seq(x.Elems)
	case *Map:
		seq(x.Keys)
		seq(x.Vals)
	case *Rec:
		for _, name := range x.Names {
			n = memorySum(n, 16+int64(len(name)))
		}
		seq(x.Vals)
	case Tup:
		seq(x)
	case List:
		seq(x)
	case Variant:
		n = memorySum(n, memorySum(int64(len(x.Label)), valueMemory(x.Val)))
	}
	return n
}

func labelMemory(events []Event, key []byte) int64 {
	n := memorySum(128, memorySum(int64(len(key)), int64(len(key))))
	for _, e := range events {
		n = memorySum(n, 128+int64(len(e.Action))+int64(len(e.Pick)))
		for _, p := range e.Params {
			n = memorySum(n, 16+int64(len(p)))
		}
		for _, a := range e.Args {
			n = memorySum(n, memorySum(16, valueMemory(a)))
		}
		if e.Value != nil {
			n = memorySum(n, valueMemory(e.Value))
		}
	}
	return n
}
