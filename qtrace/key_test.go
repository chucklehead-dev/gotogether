package qtrace

import (
	"testing"
	"time"
)

// Runs that differ in what a verdict can depend on (a seq: a gap; an
// error; an interval: real-time order) have other keys; runs that differ
// only in generated ids or, without intervals, in times share one.
func TestKeyCoversWhatVerdictsReadOnly(t *testing.T) {
	base := Step{Action: "a", Recorder: "r1", Process: "p1", Thread: "t", Seq: 1, Time: time.Unix(1, 0)}
	same := base
	same.Recorder, same.Process, same.Time = "r9", "p9", time.Unix(5, 0)
	if Key([]Step{base}) != Key([]Step{same}) {
		t.Error("generated ids or a time without an interval changed the key")
	}
	for name, change := range map[string]func(*Step){
		"seq":     func(s *Step) { s.Seq = 2 },
		"error":   func(s *Step) { s.Error = "boom" },
		"invoked": func(s *Step) { s.Invoked = time.Unix(0, 0) },
	} {
		other := base
		change(&other)
		if Key([]Step{base}) == Key([]Step{other}) {
			t.Errorf("a step with another %s has the same key", name)
		}
	}
}
