package qtrace

import (
	"strings"
	"testing"
	"time"
)

// A step's ref is written and read with it: a step log keeps it, a
// resource's quint.ref links no step (it would link every one), and a
// completion that names none takes its invocation record's.
func TestRefRoundTrip(t *testing.T) {
	ref := "bombadil/trace.jsonl:42 screenshot=screenshots/42.webp"
	steps := []Step{
		{Action: "submit", Recorder: "r", Seq: 1, Time: time.Unix(0, 1), Record: Record{Invocation: true}, Ref: ref},
		{Action: "submit", Recorder: "r", Seq: 2, Time: time.Unix(0, 2), Call: 1, Outcome: "ok"},
		{Action: "cancel", Recorder: "r", Seq: 3, Time: time.Unix(0, 3), Outcome: "ok", Ref: "line 7"},
	}
	var b strings.Builder
	if err := WriteJSONL(&b, steps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"quint.ref":"line 7"`) {
		t.Fatalf("the log has no quint.ref:\n%s", b.String())
	}
	got, err := ReadJSONL(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Ref != ref || got[1].Ref != "" || got[2].Ref != "line 7" {
		t.Fatalf("refs read: %q %q %q", got[0].Ref, got[1].Ref, got[2].Ref)
	}
	traces, _ := Reconstruct(got)
	if len(traces) != 1 || len(traces[0].Steps) != 2 || traces[0].Steps[0].Ref != ref || traces[0].Steps[1].Ref != "line 7" {
		t.Fatalf("reconstructed: %+v", traces)
	}
	s, err := FromAttributes(time.Unix(0, 1), map[string]any{KeyAction: "a"}, map[string]any{}, map[string]any{KeyRef: "deployment"})
	if err != nil || s.Ref != "" {
		t.Errorf("a resource's ref: %q, %v", s.Ref, err)
	}
	s, _ = FromAttributes(time.Unix(0, 1), map[string]any{KeyAction: "a"}, map[string]any{KeyRef: "span's"})
	if s.Ref != "span's" {
		t.Errorf("a span's ref: %q", s.Ref)
	}
}

// A ref is no model data: runs that differ only in their refs share a
// verdict cache key.
func TestRefIsNotInTheKey(t *testing.T) {
	a := []Step{{Action: "deposit", Recorder: "r", Seq: 1, Args: map[string]any{"amount": int64(5)}, Ref: "trace.jsonl:1"}}
	b := []Step{{Action: "deposit", Recorder: "q", Seq: 1, Args: map[string]any{"amount": int64(5)}, Ref: "trace.jsonl:9 screenshot=9.webp"}}
	if Key(a) != Key(b) {
		t.Errorf("refs changed the key:\n%s\n%s", Key(a), Key(b))
	}
}

// A filter that only selects keeps refs; one that sanitizes drops them
// (free text no pseudonym reaches), unless KeepRefs.
func TestFilterRefs(t *testing.T) {
	steps := []Step{{Action: "deposit", Actor: "alice", Recorder: "r", Seq: 1, Args: map[string]any{"account": "alice"}, Ref: "/home/alice/shots/1.webp"}}
	for _, c := range []struct {
		name string
		f    Filter
		want string
	}{
		{"select", Filter{Actions: []string{"deposit"}}, steps[0].Ref},
		{"pseudonymize", Filter{Pseudonymize: []string{"account"}}, ""},
		{"keep", Filter{Keep: map[string][]string{"deposit": {"account"}}}, ""},
		{"pseudonymize, KeepRefs", Filter{Pseudonymize: []string{"account"}, KeepRefs: true}, steps[0].Ref},
	} {
		got := c.f.Apply(steps)
		if len(got) != 1 || got[0].Ref != c.want {
			t.Errorf("%s: %+v, want ref %q", c.name, got, c.want)
		}
	}
	if steps[0].Ref == "" {
		t.Error("Apply modified its input")
	}
}

// A history's bars carry their refs in their titles.
func TestHistorySVGRef(t *testing.T) {
	steps := []Step{{Action: "submit", Recorder: "c", Seq: 1, Time: time.Unix(0, 5), Invoked: time.Unix(0, 1), Ref: "trace.jsonl:42"}}
	if svg := string(HistorySVG(steps, steps, 0)); !strings.Contains(svg, "ref: trace.jsonl:42") {
		t.Errorf("no ref in the history:\n%s", svg)
	}
}

// A step with no time is written with no time key, and read back with
// none: its UnixNano overflows, and was written as a time in 1677.
func TestJSONLZeroTime(t *testing.T) {
	var b strings.Builder
	if err := WriteJSONL(&b, []Step{{Action: "a", Recorder: "r", Seq: 1}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), KeyTime) {
		t.Errorf("a zero time written: %s", b.String())
	}
	got, err := ReadJSONL(strings.NewReader(b.String()))
	if err != nil || len(got) != 1 || !got[0].Time.IsZero() {
		t.Errorf("read back: %+v %v", got, err)
	}
}
