package qtrace

import (
	"bytes"
	"encoding/xml"
	"os"
	"strings"
	"testing"
	"time"
)

// A history drawn: a lane per client, each call a bar numbered by the
// order judged, the pending write dashed and numbered where the order
// assumes it took effect, the failing read red; the SVG parses. Its text
// is a golden file (GOTOGETHER_UPDATE_GOLDEN=1 writes it).
func TestHistorySVG(t *testing.T) {
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	call := func(rec string, from, to int, action string, args, obs map[string]any) []Step {
		return []Step{
			{Action: action, Recorder: rec, Seq: 1, Thread: rec, Actor: "r", Time: at(from), Invocation: true, Args: args},
			{Action: action, Recorder: rec, Seq: 2, Thread: rec, Actor: "r", Time: at(to), Call: 1, Args: args, Obs: obs},
		}
	}
	recorded := []Step{{Action: "write", Recorder: "timedout", Seq: 1, Thread: "timedout", Actor: "r", Time: at(0), Invocation: true, Args: map[string]any{"v": int64(1)}}}
	recorded = append(recorded, call("reader", 5, 6, "read", map[string]any{}, map[string]any{"v": int64(1)})...)
	recorded = append(recorded, call("writer", 10, 14, "write", map[string]any{"v": int64(2)}, nil)...)
	recorded = append(recorded, call("late", 11, 12, "read", map[string]any{}, map[string]any{"v": int64(7)})...)
	traces, _ := Reconstruct(recorded)
	tr := traces[0]
	order, ok, _, _, _ := LinearizePending(tr.Steps, tr.Pending, nil, 1000, registerFinal(len(tr.Steps), -1))
	if ok {
		t.Fatalf("a read of 7 conforms")
	}
	fs, _ := registerStates([][]Step{order})
	svg := HistorySVG(recorded, order, fs[0])
	if err := xml.Unmarshal(svg, new(struct{})); err != nil {
		t.Fatalf("the SVG does not parse: %v", err)
	}
	s := string(svg)
	for _, want := range []string{">reader<", ">timedout<", "(pending)", `stroke-dasharray`, `fill="#f6b5b5"`, "read() → v=7"} {
		if !strings.Contains(s, want) {
			t.Errorf("the drawing has no %q", want)
		}
	}
	if strings.Count(s, `fill="#f6b5b5"`) != 1 {
		t.Errorf("%d failing bars, want 1", strings.Count(s, `fill="#f6b5b5"`))
	}
	const golden = "testdata/history.svg"
	if os.Getenv("GOTOGETHER_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, svg, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v: write it with GOTOGETHER_UPDATE_GOLDEN=1 go test -run TestHistorySVG ./qtrace", err)
	}
	if !bytes.Equal(svg, want) {
		t.Errorf("the drawing changed: if deliberate, GOTOGETHER_UPDATE_GOLDEN=1 go test -run TestHistorySVG ./qtrace")
	}
}
