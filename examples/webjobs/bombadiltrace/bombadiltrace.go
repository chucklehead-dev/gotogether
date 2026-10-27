// Package bombadiltrace turns a Bombadil run's trace.jsonl into steps of
// model/webjobs.qnt, as a driver that gotogether did not run left them:
// its own times, its own calls, its own order.
//
// Bombadil (https://github.com/antithesishq/bombadil) writes one JSON
// object per line: the state it saw (its time in microseconds, the
// page's URL, a screenshot) after the action that led there (null for
// the first), with the extracts' snapshots ({name, value}). The webjobs
// spec (bombadil/spec.ts) names two custom actions, ui.submit and
// ui.cancel, and one extract, result: {id, status, key} of the job the
// page shows and of the submission the form would send.
//
// The mapping:
//   - each request a click sent (a submit, a cancel: the page logs each
//     as it sends it, numbered n from 1, with what it sent) is an
//     invocation record (qtrace.Step.Invocation), never a step: submit
//     with the key it sent, cancel with the job's id. Its time is the
//     time of the state before the click that sent it, which must be the
//     entry's custom action (ui.submit, ui.cancel).
//   - the request's own answer, once the log shows it, completes it, and
//     only that: submit (key, code, id, status), cancel (id, code,
//     status), with the HTTP status (0: none came) and the job the
//     answer named ("" for none). Its time is the time of the state that
//     first shows the answer. Another change of the page (a poll's
//     answer, a second request's) never completes a call it did not
//     answer.
//   - each change of the job the page shows is a step of its own (show:
//     id, status), whatever caused it.
//   - a request whose answer the trace never shows stays an invocation:
//     qtrace.Reconstruct keeps it as pending, so qtest.Finishes (and
//     validate -require-finish) fails the run. A click is never taken
//     for its success.
//   - every record carries quint.ref: "trace.jsonl:N screenshot=
//     screenshots/F", the line it came from and the screenshot of the
//     state it describes.
//
// The steps are written as they are (Seq, Call, Time, Ref), never
// recorded again through a qobs.Recorder, which would stamp them with
// the time of the conversion and number them in its own order. A trace
// the mapping cannot read is refused with the line and what is wrong (a
// click with no result snapshot, a request no click sent, requests that
// fell out of the page's log between two snapshots, an answer that
// changed): no verdict is better than one on steps the adapter made up.
package bombadiltrace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Options says how a trace's records are named. Every field has a
// default: webjobs' spec and binding.
type Options struct {
	Recorder string // the steps' recorder (default "ui")
	Actor    string // their actor (default "webjobs")
	Process  string // their process (default "webjobs": one with the server's, the app's)
	Snapshot string // the extract read (default "result")
	Submit   string // the custom action of a submission (default "ui.submit")
	Cancel   string // the custom action of a cancel (default "ui.cancel")
	// Name is the trace's name in refs (default "trace.jsonl"): the path
	// of the trace relative to where its screenshots are.
	Name string
}

func (o *Options) defaults() {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&o.Recorder, "ui")
	def(&o.Actor, "webjobs")
	def(&o.Process, "webjobs")
	def(&o.Snapshot, "result")
	def(&o.Submit, "ui.submit")
	def(&o.Cancel, "ui.cancel")
	def(&o.Name, "trace.jsonl")
}

// entry is a line of trace.jsonl, as much of it as is read.
type entry struct {
	Timestamp *int64          `json:"timestamp"`
	Action    json.RawMessage `json:"action"`
	State     struct {
		Screenshot string `json:"screenshot"`
	} `json:"state"`
	Snapshots []struct {
		Name  *string         `json:"name"`
		Value json.RawMessage `json:"value"`
	} `json:"snapshots"`
}

// shown is the result snapshot: the job the page shows, and its log of
// requests.
type shown struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Key    string    `json:"key"`
	Log    []request `json:"log"`
}

