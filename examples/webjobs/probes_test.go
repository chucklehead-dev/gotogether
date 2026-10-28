package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Journeys constructed whole (Bombadil's trace and the server's log),
// each a way a run can go wrong that a review found the first mapping
// and model passed: each is caught now, by the claim it states.

const t0 = 1793036761000000 // µs

// req is a log entry: request n sent (op, key or id), and once code is
// set, its answer naming the job (jid, jstatus).
type req struct {
	n            int
	op, arg      string
	code         int // -1: not answered yet
	jid, jstatus string
}

func (r req) json() map[string]any {
	m := map[string]any{"n": r.n, "op": r.op, "code": nil, "job": nil}
	if r.op == "submit" {
		m["key"] = r.arg
	} else {
		m["id"] = r.arg
	}
	if r.code >= 0 {
		m["code"] = r.code
		if r.jid != "" {
			m["job"] = map[string]any{"id": r.jid, "status": r.jstatus}
		}
	}
	return m
}

// at is a trace line at t0+ms: action (nil, "Wait", or a custom
// action's name) and the page: the job shown and the log.
func at(ms int64, action any, id, status string, log ...req) string {
	if s, ok := action.(string); ok && strings.HasPrefix(s, "ui.") {
		action = map[string]any{"Custom": map[string]any{"name": s, "arguments": []any{}}}
	}
	var l []any
	for _, r := range log {
		l = append(l, r.json())
	}
	ts := t0 + ms*1000
	e := map[string]any{"timestamp": ts, "action": action,
		"state":     map[string]any{"url": "http://127.0.0.1:1/", "screenshot": fmt.Sprintf("/x/screenshots/%d.jpeg", ts)},
		"snapshots": []any{map[string]any{"index": 0, "name": "result", "time": ts, "value": map[string]any{"id": id, "status": status, "key": "", "log": l}}}}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

// server is the server's log: steps at t0+ms, then drained and a settle.
func server(steps ...[]any) []string {
	var out []string
	add := func(ms int64, action string, kv ...string) {
		m := map[string]any{"quint.action": action, "quint.actor": "webjobs", "quint.outcome": "ok", "quint.process": "webjobs",
			"quint.recorder": "server", "quint.seq": len(out) + 1, "quint.time_unix_nano": (t0 + ms*1000) * 1000}
		for i := 0; i+1 < len(kv); i += 2 {
			m["quint.arg."+kv[i]] = kv[i+1]
		}
		if action == "settled" {
			m["quint.outcome"], m["quint.settled"] = "", true
		}
		b, _ := json.Marshal(m)
		out = append(out, string(b)+"\n")
	}
	var last int64
	for _, s := range steps {
		ms := int64(s[0].(int))
		var kv []string
		for _, x := range s[2:] {
			kv = append(kv, x.(string))
		}
		add(ms, s[1].(string), kv...)
		last = ms
	}
	add(last+300, "drained")
	add(last+301, "settled")
	return out
}

func probe(t *testing.T, srv, trace []string) *qtest.Run {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ServerFile), []byte(strings.Join(srv, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, TraceFile), []byte(strings.Join(trace, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, err := Steps(dir)
	if err != nil {
		t.Fatal(err)
	}
	return qtest.Validate(t, "webjobs.binding.yaml", steps)
}

func TestProbes(t *testing.T) {
	if qtest.Mutant() != "" {
		t.Skip("webjobs as it is")
	}
	t.Parallel()
	qtest.RequireQuint(t)
	s1 := req{1, "submit", "k1", 202, "j1", "accepted"}
	c2 := req{2, "cancel", "j1", -1, "", ""}

	// a cancel never answered (a 500 the page dropped, a hang): a poll
	// then shows the job ready, which completes nothing
	t.Run("cancel never answered", func(t *testing.T) {
		t.Parallel()
		run := probe(t, server([]any{150, "created", "key", "k1", "id", "j1"}, []any{600, "finished", "id", "j1"}), []string{
			at(0, nil, "", ""),
			at(200, "ui.submit", "j1", "accepted", s1),
			at(300, "ui.cancel", "j1", "accepted", s1, c2),
			at(700, "Wait", "j1", "ready", s1, c2),
		})
		run.Expect(t, qtest.Pending("cancel"))
		qtest.Runs(run).Expect(t, qtest.None(qtest.Finishes()))
	})
	// the page stuck at accepted while the server committed and drained
	t.Run("ready never shown", func(t *testing.T) {
		t.Parallel()
		probe(t, server([]any{150, "created", "key", "k1", "id", "j1"}, []any{600, "finished", "id", "j1"}), []string{
			at(0, nil, "", ""),
			at(200, "ui.submit", "j1", "accepted", s1),
			at(800, "Wait", "j1", "accepted", s1),
		}).Expect(t, qtest.Violates(validate.EventuallyName("pageShowsEnds")))
	})
	// ... or shown ready only long after the commit
	t.Run("ready shown late", func(t *testing.T) {
		fullOnly(t)
		t.Parallel()
		probe(t, server([]any{150, "created", "key", "k1", "id", "j1"}, []any{600, "finished", "id", "j1"}), []string{
			at(0, nil, "", ""),
			at(200, "ui.submit", "j1", "accepted", s1),
			at(1500, "Wait", "j1", "ready", s1),
		}).Expect(t, qtest.Violates("within 500ms after unseenReady: readySeen"))
	})
	t.Run("finished twice", func(t *testing.T) {
		fullOnly(t)
		t.Parallel()
		probe(t, server([]any{150, "created", "key", "k1", "id", "j1"}, []any{600, "finished", "id", "j1"}, []any{650, "finished", "id", "j1"}), []string{
			at(0, nil, "", ""),
			at(200, "ui.submit", "j1", "accepted", s1),
			at(700, "Wait", "j1", "ready", s1),
		}).Expect(t, qtest.Violates("finishedOnce"))
	})
	t.Run("committed after its cancel", func(t *testing.T) {
		fullOnly(t)
		t.Parallel()
		c := req{2, "cancel", "j1", 200, "j1", "cancelled"}
		probe(t, server([]any{150, "created", "key", "k1", "id", "j1"}, []any{310, "cancelled", "id", "j1"}, []any{600, "finished", "id", "j1"}), []string{
			at(0, nil, "", ""),
			at(200, "ui.submit", "j1", "accepted", s1),
			at(400, "ui.cancel", "j1", "cancelled", s1, c),
			at(700, "Wait", "j1", "cancelled", s1, c),
		}).Expect(t, qtest.Violates("noCommitAfterCancel"))
	})
	// a double click's second request answered 500: an answer, and a
	// failure
	t.Run("a request answered with a server error", func(t *testing.T) {
		fullOnly(t)
		t.Parallel()
		probe(t, server([]any{150, "created", "key", "k1", "id", "j1"}, []any{600, "finished", "id", "j1"}), []string{
			at(0, nil, "", ""),
			at(200, "ui.submit", "j1", "accepted", s1, req{2, "submit", "k1", 500, "", ""}),
			at(700, "Wait", "j1", "ready", s1, req{2, "submit", "k1", 500, "", ""}),
		}).Expect(t, qtest.Violates("noFailedRequest"))
	})
	// the key a click sent is the log's, not the state's before it: a
	// late answer renewed the form's key between Bombadil's snapshot and
	// its click (the faithful page did this), and the journey finishes
	t.Run("the key sent, not the key shown", func(t *testing.T) {
		t.Parallel()
		s3 := req{3, "submit", "k3", 202, "j2", "accepted"}
		probe(t, server([]any{150, "created", "key", "k1", "id", "j1"}, []any{350, "finished", "id", "j1"},
			[]any{520, "created", "key", "k3", "id", "j2"}, []any{800, "finished", "id", "j2"}), []string{
			at(0, nil, "", ""),
			at(200, "ui.submit", "j1", "accepted", s1),
			at(300, "ui.cancel", "j1", "accepted", s1, c2),
			at(400, "Wait", "j1", "ready", s1, req{2, "cancel", "j1", 409, "j1", "ready"}),
			at(600, "ui.submit", "j2", "accepted", s1, req{2, "cancel", "j1", 409, "j1", "ready"}, s3),
			at(850, "Wait", "j2", "ready", s1, req{2, "cancel", "j1", 409, "j1", "ready"}, s3),
		}).Expect(t, qtest.Finishes())
	})
}

// fullOnly skips a probe under -short whose claim a mutant's client run
// states per push too: ready shown late (statusStale), finished twice
// (rerunFinished), committed after its cancel (commitAfterCancel), a
// request answered 500 (cancelBroken).
func fullOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("a mutant's client run states this per push: full mode")
	}
}
