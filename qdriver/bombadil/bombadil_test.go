package bombadil

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qdriver"
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

// webjobs is examples/webjobs' mapping: its spec's custom actions and
// extract, its page's log entries ({n, op, key or id, code, job}).
var webjobs = Options{
	Header:   qdriver.Header{Recorder: "ui", Actor: "webjobs", Process: "webjobs", Thread: "page"},
	Snapshot: "result",
	Fields:   Fields{Action: "op", Answer: "job", AnswerFields: []string{"id", "status"}},
	Clicks:   map[string][]string{"ui.submit": {"submit"}, "ui.cancel": {"cancel"}},
	Show:     &Show{Key: "id", Fields: []string{"id", "status"}},
}

func read(t *testing.T, lines ...string) ([]qtrace.Step, error) {
	t.Helper()
	return Read(strings.NewReader(strings.Join(lines, "")), webjobs)
}

// A request a click sent is an invocation at the state before the click;
// its own answer completes it, at the state that first shows it, with
// the line and screenshot as its ref; each change of the job shown is a
// show step. A double click is two calls, each completed by its answer.
func TestMapping(t *testing.T) {
	steps, err := read(t,
		line(1000, nil, page("", "")),
		line(2000, custom("ui.submit"), page("j1", "accepted", sub(1, "k1").answered(202, "j1", "accepted"), sub(2, "k1"))),
		line(3000, "Wait", page("j1", "ready", sub(1, "k1").answered(202, "j1", "accepted"), sub(2, "k1").answered(200, "j1", "accepted"))),
		line(4000, custom("ui.cancel"), page("j1", "ready", sub(1, "k1").answered(202, "j1", "accepted"), sub(2, "k1").answered(200, "j1", "accepted"), can(3, "j1"))),
		line(5000, "Wait", page("j1", "ready", sub(1, "k1").answered(202, "j1", "accepted"), sub(2, "k1").answered(200, "j1", "accepted"), can(3, "j1").answered(409, "j1", "ready"))),
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
		{"submit", true, 0, 1000, "map[key:k1]", "trace.jsonl:1 screenshot=screenshots/1000.jpeg"},
		{"submit", false, 1, 2000, "map[code:202 id:j1 key:k1 status:accepted]", "trace.jsonl:2 screenshot=screenshots/2000.jpeg"},
		{"show", false, 0, 2000, "map[id:j1 status:accepted]", "trace.jsonl:2 screenshot=screenshots/2000.jpeg"},
		{"submit", false, 2, 3000, "map[code:200 id:j1 key:k1 status:accepted]", "trace.jsonl:3 screenshot=screenshots/3000.jpeg"},
		{"show", false, 0, 3000, "map[id:j1 status:ready]", "trace.jsonl:3 screenshot=screenshots/3000.jpeg"},
		{"cancel", true, 0, 3000, "map[id:j1]", "trace.jsonl:3 screenshot=screenshots/3000.jpeg"},
		{"cancel", false, 7, 5000, "map[code:409 id:j1 status:ready]", "trace.jsonl:5 screenshot=screenshots/5000.jpeg"},
	}
	if len(steps) != len(wants) {
		t.Fatalf("%d records, want %d: %v", len(steps), len(wants), steps)
	}
	for i, w := range wants {
		s := steps[i]
		got := want{s.Action, s.Invocation, s.Call, s.Time.UnixMicro(), fmt.Sprint(s.Args), s.Ref}
		if got != w || s.Seq != uint64(i+1) || s.Recorder != "ui" || s.Actor != "webjobs" || s.Process != "webjobs" || s.Thread != "page" {
			t.Errorf("record %d: %+v (seq %d, %s/%s/%s), want %+v", i+1, got, s.Seq, s.Recorder, s.Actor, s.Process, w)
		}
	}
	traces, issues := qtrace.Reconstruct(steps)
	if len(issues) > 0 || len(traces) != 1 || len(traces[0].Steps) != 5 || len(traces[0].Pending) != 0 || len(traces[0].Issues) != 0 {
		t.Fatalf("reconstructed: %v %+v", issues, traces)
	}
	if got := traces[0].Steps[0].Invoked; !got.Equal(time.UnixMicro(1000)) {
		t.Errorf("submit's completion invoked at %v, want its click's state (1000µs)", got)
	}
}

