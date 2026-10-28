package qtrace

import (
	"encoding/xml"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A filter keeps a step only if every set selector matches it: no
// patterns match everything, one pattern that does not match drops the
// step. A selector inverted would hand a validator another run's steps.
func TestFilterSelects(t *testing.T) {
	steps := []Step{
		{Action: "pay", Actor: "checkout-1", Seq: 1},
		{Action: "ship", Actor: "checkout-2", Seq: 2},
		{Action: "pay", Actor: "billing", Seq: 3},
	}
	for _, c := range []struct {
		name string
		f    Filter
		want []uint64
	}{
		{"nothing set", Filter{}, []uint64{1, 2, 3}},
		{"actor pattern", Filter{Actors: []string{"checkout-*"}}, []uint64{1, 2}},
		{"no actor matches", Filter{Actors: []string{"nobody"}}, nil},
		{"action", Filter{Actions: []string{"pay"}}, []uint64{1, 3}},
		{"both", Filter{Actors: []string{"checkout-*"}, Actions: []string{"pay"}}, []uint64{1}},
	} {
		var got []uint64
		for _, s := range c.f.Apply(steps) {
			got = append(got, s.Seq)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: kept %v, want %v", c.name, got, c.want)
		}
	}
}

// Without Keep a filter drops nothing of a step: its arguments,
// observations and error stay. With Keep only the listed fields stay,
// and the error goes. Pseudonymize replaces only the named fields, the
// actor among them only if named.
func TestFilterSanitizes(t *testing.T) {
	s := Step{Action: "pay", Actor: "alice", Seq: 1, Error: "declined",
		Args: map[string]any{"acct": "a1", "amount": int64(5)},
		Obs:  map[string]any{"owner": "alice", "balance": int64(3)}}

	got := Filter{}.Apply([]Step{s})[0]
	if !reflect.DeepEqual(got.Args, s.Args) || !reflect.DeepEqual(got.Obs, s.Obs) || got.Error != s.Error || got.Actor != s.Actor {
		t.Errorf("an empty filter changed the step: %+v", got)
	}
	if got := (Filter{}).Apply([]Step{{Action: "a"}})[0]; got.Args != nil || got.Obs != nil {
		t.Errorf("nil maps became %v, %v", got.Args, got.Obs)
	}

	f := Filter{Pseudonymize: []string{"acct", "obs.owner"}, Salt: "s"}
	got = f.Apply([]Step{s})[0]
	want := Step{Action: "pay", Actor: "alice", Seq: 1, Error: "declined",
		Args: map[string]any{"acct": f.Pseudonym("a1"), "amount": int64(5)},
		Obs:  map[string]any{"owner": f.Pseudonym("alice"), "balance": int64(3)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pseudonymized:\n got %+v\nwant %+v", got, want)
	}

	f = Filter{Pseudonymize: []string{"actor"}}
	if got := f.Apply([]Step{s, {Action: "b"}}); got[0].Actor != f.Pseudonym("alice") || got[1].Actor != "" {
		t.Errorf("actors %q, %q: want the pseudonym and none", got[0].Actor, got[1].Actor)
	}

	got = Filter{Keep: map[string][]string{"pay": {"amount", "obs.balance"}}}.Apply([]Step{s})[0]
	if !reflect.DeepEqual(got.Args, map[string]any{"amount": int64(5)}) || !reflect.DeepEqual(got.Obs, map[string]any{"balance": int64(3)}) || got.Error != "" {
		t.Errorf("kept %v %v %q, want amount, balance and no error", got.Args, got.Obs, got.Error)
	}
}

// A step's attributes carry an invocation time only when it has one:
// a zero time written as Unix nanoseconds would read back as a real one
// (1754), and a completion's interval would be lost.
func TestAttributesInvoked(t *testing.T) {
	plain := Step{Action: "a", Time: time.Unix(5, 0)}
	if _, ok := plain.Attributes()[KeyInvoked]; ok {
		t.Error("a step with no invocation time has one in its attributes")
	}
	timed := Step{Action: "a", Time: time.Unix(5, 0), Invoked: time.Unix(4, 0)}
	if v := timed.Attributes()[KeyInvoked]; v != time.Unix(4, 0).UnixNano() {
		t.Errorf("invocation time %v, want %d", v, time.Unix(4, 0).UnixNano())
	}
}

// A step log reads integers back as int64 and fractions as float64, its
// timestamp and invocation time as times: a binding compares an int64
// argument with the model's int, and a float would not equal it.
func TestReadJSONLValues(t *testing.T) {
	line := `{"quint.action":"a","quint.seq":1,"quint.recorder":"r","quint.time_unix_nano":5000,` +
		`"quint.invoked_unix_nano":4000,"quint.arg.n":3,"quint.arg.x":1.5}` + "\n"
	got, err := ReadJSONL(strings.NewReader(line))
	if err != nil || len(got) != 1 {
		t.Fatalf("%d steps, %v", len(got), err)
	}
	s := got[0]
	if !reflect.DeepEqual(s.Args, map[string]any{"n": int64(3), "x": 1.5}) {
		t.Errorf("args %#v", s.Args)
	}
	if !s.Time.Equal(time.Unix(0, 5000)) || !s.Invoked.Equal(time.Unix(0, 4000)) {
		t.Errorf("times %v, %v", s.Time, s.Invoked)
	}
}

// A call's invocation record is named by a seq, which starts at 1: a
// call of 0 would name no record, and is an error.
func TestFromAttributesCallZero(t *testing.T) {
	for _, c := range []struct {
		call any
		ok   bool
	}{{int64(1), true}, {int64(0), false}, {int64(-1), false}} {
		_, err := FromAttributes(time.Time{}, map[string]any{KeyAction: "a", KeyCall: c.call})
		if (err == nil) != c.ok {
			t.Errorf("call %v: err %v", c.call, err)
		}
	}
}

// Get reads an argument, or an observation with the "obs." prefix, and
// never the one for the other: an argument and an observation may share
// a name.
func TestStepGet(t *testing.T) {
	s := Step{Args: map[string]any{"v": int64(1)}, Obs: map[string]any{"v": int64(2)}}
	for name, want := range map[string]any{"v": int64(1), "obs.v": int64(2)} {
		if got, ok := s.Get(name); !ok || got != want {
			t.Errorf("Get(%q) = %v, %v; want %v", name, got, ok, want)
		}
	}
	if _, ok := s.Get("obs.w"); ok {
		t.Error("Get of a missing observation found one")
	}
}

// A step's string is the action, its arguments sorted and separated, and
// an outcome other than ok: it is in a verdict's cache key (Key), so two
// renderings of one step would miss the cache, and in every report.
func TestStepString(t *testing.T) {
	args := map[string]any{}
	var parts []string
	for i := range 12 {
		k := "k" + strconv.Itoa(10+i)
		args[k] = int64(i)
		parts = append(parts, k+"="+strconv.Itoa(i))
	}
	many := "a(" + strings.Join(parts, ", ") + ")"
	for _, c := range []struct {
		s    Step
		want string
	}{
		{Step{Action: "a"}, "a()"},
		{Step{Action: "a", Outcome: "ok", Args: map[string]any{"x": 1}}, "a(x=1)"},
		{Step{Action: "a", Outcome: "conflict", Args: map[string]any{"y": 2, "x": 1}}, "a(x=1, y=2) -> conflict"},
		{Step{Action: "a", Args: args}, many},
	} {
		for range 5 { // map order varies between iterations
			if got := c.s.String(); got != c.want {
				t.Errorf("String() = %q, want %q", got, c.want)
				break
			}
		}
	}
}

// A run's key numbers recorders by first appearance: two steps of one
// recorder and two of two recorders are other runs (one is program
// order, the other concurrency), whatever the ids.
func TestKeyNumbersRecorders(t *testing.T) {
	run := func(r1, r2 string) []Step {
		return []Step{{Action: "a", Recorder: r1, Process: r1, Thread: r1, Seq: 1}, {Action: "a", Recorder: r2, Process: r2, Thread: r2, Seq: 1}}
	}
	if Key(run("x", "x")) == Key(run("x", "y")) {
		t.Error("one recorder and two have the same key")
	}
	if Key(run("x", "y")) != Key(run("u", "v")) {
		t.Error("renamed recorders changed the key")
	}
}

// svgRects is the x and width of every bar in a drawing (the first rect
// is the background).
func svgRects(t *testing.T, svg []byte) [][2]float64 {
	t.Helper()
	var doc struct {
		Rects []struct {
			X     string `xml:"x,attr"`
			Width string `xml:"width,attr"`
		} `xml:"rect"`
	}
	if err := xml.Unmarshal(svg, &doc); err != nil {
		t.Fatalf("the SVG does not parse: %v", err)
	}
	var out [][2]float64
	for _, r := range doc.Rects[1:] {
		x, err1 := strconv.ParseFloat(r.X, 64)
		w, err2 := strconv.ParseFloat(r.Width, 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("a bar at %q, %q", r.X, r.Width)
		}
		out = append(out, [2]float64{x, w})
	}
	return out
}

// A history's bars lie inside its drawing: the time axis starts at the
// earliest invocation (not the earliest completion), and a history of
// one instant still has a width, not a division by zero.
func TestHistorySVGAxis(t *testing.T) {
	const left, right = 140, 890
	call := func(from, to time.Time) []Step {
		return []Step{
			{Action: "w", Recorder: "c", Thread: "c", Seq: 1, Time: from, Record: Record{Invocation: true}},
			{Action: "w", Recorder: "c", Thread: "c", Seq: 2, Time: to, Call: 1},
		}
	}
	t0 := time.Unix(100, 0)
	for name, steps := range map[string][]Step{
		"interval": call(t0, t0.Add(10*time.Millisecond)),
		"instant":  call(t0, t0),
	} {
		for _, r := range svgRects(t, HistorySVG(steps, nil, -1)) {
			if math.IsNaN(r[0]) || math.IsNaN(r[1]) || r[0] < left || r[0]+r[1] > right {
				t.Errorf("%s: a bar from %v, %v wide, outside [%d, %d]", name, r[0], r[1], left, right)
			}
		}
	}
}

// A bar's observations are labelled in sorted order, so a drawing of one
// history is one drawing (its golden file).
func TestObsStringSorted(t *testing.T) {
	obs := map[string]any{}
	var parts []string
	for i := range 12 {
		k := "o" + strconv.Itoa(10+i)
		obs[k] = i
		parts = append(parts, k+"="+strconv.Itoa(i))
	}
	want := strings.Join(parts, ", ")
	for range 5 {
		if got := obsString(obs); got != want {
			t.Fatalf("obsString = %q, want %q", got, want)
		}
	}
}

// A label is clipped to its bar's width with an ellipsis, down to four
// characters; below that it is left whole (its title has it anyway).
func TestClip(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"abcdefgh", 3, "abcdefgh"},
		{"abcdefgh", 4, "abc…"},
		{"abcdefgh", 5, "abcd…"},
		{"abcd", 4, "abcd"},
	} {
		if got := clip(c.s, c.n); got != c.want {
			t.Errorf("clip(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}
