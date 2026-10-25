package qenv_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// The tests here read a TimeProfile as pprof does, with a strict decoder of
// the profile.proto fields it writes (the core does not depend on
// github.com/google/pprof): a wrong varint, a missing field or a dangling
// id fails the decode, where `go tool pprof` might only show a profile
// that is quietly wrong.

// profStepLine is the line of the Sleep in profStep, set when it runs.
var profStepLine int // process-wide: written by the tasks of one test at a time

// profStep waits d at one place: every task running it has one stack.
func profStep(ctx context.Context, d time.Duration) {
	_, _, line, _ := runtime.Caller(0)
	profStepLine = line + 2
	qenv.Sleep(ctx, d)
}

// profOther waits at another place, for a second stack of one task kind.
func profOther(ctx context.Context, d time.Duration) { qenv.Sleep(ctx, d) }

// A decoded profile, as profile.proto names its fields.
type profValueType struct{ typ, unit string }

type profLine struct {
	fn   uint64
	line int64
}

type profLocation struct {
	id      uint64
	address uint64
	lines   []profLine
}

type profFunction struct {
	id                     uint64
	name, systemName, file string
}

type profLabel struct {
	key, str string
	num      int64
}

type profSample struct {
	locs   []uint64
	values []int64
	labels []profLabel
}

type profDecoded struct {
	sampleTypes []profValueType
	samples     []profSample
	locations   []profLocation
	functions   []profFunction
	strings     []string
	periodType  *profValueType
}

// profField is one protobuf field: a varint (wire type 0) or bytes (2).
type profField struct {
	num  int
	wire int
	v    uint64
	b    []byte
}

// profFields splits b into fields, failing on anything malformed: a varint
// that runs off the end, a wire type the profile never writes, a length
// past the end.
func profFields(t *testing.T, b []byte) []profField {
	t.Helper()
	varint := func() uint64 {
		var x uint64
		for shift := 0; ; shift += 7 {
			if len(b) == 0 || shift > 63 {
				t.Fatalf("truncated varint")
			}
			c := b[0]
			b = b[1:]
			x |= uint64(c&0x7f) << shift
			if c < 0x80 {
				return x
			}
		}
	}
	var fs []profField
	for len(b) > 0 {
		tag := varint()
		f := profField{num: int(tag >> 3), wire: int(tag & 7)}
		switch f.wire {
		case 0:
			f.v = varint()
		case 2:
			n := varint()
			if n > uint64(len(b)) {
				t.Fatalf("field %d: length %d past the end (%d left)", f.num, n, len(b))
			}
			f.b, b = b[:n], b[n:]
		default:
			t.Fatalf("field %d: wire type %d", f.num, f.wire)
		}
		fs = append(fs, f)
	}
	return fs
}

