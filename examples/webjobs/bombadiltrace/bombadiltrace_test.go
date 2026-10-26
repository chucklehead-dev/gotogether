package bombadiltrace

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// line is a trace.jsonl entry at ts microseconds: action (nil: none) and
// a result snapshot (nil: none).
func line(ts int64, action any, result *shown) string {
	e := map[string]any{"timestamp": ts, "action": action,
		"state":     map[string]any{"url": "http://127.0.0.1:1/", "screenshot": fmt.Sprintf("/tmp/out/screenshots/%d.jpeg", ts)},
		"snapshots": []any{}, "violations": []any{}}
	if result != nil {
		e["snapshots"] = []any{map[string]any{"index": 0, "name": "result", "value": result, "time": ts}}
	}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

func custom(name string) any {
	return map[string]any{"Custom": map[string]any{"name": name, "arguments": []any{}}}
}

func read(t *testing.T, lines ...string) ([]qtrace.Step, error) {
	t.Helper()
	return Read(strings.NewReader(strings.Join(lines, "")), Options{})
}

// A click is an invocation at the state before it; the outcome the page
// shows completes it, at the state that shows it, with the line and the
// screenshot as its ref; a job shown ready by polling is a step of its
// own; one status change answers every click outstanding on the job.
func TestMapping(t *testing.T) {
	steps, err := read(t,
		line(1000, nil, &shown{Key: "k1"}),
		line(2000, custom("ui.submit"), &shown{ID: "j1", Status: "accepted", Key: "k1"}),
		line(3000, "Wait", &shown{ID: "j1", Status: "ready", Key: "k2"}),
		line(4000, custom("ui.submit"), &shown{Key: "k2"}), // not answered yet
		line(5000, "Wait", &shown{ID: "j2", Status: "accepted", Key: "k2"}),
		line(6000, custom("ui.cancel"), &shown{ID: "j2", Status: "accepted", Key: "k2"}),
		line(7000, custom("ui.cancel"), &shown{ID: "j2", Status: "cancelled", Key: "k3"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		action string
		inv    bool
		call   uint64
		at     int64
		args   string
		ref    string
	}
	wants := []want{
		{"submit", true, 0, 1000, "map[key:k1]", "trace.jsonl:1 screenshot=screenshots/1000.jpeg"},
		{"submit", false, 1, 2000, "map[id:j1 key:k1 status:accepted]", "trace.jsonl:2 screenshot=screenshots/2000.jpeg"},
		{"ready", false, 0, 3000, "map[id:j1]", "trace.jsonl:3 screenshot=screenshots/3000.jpeg"},
		{"submit", true, 0, 3000, "map[key:k2]", "trace.jsonl:3 screenshot=screenshots/3000.jpeg"},
		{"submit", false, 4, 5000, "map[id:j2 key:k2 status:accepted]", "trace.jsonl:5 screenshot=screenshots/5000.jpeg"},
		{"cancel", true, 0, 5000, "map[id:j2]", "trace.jsonl:5 screenshot=screenshots/5000.jpeg"},
		{"cancel", true, 0, 6000, "map[id:j2]", "trace.jsonl:6 screenshot=screenshots/6000.jpeg"},
		{"cancel", false, 6, 7000, "map[id:j2 status:cancelled]", "trace.jsonl:7 screenshot=screenshots/7000.jpeg"},
		{"cancel", false, 7, 7000, "map[id:j2 status:cancelled]", "trace.jsonl:7 screenshot=screenshots/7000.jpeg"},
	}
	if len(steps) != len(wants) {
		t.Fatalf("%d records, want %d: %v", len(steps), len(wants), steps)
	}
	for i, w := range wants {
		s := steps[i]
		got := want{s.Action, s.Invocation, s.Call, s.Time.UnixMicro(), fmt.Sprint(s.Args), s.Ref}
		if got != w || s.Seq != uint64(i+1) || s.Recorder != "ui" || s.Actor != "webjobs" || s.Process != "webjobs" {
			t.Errorf("record %d: %+v (seq %d, %s/%s/%s), want %+v", i+1, got, s.Seq, s.Recorder, s.Actor, s.Process, w)
		}
	}
	traces, issues := qtrace.Reconstruct(steps)
	if len(issues) > 0 || len(traces) != 1 || len(traces[0].Steps) != 5 || len(traces[0].Pending) != 0 {
		t.Fatalf("reconstructed: %v %+v", issues, traces)
	}
	if got := traces[0].Steps[0].Invoked; !got.Equal(time.UnixMicro(1000)) {
		t.Errorf("submit's completion invoked at %v, want its click's state (1000µs)", got)
	}
}

// A click the trace never shows the outcome of is pending, not done.
func TestClickWithNoOutcome(t *testing.T) {
	steps, err := read(t,
		line(1000, nil, &shown{Key: "k1"}),
		line(2000, custom("ui.submit"), &shown{Key: "k1"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	traces, _ := qtrace.Reconstruct(steps)
	if len(traces) != 1 || len(traces[0].Steps) != 0 || len(traces[0].Pending) != 1 || traces[0].Pending[0].Action != "submit" {
		t.Fatalf("a submit with no outcome: %+v, want it pending", traces)
	}
}

// What the mapping cannot read, or that the page's contract rules out,
// is refused with the line and the fix.
func TestRefused(t *testing.T) {
	for _, c := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"a submit with no snapshot", []string{
			line(1000, nil, &shown{Key: "k1"}),
			line(2000, custom("ui.submit"), nil),
		}, "trace.jsonl:2: ui.submit with no result snapshot"},
		{"two ids for one submit", []string{
			line(1000, nil, &shown{Key: "k1"}),
			line(2000, custom("ui.submit"), &shown{ID: "j1", Status: "accepted", Key: "k1"}),
			line(3000, "Wait", &shown{ID: "j2", Status: "accepted", Key: "k1"}),
		}, "trace.jsonl:3: job j2 shown with no ui.submit outstanding"},
		{"a cancel no click made", []string{
			line(1000, nil, &shown{Key: "k1"}),
			line(2000, custom("ui.submit"), &shown{ID: "j1", Status: "accepted", Key: "k1"}),
			line(3000, "Wait", &shown{ID: "j1", Status: "cancelled", Key: "k2"}),
		}, "trace.jsonl:3: job j1 shown cancelled after accepted with no ui.cancel outstanding"},
		{"a custom action not mapped", []string{
			line(1000, nil, &shown{Key: "k1"}),
			line(2000, custom("ui.retry"), &shown{Key: "k1"}),
		}, "custom action ui.retry is none the adapter maps"},
		{"a cancel with no job", []string{
			line(1000, nil, &shown{Key: "k1"}),
			line(2000, custom("ui.cancel"), &shown{Key: "k1"}),
		}, "ui.cancel with no job shown before it"},
		{"a malformed line", []string{
			"{not json\n",
			line(2000, nil, &shown{Key: "k1"}),
		}, "trace.jsonl:1: not a Bombadil trace entry"},
	} {
		t.Run(c.name, func(t *testing.T) {
			steps, err := read(t, c.lines...)
			if err == nil || !strings.Contains(err.Error(), c.want) || steps != nil {
				t.Fatalf("got %v and %d steps, want an error with %q and none", err, len(steps), c.want)
			}
		})
	}
}

// A last line cut off is a lost tail: the steps before it, a loss
// record after the page's last seq, and the *qtrace.TornTail.
func TestTornTail(t *testing.T) {
	last := line(3000, "Wait", &shown{ID: "j1", Status: "ready", Key: "k2"})
	steps, err := read(t,
		line(1000, nil, &shown{Key: "k1"}),
		line(2000, custom("ui.submit"), &shown{ID: "j1", Status: "accepted", Key: "k1"}),
		last[:len(last)/2],
	)
	var torn *qtrace.TornTail
	if !errors.As(err, &torn) || torn.Line != 3 {
		t.Fatalf("err %v, want a torn tail at line 3", err)
	}
	if len(steps) != 3 || steps[2].Lost != "suffix" || steps[2].LostAfter != 2 || steps[2].Recorder != "ui" {
		t.Fatalf("steps %+v, want the submit's two records and a loss after seq 2", steps)
	}
}