// request is an entry of the page's log: a request as sent, and its own
// answer once it came (Code nil until then).
type request struct {
	N    int    `json:"n"`
	Op   string `json:"op"`
	Key  string `json:"key,omitempty"`
	ID   string `json:"id,omitempty"`
	Code *int   `json:"code"`
	Job  *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"job"`
}

// ReadFile reads the trace at path (Read).
func ReadFile(path string, opt Options) ([]qtrace.Step, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f, opt)
}

// Read reads a trace.jsonl and returns its steps and invocation records.
// A last line cut off (no newline, not a whole object: Bombadil killed
// while writing it) is a lost tail: the steps before it, a loss record
// after them (qtrace.MarkLost) and a *qtrace.TornTail error, which a
// caller may report and go on with: the run then has no verdict where
// the lost line could matter. Any other malformed line, or a trace the
// mapping cannot read, is an error and no steps.
func Read(r io.Reader, opt Options) ([]qtrace.Step, error) {
	opt.defaults()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	c := &converter{opt: opt, seen: map[string]string{}, calls: map[int]*call{}}
	lines := bytes.Split(data, []byte("\n"))
	for i, raw := range lines {
		n := i + 1
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(raw, &e); err != nil {
			if i == len(lines)-1 { // no newline after it: the writer stopped here
				out := qtrace.MarkLost(c.steps, qtrace.Lost{Recorder: opt.Recorder, After: c.seq})
				return out, &qtrace.TornTail{Line: n, Text: string(raw)}
			}
			return nil, fmt.Errorf("%s:%d: not a Bombadil trace entry: %v", opt.Name, n, err)
		}
		if err := c.entry(n, &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", opt.Name, n, err)
		}
	}
	return c.steps, nil
}

// call is a request the trace showed sent: its invocation record's seq,
// and whether its answer was recorded.
type call struct {
	seq      uint64
	op       string
	key, id  string
	answered bool
}

type converter struct {
	opt   Options
	steps []qtrace.Step
	seq   uint64
	// the state before this entry's action: its time and ref
	prevTime time.Time
	prevRef  string
	started  bool
	lastN    int               // the last request seen sent
	calls    map[int]*call     // by the page's request number
	seen     map[string]string // job id -> status last shown
}

func (c *converter) entry(n int, e *entry) error {
	if e.Timestamp == nil {
		return errors.New("an entry with no timestamp")
	}
	at := time.UnixMicro(*e.Timestamp)
	ref := fmt.Sprintf("%s:%d", c.opt.Name, n)
	if e.State.Screenshot != "" {
		ref += " screenshot=" + path.Join("screenshots", path.Base(strings.ReplaceAll(e.State.Screenshot, `\`, "/")))
	}
	click, err := c.click(e.Action)
	if err != nil {
		return err
	}
	cur, err := c.result(e)
	if err != nil {
		return err
	}
	if cur == nil {
		if click != "" {
			return fmt.Errorf("%s with no %s snapshot after it: the spec's extract %q is what says what the click sent and what came of it (export it, and keep its name)", click, c.opt.Snapshot, c.opt.Snapshot)
		}
	} else if err := c.observe(click, cur, at, ref); err != nil {
		return err
	}
	c.prevTime, c.prevRef, c.started = at, ref, true
	return nil
}

// click is the entry's custom action, if it is one the mapping knows
// ("" for Bombadil's own actions: Wait, Click, Reload, ...).
func (c *converter) click(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var a struct {
		Custom *struct {
			Name string `json:"name"`
		} `json:"Custom"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Custom == nil {
		return "", nil
	}
	if name := a.Custom.Name; name != c.opt.Submit && name != c.opt.Cancel {
		return "", fmt.Errorf("custom action %s is none the adapter maps (%s, %s): add it to the mapping, or rename it in the spec", name, c.opt.Submit, c.opt.Cancel)
	}
	return a.Custom.Name, nil
}

