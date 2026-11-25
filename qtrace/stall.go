package qtrace

import (
	"fmt"
	"slices"
	"time"
)

// A Stall is a window of wall-clock time in which the host that ran a
// real system stalled: a probe outside the system under test (a harness's
// ticker that did not tick, its fsync that did not return) measured it
// (qnemesis.WatchHost). It is evidence about the environment, as a Lost
// is about the telemetry: a bounded response the stall alone could have
// made late is no verdict (validate.Report.ExcuseStalls), never a pass.
type Stall struct {
	// Start and End are when the probe last ran before the stall and
	// when it ran again: the stall lies within them.
	Start, End time.Time
	// Kind is the probe that measured it: "sched" (a ticker's gap: the
	// harness was not scheduled), "disk" (an fsync that took that long).
	Kind string
	// Detail is what else the probe read of it, when anything: the
	// thread's scheduling delay, the cgroup's CPU throttling.
	Detail string
}

// Duration is how long the stall lasted.
func (s Stall) Duration() time.Duration { return s.End.Sub(s.Start) }

// String says what stalled, when and for how long.
func (s Stall) String() string {
	out := fmt.Sprintf("a %s stall of %s from %s", s.Kind, s.Duration().Round(time.Millisecond), s.Start.UTC().Format("15:04:05.000"))
	if s.Detail != "" {
		out += " (" + s.Detail + ")"
	}
	return out
}

// StalledWithin is how much of [from, to] the stalls cover, each instant
// counted once where stalls overlap (a scheduling gap and a slow fsync of
// one freeze), and the stalls that overlap it.
func StalledWithin(stalls []Stall, from, to time.Time) (time.Duration, []Stall) {
	var in []Stall
	for _, s := range stalls {
		if s.End.After(from) && s.Start.Before(to) {
			in = append(in, s)
		}
	}
	if len(in) == 0 {
		return 0, nil
	}
	spans := make([][2]time.Time, len(in))
	for i, s := range in {
		spans[i] = [2]time.Time{latest(s.Start, from), earliest(s.End, to)}
	}
	slices.SortFunc(spans, func(a, b [2]time.Time) int { return a[0].Compare(b[0]) })
	var total time.Duration
	cur := spans[0]
	for _, sp := range spans[1:] {
		if sp[0].After(cur[1]) {
			total += cur[1].Sub(cur[0])
			cur = sp
			continue
		}
		cur[1] = latest(cur[1], sp[1])
	}
	return total + cur[1].Sub(cur[0]), in
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