// profDecode gunzips and decodes a profile, checking every field is one
// pprof knows with the wire type it expects.
func profDecode(t *testing.T, gz []byte) *profDecoded {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("not gzipped: %v", err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	want := func(f profField, wire int, what string) {
		t.Helper()
		if f.wire != wire {
			t.Fatalf("%s: field %d has wire type %d, want %d", what, f.num, f.wire, wire)
		}
	}
	valueType := func(b []byte) [2]int64 {
		var vt [2]int64
		for _, f := range profFields(t, b) {
			want(f, 0, "ValueType")
			if f.num < 1 || f.num > 2 {
				t.Fatalf("ValueType: field %d", f.num)
			}
			vt[f.num-1] = int64(f.v)
		}
		return vt
	}
	var p profDecoded
	var sampleTypes [][2]int64
	var period *[2]int64
	type rawLabel struct{ key, str, num int64 }
	var labels [][]rawLabel
	var fnNames [][3]int64
	for _, f := range profFields(t, raw) {
		switch f.num {
		case 1:
			want(f, 2, "sample_type")
			sampleTypes = append(sampleTypes, valueType(f.b))
		case 2:
			want(f, 2, "sample")
			var s profSample
			var ls []rawLabel
			for _, g := range profFields(t, f.b) {
				switch g.num {
				case 1:
					want(g, 0, "Sample.location_id")
					s.locs = append(s.locs, g.v)
				case 2:
					want(g, 0, "Sample.value")
					s.values = append(s.values, int64(g.v))
				case 3:
					want(g, 2, "Sample.label")
					var l rawLabel
					for _, h := range profFields(t, g.b) {
						want(h, 0, "Label")
						switch h.num {
						case 1:
							l.key = int64(h.v)
						case 2:
							l.str = int64(h.v)
						case 3:
							l.num = int64(h.v)
						default:
							t.Fatalf("Label: field %d", h.num)
						}
					}
					ls = append(ls, l)
				default:
					t.Fatalf("Sample: field %d", g.num)
				}
			}
			p.samples = append(p.samples, s)
			labels = append(labels, ls)
		case 4:
			want(f, 2, "location")
			var l profLocation
			for _, g := range profFields(t, f.b) {
				switch g.num {
				case 1:
					want(g, 0, "Location.id")
					l.id = g.v
				case 3:
					want(g, 0, "Location.address")
					l.address = g.v
				case 4:
					want(g, 2, "Location.line")
					var ln profLine
					for _, h := range profFields(t, g.b) {
						want(h, 0, "Line")
						switch h.num {
						case 1:
							ln.fn = h.v
						case 2:
							ln.line = int64(h.v)
						default:
							t.Fatalf("Line: field %d", h.num)
						}
					}
					l.lines = append(l.lines, ln)
				default:
					t.Fatalf("Location: field %d", g.num)
				}
			}
			p.locations = append(p.locations, l)
		case 5:
			want(f, 2, "function")
			var fn profFunction
			var names [3]int64
			for _, g := range profFields(t, f.b) {
				want(g, 0, "Function")
				switch g.num {
				case 1:
					fn.id = g.v
				case 2, 3, 4:
					names[g.num-2] = int64(g.v)
				default:
					t.Fatalf("Function: field %d", g.num)
				}
			}
			p.functions = append(p.functions, fn)
			fnNames = append(fnNames, names)
		case 6:
			want(f, 2, "string_table")
			p.strings = append(p.strings, string(f.b))
		case 11:
			want(f, 2, "period_type")
			if period != nil {
				t.Fatalf("two period types")
			}
			vt := valueType(f.b)
			period = &vt
		default:
			t.Fatalf("Profile: field %d", f.num)
		}
	}
	// strings are referred to by index; pprof requires the first be ""
	if len(p.strings) == 0 || p.strings[0] != "" {
		t.Fatalf("string table %q: want it to start with \"\"", p.strings)
	}
	str := func(i int64) string {
		t.Helper()
		if i < 0 || i >= int64(len(p.strings)) {
			t.Fatalf("string index %d out of a table of %d", i, len(p.strings))
		}
		return p.strings[i]
	}
	for _, vt := range sampleTypes {
		p.sampleTypes = append(p.sampleTypes, profValueType{str(vt[0]), str(vt[1])})
	}
	if period != nil {
		p.periodType = &profValueType{str(period[0]), str(period[1])}
	}
	for i, ls := range labels {
		for _, l := range ls {
			p.samples[i].labels = append(p.samples[i].labels, profLabel{str(l.key), str(l.str), l.num})
		}
	}
	for i, n := range fnNames {
		p.functions[i].name, p.functions[i].systemName, p.functions[i].file = str(n[0]), str(n[1]), str(n[2])
	}
	return &p
}

// profWrite writes p and checks WriteTo's count is what it wrote.
func profWrite(t *testing.T, p *qenv.TimeProfile) []byte {
	t.Helper()
	var buf bytes.Buffer
	n, err := p.WriteTo(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(buf.Len()) || n == 0 {
		t.Fatalf("WriteTo returned %d; wrote %d bytes", n, buf.Len())
	}
	return buf.Bytes()
}

// profRun runs a simulation with a profile: tasks of several kinds, some
// at one stack, one at another.
func profRun(t *testing.T) *qenv.Sim {
	t.Helper()
	s := qenv.New(qenv.Nominal{})
	s.Profile = qenv.NewTimeProfile()
	spawn := func(name string, d time.Duration) { profSpawn(s, name, d) }
	spawn("client.o1", time.Second)
	spawn("client.o2", time.Second)
	spawn("server", 128) // 128ns: a varint at the edge of one byte
	spawn(".x", 3*time.Millisecond)
	s.Spawn("client.o3", func(ctx context.Context) { profOther(ctx, 5*time.Millisecond) })
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	return s
}

// profSpawn spawns a task running profStep. It is not inlined, so every
// task it spawns runs one closure's code: one stack.
//
//go:noinline
func profSpawn(s *qenv.Sim, name string, d time.Duration) {
	s.Spawn(name, func(ctx context.Context) { profStep(ctx, d) })
}

// A sample as a test compares it: the task kind, the values, and whether
// its stack runs through profStep or profOther.
type profSeen struct {
	task   string
	count  int64
	nanos  int64
	caller string
}

// TestTimeProfileAsPprofReadsIt pins what a TimeProfile says, decoded field
// by field as pprof reads it: the sample types (waits, then virtual time,
// pprof's default being the last), one sample per task kind and stack (a
// name's last dotted part is an instance; a name whose only dot leads
// keeps it), each with its count and virtual time and a task label, and
// locations that resolve to the functions, files and lines that waited. A
// profile that merged kinds, split instances, or pointed at the wrong line
// would send whoever reads it to the wrong call.
func TestTimeProfileAsPprofReadsIt(t *testing.T) {
	s := profRun(t)
	wantTotal := 2*time.Second + 128 + 3*time.Millisecond + 5*time.Millisecond
	if got := s.Profile.Total(); got != wantTotal {
		t.Fatalf("Total %v; want %v", got, wantTotal)
	}
	p := profDecode(t, profWrite(t, s.Profile))

	if got, want := fmt.Sprint(p.sampleTypes), "[{waits count} {virtual nanoseconds}]"; got != want {
		t.Errorf("sample types %s; want %s", got, want)
	}
	if p.periodType == nil || *p.periodType != (profValueType{"virtual", "nanoseconds"}) {
		t.Errorf("period type %v; want virtual nanoseconds", p.periodType)
	}
	seenStr := map[string]bool{}
	for _, s := range p.strings {
		if seenStr[s] {
			t.Errorf("string %q twice in the table", s)
		}
		seenStr[s] = true
	}

	funcs := map[uint64]profFunction{}
	fnByName := map[string]bool{}
	for i, f := range p.functions {
		if f.id != uint64(i+1) {
			t.Errorf("function %d has id %d; want ids 1..n in order", i, f.id)
		}
		if f.name == "" || f.systemName != f.name || f.file == "" {
			t.Errorf("function %d: name %q, system name %q, file %q", f.id, f.name, f.systemName, f.file)
		}
		if fnByName[f.name] {
			t.Errorf("function %s written twice", f.name)
		}
		fnByName[f.name] = true
		funcs[f.id] = f
	}
	locs := map[uint64]profLocation{}
	for i, l := range p.locations {
		if l.id != uint64(i+1) {
			t.Errorf("location %d has id %d; want ids 1..n in order", i, l.id)
		}
		// the address is the call's (the return address less one): its
		// frames are the location's lines, inlined calls included
		frames := runtime.CallersFrames([]uintptr{uintptr(l.address) + 1})
		var want []string
		for {
			f, more := frames.Next()
			want = append(want, fmt.Sprintf("%s %s:%d", f.Function, f.File, f.Line))
			if !more {
				break
			}
		}
		var got []string
		for _, ln := range l.lines {
			f, ok := funcs[ln.fn]
			if !ok {
				t.Fatalf("location %d: line names function %d, which is not written", l.id, ln.fn)
			}
			got = append(got, fmt.Sprintf("%s %s:%d", f.name, f.file, ln.line))
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("location %d at %#x:\n%s\nwant\n%s", l.id, l.address, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		locs[l.id] = l
	}

	var seen []profSeen
	usedLocs := map[uint64]bool{}
	for i, smp := range p.samples {
		if len(smp.values) != 2 {
			t.Fatalf("sample %d: values %v; want a count and a time", i, smp.values)
		}
		if len(smp.labels) != 1 || smp.labels[0].key != "task" || smp.labels[0].num != 0 {
			t.Fatalf("sample %d: labels %v; want one task label", i, smp.labels)
		}
		if len(smp.locs) == 0 {
			t.Fatalf("sample %d: no stack", i)
		}
		ps := profSeen{task: smp.labels[0].str, count: smp.values[0], nanos: smp.values[1]}
		for j, id := range smp.locs {
			l, ok := locs[id]
			if !ok {
				t.Fatalf("sample %d: location %d is not written", i, id)
			}
			usedLocs[id] = true
			for _, ln := range l.lines {
				name := funcs[ln.fn].name
				switch {
				case strings.HasSuffix(name, "qenv_test.profStep"):
					ps.caller = "profStep"
					if !strings.HasSuffix(funcs[ln.fn].file, "survivors_profile_test.go") || ln.line != int64(profStepLine) {
						t.Errorf("profStep at %s:%d; want survivors_profile_test.go:%d", funcs[ln.fn].file, ln.line, profStepLine)
					}
				case strings.HasSuffix(name, "qenv_test.profOther"):
					ps.caller = "profOther"
				}
				// the stack starts at the seam that waits, not in the
				// scheduler or the profile
				if j == 0 && name != "github.com/chucklehead-dev/gotogether/qenv.Sleep" {
					t.Errorf("sample %d starts at %s; want the seam, qenv.Sleep", i, name)
				}
			}
		}
		seen = append(seen, ps)
	}
	if len(usedLocs) != len(p.locations) {
		t.Errorf("%d locations written, %d used by samples", len(p.locations), len(usedLocs))
	}
	got := append([]profSeen(nil), seen...)
	sort.Slice(got, func(i, j int) bool {
		if got[i].task != got[j].task {
			return got[i].task < got[j].task
		}
		return got[i].caller < got[j].caller
	})
	want := []profSeen{
		{".x", 1, int64(3 * time.Millisecond), "profStep"},
		{"client", 1, int64(5 * time.Millisecond), "profOther"},
		{"client", 2, int64(2 * time.Second), "profStep"},
		{"server", 1, 128, "profStep"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("samples\n%v\nwant\n%v", got, want)
	}
	// samples are written in order of task kind (then stack)
	for i := 1; i < len(seen); i++ {
		if seen[i-1].task > seen[i].task {
			t.Errorf("samples out of order: %v", seen)
			break
		}
	}
}

// TestTimeProfileWritesTheSameBytes pins that writing a profile is
// deterministic: one profile written again gives the same bytes, as a
// simulation's runs are meant to be compared (a corpus, a diff of two
// profiles). Map order must not reach the output.
func TestTimeProfileWritesTheSameBytes(t *testing.T) {
	s := profRun(t)
	first := profWrite(t, s.Profile)
	for i := 0; i < 20; i++ {
		if again := profWrite(t, s.Profile); !bytes.Equal(again, first) {
			t.Fatalf("write %d differs from the first", i+2)
		}
	}
}
