package journey

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// line is a trace.jsonl entry at ts microseconds: action (nil: none) and
// a result snapshot.
func line(ts int64, action any, result *shown) string {
	e := map[string]any{"timestamp": ts, "action": action,
		"state":     map[string]any{"url": "http://127.0.0.1:1/", "screenshot": fmt.Sprintf("/tmp/out/screenshots/%d.jpeg", ts)},
		"snapshots": []any{map[string]any{"index": 0, "name": "result", "value": result, "time": ts}}, "violations": []any{}}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

func custom(name string) any {
	return map[string]any{"Custom": map[string]any{"name": name, "arguments": []any{}}}
}

// sub and can are entries of the page's log (index.html): request n as
// sent; answered adds its answer (code, and the job's id and status).
func sub(n int, key string) request { return request{N: n, Op: "submit", Key: key} }
func can(n int, id string) request  { return request{N: n, Op: "cancel", ID: id} }
func (r request) answered(code int, id, status string) request {
	r.Code = &code
	if id != "" {
		r.Job = &job{id, status}
	}
	return r
}

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

type shown struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Log    []request `json:"log"`
}

func snap(id, status string, log ...request) *shown { return &shown{ID: id, Status: status, Log: log} }

// A trace's clicks are the page's actions: a double click's two requests
// a Submit twice, a cancel the key of the job it names, the page's own
// polling an await before the next submission (and at the end), and the
// time between a submission and its cancel a sleep.
func TestActions(t *testing.T) {
	s1 := sub(1, "k1").answered(202, "j1", "accepted")
	s2 := sub(2, "k1").answered(200, "j1", "accepted")
	trace := strings.Join([]string{
		line(1000, nil, snap("", "")),
		line(2000, custom("ui.submit"), snap("j1", "accepted", s1, s2)),
		line(3000, "Wait", snap("j1", "ready", s1, s2)),
		line(4000, custom("ui.submit"), snap("j2", "accepted", s1, s2, sub(3, "k2").answered(202, "j2", "accepted"))),
		line(53000, "Wait", snap("j2", "accepted", s1, s2, sub(3, "k2").answered(202, "j2", "accepted"))),
		line(54000, custom("ui.cancel"), snap("j2", "cancelled", s2, sub(3, "k2").answered(202, "j2", "accepted"), can(4, "j2").answered(200, "j2", "cancelled"))),
	}, "")
	as, err := FromTrace(strings.NewReader(trace))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(as), "[submit k1 twice await k1 submit k2 sleep 50ms cancel k2 await k2]"; got != want {
		t.Fatalf("actions %s, want %s", got, want)
	}
}

// Every request of an entry is an action, in the order sent: two
// submissions of other keys two Submits, a cancel beside a submission
// and two cancels each a Cancel; an await polls at the page's 100ms.
func TestActionsEveryRequest(t *testing.T) {
	a1 := sub(1, "k1").answered(202, "j1", "accepted")
	b2 := sub(2, "k2").answered(202, "j2", "accepted")
	trace := strings.Join([]string{
		line(1000, nil, snap("", "")),
		line(2000, custom("ui.submit"), snap("j1", "accepted", a1, b2)),
		line(3000, custom("ui.cancel"), snap("j1", "cancelled", a1, b2, can(3, "j1").answered(200, "j1", "cancelled"))),
		line(4000, custom("ui.cancel"), snap("j2", "cancelled", can(3, "j1").answered(200, "j1", "cancelled"), can(4, "j2").answered(200, "j2", "cancelled"), can(5, "j1").answered(409, "j1", "cancelled"))),
	}, "")
	as, err := FromTrace(strings.NewReader(trace))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(as), "[submit k1 await k1 submit k2 sleep 1ms cancel k1 sleep 1ms cancel k2 cancel k1 await k2]"; got != want {
		t.Fatalf("actions %s, want %s", got, want)
	}
	for _, a := range as {
		if w, ok := a.(AwaitEnd); ok && w.Every != 100*time.Millisecond {
			t.Errorf("%v polls every %v, want the page's 100ms", w, w.Every)
		}
	}
}
