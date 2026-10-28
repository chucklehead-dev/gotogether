package qeval

import (
	"encoding/binary"
	"unsafe"
)

// Keys are binary (I39): a value is its kind's tag, then its content,
// every variable-length part counted before it, so each value's encoding
// is self-delimiting and decodes to one value (key_test.go decodes them
// back). Two values encode alike exactly when Compare says they are
// equal: sets' elements, maps' keys and records' fields are in their
// canonical order (sorted, distinct), as Compare reads them, a Tup and a
// List have their own tags, and an operator encodes its identity, as
// Compare orders it. A state's key is its variables' encodings in the
// order of Vars (tagAbsent for one it lacks): one self-delimiting
// encoding after another, so equal keys are equal states. The text keys
// they replaced quoted every string with strconv.Quote, a fifth of an
// explore's CPU.
const (
	tagAbsent byte = iota
	tagInt
	tagFalse
	tagTrue
	tagStr
	tagSet
	tagMap
	tagRec
	tagTup
	tagList
	tagVariant
	tagLambda
	tagInfinite
)

// Events' tags, outside the values' (appendEvent): a list of events and
// the state after it (Transitions' key) are told apart by tagEnd.
const (
	tagAction byte = 0xf1 + iota
	tagPick
	tagEnd
)

// appendKey appends v's encoding to b.
func appendKey(b []byte, v Value) []byte {
	switch x := v.(type) {
	case Int:
		return binary.AppendVarint(append(b, tagInt), int64(x))
	case Bool:
		if x {
			return append(b, tagTrue)
		}
		return append(b, tagFalse)
	case Str:
		return appendStr(append(b, tagStr), string(x))
	case *Set:
		return appendSeq(append(b, tagSet), x.Elems)
	case *Map:
		b = binary.AppendUvarint(append(b, tagMap), uint64(len(x.Keys)))
		for i := range x.Keys {
			b = appendKey(appendKey(b, x.Keys[i]), x.Vals[i])
		}
		return b
	case *Rec:
		b = binary.AppendUvarint(append(b, tagRec), uint64(len(x.Names)))
		for i, n := range x.Names {
			b = appendKey(appendStr(b, n), x.Vals[i])
		}
		return b
	case Tup:
		return appendSeq(append(b, tagTup), x)
	case List:
		return appendSeq(append(b, tagList), x)
	case Variant:
		return appendKey(appendStr(append(b, tagVariant), x.Label), x.Val)
	case *Lambda:
		// by identity, as Compare orders operators (no state holds one:
		// a let's or a parameter's, in an action's call memo, is not
		// remembered)
		return binary.AppendUvarint(append(b, tagLambda), uint64(uintptr(unsafe.Pointer(x))))
	case infinite:
		if x.nat {
			return append(b, tagInfinite, 1)
		}
		return append(b, tagInfinite, 0)
	}
	panic("qeval: no key of a value of this kind")
}

func appendStr(b []byte, s string) []byte {
	return append(binary.AppendUvarint(b, uint64(len(s))), s...)
}

func appendSeq(b []byte, vs []Value) []byte {
	b = binary.AppendUvarint(b, uint64(len(vs)))
	for _, v := range vs {
		b = appendKey(b, v)
	}
	return b
}

// appendState appends s's key to b: its variables' values in the order
// of Vars.
func (m *Model) appendState(b []byte, s State) []byte {
	for _, v := range m.Vars {
		x, ok := s[v]
		if !ok {
			b = append(b, tagAbsent)
			continue
		}
		b = appendKey(b, x)
	}
	return b
}

// appendEvent appends one event's encoding: an action's name and
// arguments, or a choice's name and value, written in full (String cuts
// long values short, and two labels differing past the cut are two
// labels). An action's parameters are its name's.
func appendEvent(b []byte, e Event) []byte {
	if e.Action != "" {
		return appendSeq(appendStr(append(b, tagAction), e.Action), e.Args)
	}
	return appendKey(appendStr(append(b, tagPick), e.Pick), e.Value)
}