// A request the trace never shows answered is pending, whatever else
// the page shows: a cancel never answered stays pending though a poll
// then shows the job ready, and a double click's second request stays
// pending though the first was answered with the job.
func TestOnlyItsOwnAnswerCompletesACall(t *testing.T) {
	for _, c := range []struct {
		name  string
		lines []string
		want  string // the action pending
	}{
		{"a click with no answer", []string{
			line(1000, nil, page("", "")),
			line(2000, custom("ui.submit"), page("", "", sub(1, "k1"))),
		}, "submit"},
		{"a cancel never answered, then a poll's ready", []string{
			line(1000, nil, page("", "")),
			line(2000, custom("ui.submit"), page("j1", "accepted", sub(1, "k1").answered(202, "j1", "accepted"))),
			line(3000, custom("ui.cancel"), page("j1", "accepted", sub(1, "k1").answered(202, "j1", "accepted"), can(2, "j1"))),
			line(4000, "Wait", page("j1", "ready", sub(1, "k1").answered(202, "j1", "accepted"), can(2, "j1"))),
		}, "cancel"},
		{"a double click's second request unanswered", []string{
			line(1000, nil, page("", "")),
			line(2000, custom("ui.submit"), page("j1", "accepted", sub(1, "k1").answered(202, "j1", "accepted"), sub(2, "k1"))),
			line(3000, "Wait", page("j1", "ready", sub(1, "k1").answered(202, "j1", "accepted"), sub(2, "k1"))),
		}, "submit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			steps, err := read(t, c.lines...)
			if err != nil {
				t.Fatal(err)
			}
			traces, _ := qtrace.Reconstruct(steps)
			if len(traces) != 1 || len(traces[0].Pending) != 1 || traces[0].Pending[0].Action != c.want {
				t.Fatalf("%+v, want one %s pending", traces, c.want)
			}
		})
	}
}

// What the mapping cannot read is refused with the line and the fix.
func TestRefused(t *testing.T) {
	var full []request
	for n := 1; n <= 8; n++ {
		full = append(full, sub(n, "k").answered(500, "", ""))
	}
	for _, c := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"a submit with no snapshot", []string{
			line(1000, nil, page("", "")),
			line(2000, custom("ui.submit"), nil),
		}, "trace.jsonl:2: ui.submit with no result snapshot"},
		{"a request no click sent", []string{
			line(1000, nil, page("", "")),
			line(2000, "Wait", page("", "", sub(1, "k1"))),
		}, "trace.jsonl:2: request 1 (submit) was sent with no ui.submit before it"},
		{"requests fallen out of the log", []string{
			line(1000, nil, page("", "")),
			line(2000, custom("ui.submit"), page("", "", sub(1, "k1"))),
			line(3000, custom("ui.submit"), page("", "", append(append([]request{}, full[2:]...), sub(9, "k").answered(500, "", ""), sub(10, "k"))...)),
		}, "trace.jsonl:3: requests 2 to 2 fell out of the page's log"},
		{"a log out of order", []string{
			line(1000, nil, page("", "")),
			line(2000, custom("ui.submit"), page("", "", sub(2, "k1"), sub(1, "k1"))),
		}, "the page's log skips from request 2 to 1"},
		{"a custom action not mapped", []string{
			line(1000, nil, page("", "")),
			line(2000, custom("ui.retry"), page("", "")),
		}, "custom action ui.retry is none the adapter maps (ui.cancel, ui.submit)"},
		{"a malformed line", []string{
			"{not json\n",
			line(2000, nil, page("", "")),
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
	last := line(3000, "Wait", page("j1", "ready", sub(1, "k1").answered(202, "j1", "accepted")))
	steps, err := read(t,
		line(1000, nil, page("", "")),
		line(2000, custom("ui.submit"), page("j1", "accepted", sub(1, "k1").answered(202, "j1", "accepted"))),
		last[:len(last)/2],
	)
	var torn *qtrace.TornTail
	if !errors.As(err, &torn) || torn.Line != 3 {
		t.Fatalf("err %v, want a torn tail at line 3", err)
	}
	if len(steps) != 4 || steps[3].Lost != "suffix" || steps[3].LostAfter != 3 || steps[3].Recorder != "ui" {
		t.Fatalf("steps %+v, want the submit's records, its show, and a loss after seq 3", steps)
	}
}

// sub and can are log entries: request n as sent; answered adds its
// answer (code, and the job's id and status; "" for none).
func sub(n int, key string) request { return request{N: n, Op: "submit", Key: key} }
func can(n int, id string) request  { return request{N: n, Op: "cancel", ID: id} }
func (r request) answered(code int, id, status string) request {
	r.Code = &code
	if id != "" {
		r.Job = &job{id, status}
	}
	return r
}

// request is an entry of webjobs' page log, as its page writes it.
type request struct {
	N    int    `json:"n"`
	Op   string `json:"op"`
	Key  string `json:"key,omitempty"`
	ID   string `json:"id,omitempty"`
	Code *int   `json:"code"`
	Job  *job   `json:"job"`
}

type job struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// shown is webjobs' result snapshot: the job shown, and the log.
type shown struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Log    []request `json:"log"`
}

