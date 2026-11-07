package qtrace

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Cross-recorder ordering is an explicit clock assumption in the verdict.
// Check its evidence separately from the linearization search.
func TestClockOrdersContract(t *testing.T) {
	at := time.Unix(1700000000, 0)
	a := Step{Recorder: "z", Time: at}
	b := Step{Recorder: "a", Invoked: at.Add(3 * time.Second), Time: at.Add(4 * time.Second)}
	cases := []struct {
		name    string
		steps   []Step
		skew    time.Duration
		orders  int
		closest time.Duration
		recs    []string
	}{
		{"empty", nil, time.Second, 0, 0, nil},
		{"pair", []Step{a, b}, time.Second, 1, 3 * time.Second, []string{"a", "z"}},
		{"default-zero", []Step{a, b}, 0, 1, 3 * time.Second, []string{"a", "z"}},
		{"default-negative", []Step{a, b}, -1, 1, 3 * time.Second, []string{"a", "z"}},
		{"strict-boundary", []Step{a, b}, 3 * time.Second, 0, 0, nil},
		{"same-recorder", []Step{{Recorder: "a", Time: at}, b}, time.Second, 0, 0, nil},
		{"no-invocation", []Step{a, {Recorder: "a", Time: at.Add(time.Hour)}}, time.Second, 0, 0, nil},
		{"domain-clock", []Step{{Recorder: "z", Time: at, fromRecord: true, Order: map[string]int64{"l": 1}}, b}, time.Second, 0, 0, nil},
		{"closest", []Step{a, b, {Recorder: "a", Invoked: at.Add(2 * time.Second), Time: at.Add(4 * time.Second)}}, time.Second, 2, 2 * time.Second, []string{"a", "z"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClockOrders(c.steps, c.skew)
			wantSkew := c.skew
			if wantSkew <= 0 {
				wantSkew = RealTimeSlack
			}
			if got.Skew != wantSkew || got.Orders != c.orders || got.Closest != c.closest || !slices.Equal(got.Recorders, c.recs) {
				t.Fatalf("got %#v; want orders=%d closest=%v recorders=%v skew=%v", got, c.orders, c.closest, c.recs, wantSkew)
			}
		})
	}
}

func TestClockAssumptionString(t *testing.T) {
	if got := (ClockAssumption{}).String(); got != "" {
		t.Fatalf("no clock orders should need no assumption: %q", got)
	}
	for _, tc := range []struct {
		orders int
		want   string
	}{{1, "a pair of steps"}, {2, "2 pairs of steps"}} {
		if got := (ClockAssumption{Orders: tc.orders}).String(); !strings.Contains(got, tc.want) {
			t.Fatalf("%d clock orders: got %q, want %q", tc.orders, got, tc.want)
		}
	}
}

// A read within the default slack may precede the write it did not see.
// An explicit tighter skew makes that order impossible.
func TestLinearizeSkewContract(t *testing.T) {
	at := time.Unix(1700000000, 0)
	write := op("writer", 1, "write", 1, 0, 0)
	write.Invoked, write.Time = at, at.Add(time.Millisecond)
	read := op("reader", 1, "read", 0, 0, 0)
	read.Invoked = write.Time.Add(RealTimeSlack / 2)
	read.Time = read.Invoked.Add(time.Millisecond)
	for _, tc := range []struct {
		name string
		skew time.Duration
		want LinearizeVerdict
	}{
		{"default-zero", 0, Linearizable},
		{"default-negative", -time.Nanosecond, Linearizable},
		{"explicit-tight", RealTimeSlack / 4, NotLinearizable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Linearize([]Step{write, read}, LinearizeOptions{Skew: tc.skew}, func(orders [][]Step) ([]int, []string, error) {
				fs, states := registerStates(orders)
				return fs, states, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Verdict != tc.want {
				t.Fatalf("skew %v: got %v, want %v", tc.skew, got.Verdict, tc.want)
			}
		})
	}
}
