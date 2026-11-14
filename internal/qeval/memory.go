package qeval

import "math"

// graphMemory charges modelled retained graph storage before admission. This
// is not heap/RSS accounting: shared values are charged on each occurrence,
// allocator overhead is estimated, and transient evaluation is outside it.
// Constants include room for node/graph slices, frontier and map entries.
type graphMemory struct {
	limit, used int64
}

func (b *graphMemory) reserve(n int64) bool {
	if b.limit > 0 && n > b.limit-b.used {
		return false
	}
	b.used = memorySum(b.used, n)
	return true
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
