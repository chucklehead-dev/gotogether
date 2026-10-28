package qtrace

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// marshalRef is the step-log line as gotogether.steps/1 defines it, the
// encoding before AppendLine: json.Marshal of the step's attributes.
func marshalRef(s *Step) ([]byte, error) {
	b, err := json.Marshal(s.Attributes())
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// sameLine checks AppendLine (and MarshalLine) against marshalRef: the
// same bytes, or an error from both.
func sameLine(t *testing.T, s *Step) {
	t.Helper()
	want, werr := marshalRef(s)
	got, gerr := s.AppendLine([]byte("prefix"))
	if (werr != nil) != (gerr != nil) {
		t.Fatalf("json.Marshal's error %v, AppendLine's %v, for %#v", werr, gerr, s)
	}
	if werr != nil {
		if string(got) != "prefix" {
			t.Fatalf("AppendLine's error left %q in the buffer", got)
		}
		if werr.Error() != gerr.Error() {
			t.Fatalf("json.Marshal's error %q, AppendLine's %q", werr, gerr)
		}
		return
	}
	if !bytes.Equal(got[len("prefix"):], want) {
		t.Fatalf("AppendLine differs from json.Marshal for %#v:\n got %s\nwant %s", s, got[len("prefix"):], want)
	}
	if m, _ := s.MarshalLine(); !bytes.Equal(m, want) {
		t.Fatalf("MarshalLine differs from json.Marshal:\n got %s\nwant %s", m, want)
	}
}

// genString is a string of the characters encodings get wrong: quotes,
// backslashes, control characters, HTML's, multi-byte runes, U+2028 and
// U+2029, invalid UTF-8.
func genString(r *rand.Rand) string {
	pieces := []string{"a", "Z", "0", " ", "\"", "\\", "/", "<", ">", "&", "\b", "\f", "\n", "\r", "\t", "\x00", "\x01", "\x1f", "\x7f",
		"é", "ü", "日本", "🙂", "\u2028", "\u2029", "\u2027", "\ufffd", "\xff", "\xc3", "\xe2\x80", "\xed\xa0\x80", "quint.", ".", "\u00a0"}
	var sb strings.Builder
	for range r.IntN(8) {
		sb.WriteString(pieces[r.IntN(len(pieces))])
	}
	return sb.String()
}

// genValue is an argument or observation: a step log's scalars, at their
// edges, and values only json.Marshal encodes (other types, floats that
// are not finite, invalid numbers).
func genValue(r *rand.Rand) any {
	switch r.IntN(14) {
	case 0:
		return genString(r)
	case 1:
		return []int64{0, 1, -1, 255, 256, math.MaxInt64, math.MinInt64, 1 << 53, -(1 << 53) - 1}[r.IntN(9)]
	case 2:
		return r.Int64() - r.Int64()
	case 3:
		return r.IntN(2) == 0
	case 4:
		return []float64{0, math.Copysign(0, -1), 1.5, -2.25, 1e-6, 9.99e-7, 1e-7, 1e20, 1e21, 123456789e15, math.MaxFloat64,
			math.SmallestNonzeroFloat64, 3.0, 0.1, 1e-10, -1e-300}[r.IntN(16)]
	case 5:
		return r.NormFloat64() * math.Pow(10, float64(r.IntN(60)-30))
	case 6:
		return []float64{math.NaN(), math.Inf(1), math.Inf(-1)}[r.IntN(3)]
	case 7:
		return json.Number([]string{"", "0", "-0", "12345678901234567890123", "-98765432109876543210", "1.5e300", "1E+2", "0.5", "-", "01",
			"1.", "1e", "abc", "1.2.3", "+1", " 1"}[r.IntN(16)])
	case 8:
		return nil
	case 9:
		return r.IntN(100) // an int: json.Marshal's
	case 10:
		return []int{1, 2}
	case 11:
		return float32(1.5)
	case 12:
		return time.Duration(5)
	}
	return ""
}

func genTime(r *rand.Rand) time.Time {
	switch r.IntN(4) {
	case 0:
		return time.Time{}
	case 1:
		return time.Unix(0, r.Int64())
	case 2:
		return time.Unix(0, -r.Int64())
	}
	return time.Unix(1_700_000_000, r.Int64N(1e9))
}

// genStep is a step of every field, each set or not.
func genStep(r *rand.Rand) Step {
	maybe := func() bool { return r.IntN(3) == 0 }
	s := Step{Action: genString(r), Seq: r.Uint64() >> uint(r.IntN(64)), Recorder: genString(r), Outcome: genString(r), Time: genTime(r)}
	if maybe() {
		s.Seq = math.MaxUint64 // past int64: json.Marshal of int64(Seq)
	}
	for _, f := range []*string{&s.Actor, &s.Process, &s.Thread, &s.Error, &s.Ref} {
		if maybe() {
			*f = genString(r)
		}
	}
	if maybe() {
		s.Invoked = genTime(r)
	}
	s.Invocation, s.Settled, s.Ambiguous = maybe(), maybe(), maybe()
	if maybe() {
		s.Call = r.Uint64()
	}
	if maybe() {
		s.Lost, s.LostAfter, s.LostBefore = []string{"suffix", "prefix", genString(r)}[r.IntN(3)], r.Uint64(), genTime(r)
	} else if maybe() {
		s.LostAfter, s.LostBefore = r.Uint64(), genTime(r) // no Lost: not written
	}
	if maybe() {
		s.CompleteThrough = genTime(r)
	}
	scalar := r.IntN(2) == 0 // half the steps of a step log's scalars only, which skip json.Marshal
	value := func() any {
		for {
			v := genValue(r)
			switch v.(type) {
			case string, int64, bool, nil:
				return v
			case float64:
				if f := v.(float64); !math.IsNaN(f) && !math.IsInf(f, 0) {
					return v
				}
			}
			if !scalar {
				return v
			}
		}
	}
	for _, m := range []*map[string]any{&s.Args, &s.Obs} {
		switch r.IntN(4) {
		case 0:
		case 1:
			*m = map[string]any{}
		default:
			*m = map[string]any{}
			for range r.IntN(20) {
				(*m)[genString(r)] = value()
			}
		}
	}
	if maybe() {
		s.Order = map[string]int64{}
		for range r.IntN(4) {
			s.Order[genString(r)] = r.Int64() - r.Int64()
		}
	}
	s.Source = genString(r) // not written
	return s
}

// The step log is a published format (gotogether.steps/1): AppendLine
// writes json.Marshal's bytes, or fails as it does, on steps of every
// field, string and value json.Marshal treats specially.
func TestAppendLineAgainstMarshal(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	n := 20_000
	if testing.Short() {
		n = 3_000
	}
	fast := 0
	for range n {
		s := genStep(r)
		if _, ok := s.appendLine(nil); ok {
			fast++
		}
		sameLine(t, &s)
	}
	// the generator reaches both paths: a test of the fallback alone
	// would prove nothing of the encoder
	if fast < n/4 || fast > n*3/4 {
		t.Fatalf("%d of %d steps took the encoder", fast, n)
	}
}

// Every step of the repository's step logs encodes as json.Marshal does.
func TestAppendLineOnRepositoryLogs(t *testing.T) {
	for _, steps := range repositoryLogs(t) {
		for i := range steps {
			sameLine(t, &steps[i])
		}
	}
}

// repositoryLogs are the repository's step logs (its testdata's,
// examples' too): none in a module's copy outside it.
func repositoryLogs(t *testing.T) [][]Step {
	t.Helper()
	var out [][]Step
	logs := 0
	_ = filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == ".gosim") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") || !strings.Contains(path, "testdata") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		steps, err := ReadJSONL(f)
		if err != nil || len(steps) == 0 {
			return nil // a request log, a driver's trace: no step log
		}
		logs++
		out = append(out, steps)
		return nil
	})
	if logs == 0 {
		t.Skip("no step logs in the tree (a module's copy)")
	}
	t.Logf("%d step logs", logs)
	return out
}