// result is the entry's result snapshot, or nil if it has none.
func (c *converter) result(e *entry) (*shown, error) {
	for _, s := range e.Snapshots {
		if s.Name == nil || *s.Name != c.opt.Snapshot {
			continue
		}
		var r shown
		if err := json.Unmarshal(s.Value, &r); err != nil {
			return nil, fmt.Errorf("snapshot %s is not {id, status, key, log}: %v", c.opt.Snapshot, err)
		}
		return &r, nil
	}
	return nil, nil
}

// observe records what the state shows that the one before did not: the
// requests the entry's click sent, the answers that came, the job shown.
func (c *converter) observe(click string, cur *shown, at time.Time, ref string) error {
	// the requests sent since the state before: the click's
	first := 0
	for i, r := range cur.Log {
		if i > 0 && r.N != cur.Log[i-1].N+1 {
			return fmt.Errorf("the page's log skips from request %d to %d: it lists requests in the order sent, numbered from 1", cur.Log[i-1].N, r.N)
		}
		if i == 0 {
			first = r.N
		}
	}
	if len(cur.Log) > 0 && cur.Log[len(cur.Log)-1].N > c.lastN && first > c.lastN+1 {
		return fmt.Errorf("requests %d to %d fell out of the page's log between two snapshots (it lists the last %d): their answers are unknown", c.lastN+1, first-1, len(cur.Log))
	}
	for _, r := range cur.Log {
		if r.N <= c.lastN {
			continue
		}
		want := map[string]string{"submit": c.opt.Submit, "cancel": c.opt.Cancel}[r.Op]
		switch {
		case want == "":
			return fmt.Errorf("request %d is a %q, which the mapping does not know (submit, cancel)", r.N, r.Op)
		case click != want:
			return fmt.Errorf("request %d (%s) was sent with no %s before it: a request no click sent", r.N, r.Op, want)
		case !c.started:
			return fmt.Errorf("request %d (%s) in the first state: no state before its click", r.N, r.Op)
		}
		args := map[string]any{"key": r.Key}
		if r.Op == "cancel" {
			args = map[string]any{"id": r.ID}
		}
		c.seq++
		c.steps = append(c.steps, c.step(qtrace.Step{Action: r.Op, Invocation: true, Time: c.prevTime, Ref: c.prevRef, Args: args}))
		c.calls[r.N] = &call{seq: c.seq, op: r.Op, key: r.Key, id: r.ID}
		c.lastN = r.N
	}
	// the answers that came: each completes its own request alone
	for _, r := range cur.Log {
		k := c.calls[r.N]
		if r.Code == nil || k == nil {
			continue
		}
		if k.answered {
			continue
		}
		k.answered = true
		id, status := "", ""
		if r.Job != nil {
			id, status = r.Job.ID, r.Job.Status
		}
		args := map[string]any{"code": int64(*r.Code), "status": status}
		if k.op == "submit" {
			args["key"], args["id"] = k.key, id
		} else {
			args["id"] = k.id
		}
		c.seq++
		c.steps = append(c.steps, c.step(qtrace.Step{Action: k.op, Call: k.seq, Time: at, Ref: ref, Args: args}))
	}
	// the job shown
	if cur.ID != "" && c.seen[cur.ID] != cur.Status {
		c.seen[cur.ID] = cur.Status
		c.seq++
		c.steps = append(c.steps, c.step(qtrace.Step{Action: "show", Time: at, Ref: ref, Args: map[string]any{"id": cur.ID, "status": cur.Status}}))
	}
	return nil
}

func (c *converter) step(s qtrace.Step) qtrace.Step {
	s.Seq, s.Recorder, s.Actor, s.Process, s.Thread = c.seq, c.opt.Recorder, c.opt.Actor, c.opt.Process, "page"
	if !s.Invocation {
		s.Outcome = "ok"
	}
	return s
}
