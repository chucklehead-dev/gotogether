package qtrace

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The step log's encoder. A line is json.Marshal(s.Attributes()): an
// object of the attribute keys in sorted order, strings escaped as
// encoding/json escapes them (HTML characters too). gotogether.steps/1 is
// a published format, so the bytes are encoding/json's exactly
// (TestAppendLineAgainstMarshal); the encoder only skips the map, the
// reflection and the sort, which cost a step 10 µs and 40 allocations.
// A value it does not know (an argument of another type than a step
// log's scalars, a float that is not finite, an invalid json.Number) goes
// through json.Marshal, as before: the same bytes, or the same error.

// field is one of a line's keys: a fixed one, or a group of prefixed
// ones (an argument, an observation, a domain clock).
type field uint8

const (
	fAction field = iota
	fSeq
	fRecorder
	fOutcome
	fTime
	fActor
	fProcess
	fThread
	fError
	fRef
	fInvoked
	fInvocation
	fCall
	fSettled
	fLost
	fLostAfter
	fLostBefore
	fCompleteThrough
	fAssumed
	fArgs
	fObs
	fOrder
	nFields
)

// fieldKey is each field's key, a group's its prefix: every key of a
// group sorts against every other key as its prefix does (no fixed key
// starts with a prefix, nor is a prefix's proper prefix), so the groups
// sort into the fixed keys' order whole.
// Read-only.
var fieldKey = [nFields]string{
	fAction: KeyAction, fSeq: KeySeq, fRecorder: KeyRecorder, fOutcome: KeyOutcome, fTime: KeyTime,
	fActor: KeyActor, fProcess: KeyProcess, fThread: KeyThread, fError: KeyError, fRef: KeyRef,
	fInvoked: KeyInvoked, fInvocation: KeyInvocation, fCall: KeyCall, fSettled: KeySettled,
	fLost: KeyLost, fLostAfter: KeyLostAfter, fLostBefore: KeyLostBefore,
	fCompleteThrough: KeyCompleteThrough, fAssumed: KeyAssumed,
	fArgs: PrefixArg, fObs: PrefixObs, fOrder: PrefixOrder,
}

// lineOrder is the fields in their keys' sorted order: json.Marshal's
// order of a map's keys.
// Read-only (set in init).
var lineOrder = func() [nFields]field {
	var o [nFields]field
	for i := range o {
		o[i] = field(i)
	}
	slices.SortFunc(o[:], func(a, b field) int { return strings.Compare(fieldKey[a], fieldKey[b]) })
	return o
}()

// AppendLine appends the step's step-log line (with the newline) to b:
// MarshalLine's bytes, without allocating for a step of a step log's
// scalars when b has room. On an error b is returned as it was.
func (s *Step) AppendLine(b []byte) ([]byte, error) {
	if out, ok := s.appendLine(b); ok {
		return out, nil
	}
	j, err := json.Marshal(s.Attributes())
	if err != nil {
		return b, err
	}
	return append(append(b, j...), '\n'), nil
}

// appendLine encodes the step, false (b's contents past len(b) undefined)
// if a value needs json.Marshal.
func (s *Step) appendLine(b []byte) ([]byte, bool) {
	start := len(b)
	b = append(b, '{')
	first := true
	key := func(k string) {
		if !first {
			b = append(b, ',')
		}
		first = false
		b = appendString(b, k)
		b = append(b, ':')
	}
	str := func(k, v string) {
		key(k)
		b = appendString(b, v)
	}
	num := func(k string, v int64) {
		key(k)
		b = strconv.AppendInt(b, v, 10)
	}
	yes := func(k string) {
		key(k)
		b = append(b, "true"...)
	}
	var sorted [16]string
	for _, f := range lineOrder {
		switch f {
		case fAction:
			str(KeyAction, s.Action)
		case fSeq:
			num(KeySeq, int64(s.Seq))
		case fRecorder:
			str(KeyRecorder, s.Recorder)
		case fOutcome:
			str(KeyOutcome, s.Outcome)
		case fTime:
			if !s.Time.IsZero() {
				num(KeyTime, s.Time.UnixNano())
			}
		case fActor:
			if s.Actor != "" {
				str(KeyActor, s.Actor)
			}
		case fProcess:
			if s.Process != "" {
				str(KeyProcess, s.Process)
			}
		case fThread:
			if s.Thread != "" {
				str(KeyThread, s.Thread)
			}
		case fError:
			if s.Error != "" {
				str(KeyError, s.Error)
			}
		case fRef:
			if s.Ref != "" {
				str(KeyRef, s.Ref)
			}
		case fInvoked:
			if !s.Invoked.IsZero() {
				num(KeyInvoked, s.Invoked.UnixNano())
			}
		case fInvocation:
			if s.Invocation {
				yes(KeyInvocation)
			}
		case fCall:
			if s.Call != 0 {
				num(KeyCall, int64(s.Call))
			}
		case fSettled:
			if s.Settled {
				yes(KeySettled)
			}
		case fLost:
			if s.Lost != "" {
				str(KeyLost, s.Lost)
			}
		case fLostAfter:
			if s.Lost != "" {
				num(KeyLostAfter, int64(s.LostAfter))
			}
		case fLostBefore:
			if s.Lost != "" && !s.LostBefore.IsZero() {
				num(KeyLostBefore, s.LostBefore.UnixNano())
			}
		case fCompleteThrough:
			if !s.CompleteThrough.IsZero() {
				num(KeyCompleteThrough, s.CompleteThrough.UnixNano())
			}
		case fAssumed:
			if s.Ambiguous {
				yes(KeyAssumed)
			}
		case fArgs, fObs:
			m := s.Args
			if f == fObs {
				m = s.Obs
			}
			if len(m) == 0 {
				continue
			}
			keys := sorted[:0]
			for k := range m {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if !first {
					b = append(b, ',')
				}
				first = false
				b = appendPrefixed(b, fieldKey[f], k)
				b = append(b, ':')
				var ok bool
				if b, ok = appendValue(b, m[k]); !ok {
					return b[:start], false
				}
			}
		case fOrder:
			if len(s.Order) == 0 {
				continue
			}
			keys := sorted[:0]
			for k := range s.Order {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if !first {
					b = append(b, ',')
				}
				first = false
				b = appendPrefixed(b, PrefixOrder, k)
				b = append(b, ':')
				b = strconv.AppendInt(b, s.Order[k], 10)
			}
		}
	}
	return append(b, '}', '\n'), true
}

