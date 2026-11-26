package qtrace

import (
	"reflect"
	"testing"
	"time"
)

// A stall says its kind, its length and its start, and its detail only
// when the probe read one.
func TestStallString(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 6e6, time.UTC)
	s := Stall{Start: start, End: start.Add(1500 * time.Millisecond), Kind: "sched"}
	if got, want := s.String(), "a sched stall of 1.5s from 03:04:05.006"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	s.Detail = "throttled 1.2s"
	if got, want := s.String(), "a sched stall of 1.5s from 03:04:05.006 (throttled 1.2s)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// StalledWithin counts each instant of the window once: stalls clipped
// to the window, overlapping ones merged whatever their order, disjoint
// ones summed, and those outside it neither counted nor returned.
func TestStalledWithin(t *testing.T) {
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return base.Add(time.Duration(s * float64(time.Second))) }
	st := func(from, to float64) Stall { return Stall{Start: at(from), End: at(to), Kind: "sched"} }
	sec := func(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
	for _, c := range []struct {
		name     string
		stalls   []Stall
		from, to float64
		want     time.Duration
		in       []Stall
	}{
		{"none", nil, 10, 20, 0, nil},
		{"all outside", []Stall{st(0, 10), st(20, 30), st(2, 5)}, 10, 20, 0, nil},
		{"one inside", []Stall{st(12, 14)}, 10, 20, sec(2), []Stall{st(12, 14)}},
		{"one across the start", []Stall{st(5, 12)}, 10, 20, sec(2), []Stall{st(5, 12)}},
		{"one across the end", []Stall{st(18, 25)}, 10, 20, sec(2), []Stall{st(18, 25)}},
		{"one covering it", []Stall{st(0, 30)}, 10, 20, sec(10), []Stall{st(0, 30)}},
		{"disjoint", []Stall{st(11, 12), st(15, 18)}, 10, 20, sec(4), []Stall{st(11, 12), st(15, 18)}},
		{"overlapping", []Stall{st(11, 14), st(13, 16)}, 10, 20, sec(5), []Stall{st(11, 14), st(13, 16)}},
		{"nested", []Stall{st(11, 18), st(12, 13)}, 10, 20, sec(7), []Stall{st(11, 18), st(12, 13)}},
		{"unsorted, merged", []Stall{st(15, 19), st(10, 12), st(11, 16)}, 10, 20, sec(9),
			[]Stall{st(15, 19), st(10, 12), st(11, 16)}},
		{"unsorted, disjoint", []Stall{st(17, 18), st(11, 12), st(14, 15)}, 10, 20, sec(3),
			[]Stall{st(17, 18), st(11, 12), st(14, 15)}},
		{"some outside", []Stall{st(1, 2), st(12, 13), st(25, 26)}, 10, 20, sec(1), []Stall{st(12, 13)}},
	} {
		got, in := StalledWithin(c.stalls, at(c.from), at(c.to))
		if got != c.want || !reflect.DeepEqual(in, c.in) {
			t.Errorf("%s: StalledWithin = %v, %v; want %v, %v", c.name, got, in, c.want, c.in)
		}
	}
}

func TestLatestEarliest(t *testing.T) {
	a, b := time.Unix(1, 0), time.Unix(2, 0)
	for _, p := range [][2]time.Time{{a, b}, {b, a}} {
		if got := latest(p[0], p[1]); !got.Equal(b) {
			t.Errorf("latest(%v, %v) = %v, want %v", p[0], p[1], got, b)
		}
		if got := earliest(p[0], p[1]); !got.Equal(a) {
			t.Errorf("earliest(%v, %v) = %v, want %v", p[0], p[1], got, a)
		}
	}
}