// page is a result snapshot: the job shown, and the log.
func page(id, status string, log ...request) *shown { return &shown{ID: id, Status: status, Log: log} }

// With the defaults a page's log entry is {n, action, code, answer, and
// what the request sent}: the answer's fields are carried as they are,
// one click may send requests of several actions, the recorder is
// "bombadil", and with no Show nothing shown is a step.
func TestDefaults(t *testing.T) {
	entry := func(n int, action string, sent map[string]any, code any, answer any) map[string]any {
		e := map[string]any{"n": n, "action": action, "code": code, "answer": answer}
		for k, v := range sent {
			e[k] = v
		}
		return e
	}
	snap := func(log ...map[string]any) map[string]any { return map[string]any{"log": log, "id": "x"} }
	trace := strings.Join([]string{
		line(1000, nil, nil),
		strings.Replace(line(2000, custom("ui.save"), nil), `"snapshots":[]`, `"snapshots":[{"name":"page","value":`+mustJSON(snap(
			entry(1, "put", map[string]any{"k": "a", "v": 2}, nil, nil),
			entry(2, "audit", nil, nil, nil)))+`}]`, 1),
		strings.Replace(line(3000, "Wait", nil), `"snapshots":[]`, `"snapshots":[{"name":"page","value":`+mustJSON(snap(
			entry(1, "put", map[string]any{"k": "a", "v": 2}, 201, map[string]any{"version": 7, "etag": "e1"}),
			entry(2, "audit", nil, 0, nil)))+`}]`, 1),
	}, "")
	opt := Options{Snapshot: "page", Clicks: map[string][]string{"ui.save": {"put", "audit"}}}
	steps, err := Read(strings.NewReader(trace), opt)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range steps {
		got = append(got, fmt.Sprintf("%s %v %d %v %s", s.Action, s.Invocation, s.Call, s.Args, s.Recorder))
	}
	want := []string{
		"put true 0 map[k:a v:2] bombadil",
		"audit true 0 map[] bombadil",
		"put false 1 map[code:201 etag:e1 k:a v:2 version:7] bombadil",
		"audit false 2 map[code:0] bombadil",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("steps\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// the request log it makes is the protocol's: written and read back,
	// the same steps
	l, err := Log(strings.NewReader(trace), opt)
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := l.Write(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := qdriver.Read(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	for i, s := range back.Steps() {
		if fmt.Sprint(s.Args) != fmt.Sprint(steps[i].Args) || !s.Time.Equal(steps[i].Time) || s.Ref != steps[i].Ref || s.Call != steps[i].Call {
			t.Errorf("step %d read back as %+v, want %+v", i, s, steps[i])
		}
	}
	// the spec's own names are required
	if _, err := Read(strings.NewReader(trace), Options{}); err == nil || !strings.Contains(err.Error(), "names no Snapshot") {
		t.Errorf("no snapshot named: %v", err)
	}
	if _, err := Read(strings.NewReader(trace), Options{Snapshot: "page"}); err == nil || !strings.Contains(err.Error(), "custom action ui.save is none the adapter maps") {
		t.Errorf("no clicks named: %v", err)
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
