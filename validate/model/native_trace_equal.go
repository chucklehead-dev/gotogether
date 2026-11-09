package model

import "github.com/chucklehead-dev/gotogether/itf"

// nativeTraceEqual compares the structured Quint literal with its decoded ITF
// value. Apalache encodes the empty tuple as the exact record {tag:"UNIT"};
// Quint's reviewed ofItfValue converts that marker back to Tup(). Only an
// expected empty tuple permits this directional interpretation. Ordinary records,
// variant labels/payloads, field names and all other value kinds stay distinct.
// The neutral-field decoder preserves every nested record key, including metadata.
func nativeTraceEqual(expected, actual itf.Value) bool {
	if expected.Kind == itf.Tuple && len(expected.Elems) == 0 && actual.Kind == itf.Record && len(actual.Fields) == 1 {
		tag, ok := actual.Fields["tag"]
		if ok && tag.Kind == itf.Str && tag.S == "UNIT" {
			return true
		}
	}
	if expected.Kind != actual.Kind {
		return false
	}
	switch expected.Kind {
	case itf.Record:
		if len(expected.Fields) != len(actual.Fields) {
			return false
		}
		for name, value := range expected.Fields {
			other, ok := actual.Fields[name]
			if !ok || !nativeTraceEqual(value, other) {
				return false
			}
		}
		return true
	case itf.List, itf.Tuple:
		if len(expected.Elems) != len(actual.Elems) {
			return false
		}
		for i, value := range expected.Elems {
			if !nativeTraceEqual(value, actual.Elems[i]) {
				return false
			}
		}
		return true
	case itf.Set:
		if len(expected.Elems) != len(actual.Elems) {
			return false
		}
		return nativeTraceBijection(len(expected.Elems), func(i, j int) bool { return nativeTraceEqual(expected.Elems[i], actual.Elems[j]) })
	case itf.Map:
		if len(expected.Pairs) != len(actual.Pairs) {
			return false
		}
		return nativeTraceBijection(len(expected.Pairs), func(i, j int) bool {
			return nativeTraceEqual(expected.Pairs[i][0], actual.Pairs[j][0]) && nativeTraceEqual(expected.Pairs[i][1], actual.Pairs[j][1])
		})
	case itf.Variant:
		if expected.Tag != actual.Tag {
			return false
		}
		inner := func(v itf.Value) itf.Value {
			if v.Inner == nil {
				return itf.Value{Kind: itf.Tuple}
			}
			return *v.Inner
		}
		return nativeTraceEqual(inner(expected), inner(actual))
	default:
		return expected.Equal(actual)
	}
}

// Unit markers can match either a real tag record or an expected empty tuple.
// A complete bipartite matching avoids order-dependent greedy choices while
// preserving cardinality and pairing keys with their own map values.
func nativeTraceBijection(n int, equal func(int, int) bool) bool {
	edges := make([][]bool, n)
	for i := range n {
		edges[i] = make([]bool, n)
		for j := range n {
			edges[i][j] = equal(i, j)
		}
	}
	matched := make([]int, n)
	for i := range matched {
		matched[i] = -1
	}
	var augment func(int, []bool) bool
	augment = func(i int, seen []bool) bool {
		for j, ok := range edges[i] {
			if !ok || seen[j] {
				continue
			}
			seen[j] = true
			if matched[j] < 0 || augment(matched[j], seen) {
				matched[j] = i
				return true
			}
		}
		return false
	}
	for i := range n {
		if !augment(i, make([]bool, n)) {
			return false
		}
	}
	return true
}
