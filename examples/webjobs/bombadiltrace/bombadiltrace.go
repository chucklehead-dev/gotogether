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
//   - a custom action is an invocation record (qtrace.Step.Invocation:
//     submit with the key the page showed before the click, cancel with
//     the id), never a step. Its time is the time of the state before
//     it: the click came after.
//   - a job's id first shown completes the submissions outstanding for
//     the key (submit: key, id, status); a job's status changing
//     completes the cancels outstanding for it (cancel: id, status), or,
//     with none, is a step of its own if it is ready (ready: id). The
//     page shows one status per job, so one change answers every click
//     outstanding on it: a second click's answer can change nothing
//     there. Its time is the time of the state that shows it.
//   - a click whose outcome the trace never shows stays an invocation:
//     qtrace.Reconstruct keeps it as pending, so qtest.Finishes (and
//     validate -require-settle) fails the run. A click is never taken
//     for its success.
//   - every record carries quint.ref: "trace.jsonl:N screenshot=
//     screenshots/F", the line it came from and the screenshot of the
//     state it describes.
//
// The steps are written as they are (Seq, Call, Time, Ref), never
// recorded again through a qobs.Recorder, which would stamp them with
// the time of the conversion and number them in its own order. A trace
// outside the page's contract (a submit with no result snapshot, an id
// no submission explains, a status no click or job explains) is refused
// with the line and what is wrong: no verdict is better than one on
// steps the adapter made up.
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

// shown is the result snapshot.
type shown struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Key    string `json:"key"`
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
// the lost line could matter. Any other malformed line, or a trace
// outside the page's contract, is an error and no steps.
func Read(r io.Reader, opt Options) ([]qtrace.Step, error) {
	opt.defaults()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	c := &converter{opt: opt, seen: map[string]string{}, submits: map[string][]uint64{}, cancels: map[string][]uint64{}}
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

type converter struct {
	opt   Options
	steps []qtrace.Step
	seq   uint64
	// the state before this entry's action: its result and time
	prev     *shown
	prevTime time.Time
	prevRef  string
	seen     map[string]string   // job id -> status last shown
	submits  map[string][]uint64 // key -> invocations outstanding
	cancels  map[string][]uint64 // id -> invocations outstanding
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
	cur, err := c.result(e)
	if err != nil {
		return err
	}
	if err := c.action(n, e.Action, cur); err != nil {
		return err
	}
	if cur != nil {
		if err := c.observe(cur, at, ref); err != nil {
			return err
		}
	}
	c.prev, c.prevTime, c.prevRef = cur, at, ref
	return nil
}

// result is the entry's result snapshot, or nil if it has none.
func (c *converter) result(e *entry) (*shown, error) {
	for _, s := range e.Snapshots {
		if s.Name == nil || *s.Name != c.opt.Snapshot {
			continue
		}
		var r shown
		if err := json.Unmarshal(s.Value, &r); err != nil {
			return nil, fmt.Errorf("snapshot %s is not {id, status, key}: %v", c.opt.Snapshot, err)
		}
		return &r, nil
	}
	return nil, nil
}

// action records the invocation of the entry's action, if it is one the
// mapping knows. cur is the entry's result: an action needs the result
// of the state before it, and of its own, to be read at all.
func (c *converter) action(n int, raw json.RawMessage, cur *shown) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var a struct {
		Custom *struct {
			Name string `json:"name"`
		} `json:"Custom"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Custom == nil {
		return nil // Bombadil's own actions (Wait, Click, Reload, ...): no call of the model's
	}
	name := a.Custom.Name
	if name != c.opt.Submit && name != c.opt.Cancel {
		return fmt.Errorf("custom action %s is none the adapter maps (%s, %s): add it to the mapping, or rename it in the spec", name, c.opt.Submit, c.opt.Cancel)
	}
	if c.prev == nil || cur == nil {
		return fmt.Errorf("%s with no %s snapshot before or after it: the spec's extract %q is what says what was clicked and what came of it (export it, and keep its name)", name, c.opt.Snapshot, c.opt.Snapshot)
	}
	switch name {
	case c.opt.Submit:
		if c.prev.Key == "" {
			return fmt.Errorf("%s with no key in the %s snapshot before it: the form's data-key is the submission's", name, c.opt.Snapshot)
		}
		seq := c.invoke("submit", "key", c.prev.Key)
		c.submits[c.prev.Key] = append(c.submits[c.prev.Key], seq)
	case c.opt.Cancel:
		if c.prev.ID == "" {
			return fmt.Errorf("%s with no job shown before it: a cancel names the job the page shows", name)
		}
		seq := c.invoke("cancel", "id", c.prev.ID)
		c.cancels[c.prev.ID] = append(c.cancels[c.prev.ID], seq)
	}
	return nil
}

// invoke records an invocation at the state before the click.
func (c *converter) invoke(action, k, v string) uint64 {
	c.seq++
	c.steps = append(c.steps, c.step(qtrace.Step{Action: action, Invocation: true, Time: c.prevTime, Ref: c.prevRef,
		Args: map[string]any{k: v}}))
	return c.seq
}

// observe records what the state shows that the one before did not.
func (c *converter) observe(cur *shown, at time.Time, ref string) error {
	if cur.ID == "" {
		return nil // nothing shown (the page as loaded)
	}
	last, known := c.seen[cur.ID]
	switch {
	case !known:
		var key string
		for k, calls := range c.submits {
			if len(calls) == 0 {
				continue
			}
			if key != "" {
				return fmt.Errorf("job %s shown with submissions of two keys outstanding (%s, %s): which one it answers is unknown", cur.ID, key, k)
			}
			key = k
		}
		if key == "" {
			return fmt.Errorf("job %s shown with no %s outstanding: an id no click explains (a second id for one submission?)", cur.ID, c.opt.Submit)
		}
		for _, call := range c.submits[key] {
			c.complete(call, "submit", at, ref, "key", key, "id", cur.ID, "status", cur.Status)
		}
		delete(c.submits, key)
	case last == cur.Status:
		return nil
	case len(c.cancels[cur.ID]) > 0:
		for _, call := range c.cancels[cur.ID] {
			c.complete(call, "cancel", at, ref, "id", cur.ID, "status", cur.Status)
		}
		delete(c.cancels, cur.ID)
	case cur.Status == "ready":
		c.seq++
		c.steps = append(c.steps, c.step(qtrace.Step{Action: "ready", Time: at, Ref: ref, Args: map[string]any{"id": cur.ID}}))
	default:
		return fmt.Errorf("job %s shown %s after %s with no %s outstanding: a change no click or job explains", cur.ID, cur.Status, last, c.opt.Cancel)
	}
	c.seen[cur.ID] = cur.Status
	return nil
}

// complete records call's completion: the step, at the state showing it.
func (c *converter) complete(call uint64, action string, at time.Time, ref string, kv ...string) {
	args := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		args[kv[i]] = kv[i+1]
	}
	c.seq++
	c.steps = append(c.steps, c.step(qtrace.Step{Action: action, Call: call, Time: at, Ref: ref, Args: args}))
}

func (c *converter) step(s qtrace.Step) qtrace.Step {
	s.Seq, s.Recorder, s.Actor, s.Process, s.Thread = c.seq, c.opt.Recorder, c.opt.Actor, c.opt.Process, "page"
	if !s.Invocation {
		s.Outcome = "ok"
	}
	return s
}