// appendPrefixed appends the key prefix+k, quoted, without building it:
// the prefixes need no escaping.
func appendPrefixed(b []byte, prefix, k string) []byte {
	b = append(b, '"')
	b = append(b, prefix...)
	b = appendEscaped(b, k)
	return append(b, '"')
}

// appendValue appends a step log's scalar as encoding/json writes it;
// false for a value it leaves to json.Marshal.
func appendValue(b []byte, v any) ([]byte, bool) {
	switch x := v.(type) {
	case string:
		return appendString(b, x), true
	case int64:
		return strconv.AppendInt(b, x, 10), true
	case bool:
		return strconv.AppendBool(b, x), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return b, false // json.Marshal's error
		}
		format := byte('f')
		if abs := math.Abs(x); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
			format = 'e'
		}
		b = strconv.AppendFloat(b, x, format, -1, 64)
		if format == 'e' { // e-07 is e-7, as encoding/json writes it
			if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
				b[n-2] = b[n-1]
				b = b[:n-1]
			}
		}
		return b, true
	case json.Number:
		if x == "" {
			return append(b, '0'), true
		}
		if !validNumber(string(x)) {
			return b, false // json.Marshal's error
		}
		return append(b, x...), true
	case nil:
		return append(b, "null"...), true
	}
	return b, false
}

// validNumber is encoding/json's check of a json.Number.
func validNumber(s string) bool {
	digit := func(c byte) bool { return '0' <= c && c <= '9' }
	if s == "" {
		return false
	}
	if s[0] == '-' {
		if s = s[1:]; s == "" {
			return false
		}
	}
	switch {
	case s[0] == '0':
		s = s[1:]
	case '1' <= s[0] && s[0] <= '9':
		s = s[1:]
		for len(s) > 0 && digit(s[0]) {
			s = s[1:]
		}
	default:
		return false
	}
	if len(s) >= 2 && s[0] == '.' && digit(s[1]) {
		s = s[2:]
		for len(s) > 0 && digit(s[0]) {
			s = s[1:]
		}
	}
	if len(s) >= 2 && (s[0] == 'e' || s[0] == 'E') {
		s = s[1:]
		if s[0] == '+' || s[0] == '-' {
			if s = s[1:]; s == "" {
				return false
			}
		}
		for len(s) > 0 && digit(s[0]) {
			s = s[1:]
		}
	}
	return s == ""
}

const hexDigits = "0123456789abcdef"

// appendString appends s quoted as encoding/json quotes a string, HTML
// characters escaped: its bytes, invalid UTF-8 as U+FFFD, U+2028 and
// U+2029 escaped.
func appendString(b []byte, s string) []byte {
	b = append(b, '"')
	b = appendEscaped(b, s)
	return append(b, '"')
}

// appendEscaped appends s's characters as appendString quotes them.
func appendEscaped(b []byte, s string) []byte {
	start := 0
	for i := 0; i < len(s); {
		if c := s[i]; c < utf8.RuneSelf {
			if htmlSafe[c] {
				i++
				continue
			}
			b = append(b, s[start:i]...)
			switch c {
			case '\\', '"':
				b = append(b, '\\', c)
			case '\b':
				b = append(b, '\\', 'b')
			case '\f':
				b = append(b, '\\', 'f')
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default: // a control character, or <, > or &
				b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', 'f', 'f', 'f', 'd')
			i += size
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', '2', '0', '2', hexDigits[r&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	return append(b, s[start:]...)
}

// htmlSafe is encoding/json's htmlSafeSet: printable ASCII but for the
// quote, the backslash and <, > and &.
// Read-only (set in init).
var htmlSafe = func() (t [utf8.RuneSelf]bool) {
	for c := byte(0x20); c < utf8.RuneSelf; c++ {
		t[c] = c != '"' && c != '\\' && c != '<' && c != '>' && c != '&'
	}
	return t
}()
