package qtrace

import (
	"strings"
	"testing"
	"time"
)

// An invocation record's time is the call's start: a record timed after
// its completion, and a completion numbered before its invocation, are
// errors of the actor's trace, each naming the records. An invocation
// timed before an earlier record of its recorder is not: a search
// (Linearize, ResolvePending) re-sequences a history in its own order.
func TestInvocationOrder(t *testing.T) {
	at := func(ms int) time.Time { return time.Unix(100, 0).Add(time.Duration(ms) * time.Millisecond) }
	inv := func(seq uint64, ms int) Step {
		return Step{Action: "put", Recorder: "r", Actor: "a", Seq: seq, Time: at(ms), Invocation: true}
	}
	done := func(seq, call uint64, ms int) Step {
		return Step{Action: "put", Recorder: "r", Actor: "a", Seq: seq, Call: call, Time: at(ms), Outcome: "ok"}
	}
	step := func(seq uint64, ms int) Step {
		return Step{Action: "get", Recorder: "r", Actor: "a", Seq: seq, Time: at(ms), Outcome: "ok"}
	}
	for _, c := range []struct {
		name  string
		steps []Step
		want  string // "" for no invocation issue
	}{
		{"in order", []Step{inv(1, 0), done(2, 1, 5)}, ""},
		{"within the slack", []Step{step(1, 10), inv(2, 10), done(3, 2, 10)}, ""},
		{"invoked after its completion", []Step{inv(1, 50), done(2, 1, 5)}, "before its invocation record (seq 1)"},
		{"completed before its invocation in seq", []Step{done(1, 2, 5), inv(2, 0)}, "completes the invocation of seq 2, which is not before it"},
		{"invoked before an earlier record", []Step{step(1, 40), inv(2, 10), done(3, 2, 50)}, ""},
	} {
		traces, _ := Reconstruct(c.steps)
		var got []string
		for _, tr := range traces {
			for _, is := range tr.Issues {
				if is.Kind == "invocation" {
					if is.Severity != "error" {
						t.Errorf("%s: %s, want an error", c.name, is)
					}
					got = append(got, is.Msg)
				}
			}
		}
		switch {
		case c.want == "" && len(got) > 0:
			t.Errorf("%s: %v, want no invocation issue", c.name, got)
		case c.want != "" && !strings.Contains(strings.Join(got, "\n"), c.want):
			t.Errorf("%s: %v, want an issue with %q", c.name, got, c.want)
		}
	}
}
