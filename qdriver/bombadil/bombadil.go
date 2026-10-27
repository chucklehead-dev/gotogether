// Package bombadil turns a Bombadil run's trace.jsonl into a request log
// (package qdriver), and so into steps: a driver gotogether did not run
// left them, with its own times, its own calls, its own order.
//
// Bombadil (https://github.com/antithesishq/bombadil) writes one JSON
// object per line: the state it saw (its time in microseconds, the
// page's URL, a screenshot) after the action that led there (null for
// the first), with the extracts' snapshots ({name, value}). A click's
// effect is not in the trace: the page's display rarely says which click
// caused what (a poll's answer, a second click's and a cancel's can all
// change one status). So the page keeps a log of the requests it sends,
// numbered as sent, each with what it sent and, once it came, its own
// answer, and the spec's extract (Options.Snapshot) reads it:
//
//	{"log": [{"n": 1, "action": "submit", "key": "k1", "code": 202, "answer": {"id": "j1", "status": "accepted"}},
//	         {"n": 2, "action": "cancel", "id": "j1", "code": null, "answer": null}],
//	 "id": "j1", "status": "accepted"}
//
// An entry's fields are its number, its action, its code (null until
// its answer came; 0 for none, a network error) and its answer (the
// answer's fields the model reads), named by Options.Fields; every other
// field is what the request sent. The page lists its last requests (a
// few: a snapshot is in every trace line), in the order sent.
//
// The mapping:
//   - each request a click sent is a request record (an invocation),
//     with what the request sent, at the time of the state before the
//     click, which must be the entry's custom action that sends it
//     (Options.Clicks): a request no click sent is refused.
//   - the request's own answer, once the log shows it, is its response
//     (the code, the answer's fields), at the time of the state that
//     first shows it. Another change of the page never completes a call
//     it did not answer.
//   - each change of what the page shows (Options.Show) is a step of its
//     own, whatever caused it.
//   - a request whose answer the trace never shows stays an invocation:
//     pending, so a run that must finish does not. A click is never taken
//     for its success.
//   - every record carries a ref, "trace.jsonl:N screenshot=
//     screenshots/F": the line it came from and the screenshot of the
//     state it describes.
//
// A trace the mapping cannot read is refused with the line and what is
// wrong (a click with no snapshot, a request no click sent, requests
// that fell out of the page's log between two snapshots, an unmapped
// custom action): no verdict is better than one on steps the adapter
// made up.
package bombadil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qdriver"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Options says how a trace's records are named and read. Snapshot and
// Clicks name the spec's own; the rest have defaults.
type Options struct {
	// Header names the steps (qdriver.Header: recorder, actor, process,
	// thread). Its Recorder defaults to "bombadil", its Protocol is
	// qdriver.Protocol.
	Header qdriver.Header
	// Snapshot is the spec's extract that holds the page's request log
	// and what it shows (required).
	Snapshot string
	// Log is the snapshot's field holding the request log (default "log").
	Log string
	// Fields name a log entry's fields (each defaults to its own name).
	Fields Fields
	// Clicks maps each of the spec's custom actions that sends requests
	// to the actions of the requests it may send: ui.submit to
	// [submit]. A custom action not in it is refused, as is a request of
	// an action no click sends.
	Clicks map[string][]string
	// Show, if set, makes each change of what the page shows a step.
	Show *Show
	// Name is the trace's name in refs (default "trace.jsonl"): its path
	// relative to where its screenshots directory is.
	Name string
}

// Fields name a page log entry's fields; every other field of an entry
// is what its request sent.
type Fields struct {
	Number string // the request's number, from 1 as sent (default "n")
	Action string // the model's action it invokes (default "action")
	Code   string // its answer's status, null until it came, 0 for none (default "code")
	// Answer is the answer's fields the model reads, an object, null
	// until it came or if it named none (default "answer")
	Answer string
	// AnswerFields, if set, are the answer's fields a response carries,
	// each "" when the answer has none (a null answer: an error's), so
	// every completion has the arguments its action takes; nil carries
	// the answer's fields as they are
	AnswerFields []string
}

// Show says what of the snapshot is the page's display: Fields of it
// (Key among them), a step named Action each time they change for the
// Key's value. A snapshot whose Key is empty or absent shows nothing.
type Show struct {
	Action string   // the step's action (default "show")
	Key    string   // the field naming what is shown (a job's id)
	Fields []string // the fields the step carries (default: Key alone)
}

func (o *Options) defaults() error {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&o.Header.Recorder, "bombadil")
	o.Header.Protocol = qdriver.Protocol
	def(&o.Log, "log")
	def(&o.Name, "trace.jsonl")
	def(&o.Fields.Number, "n")
	def(&o.Fields.Action, "action")
	def(&o.Fields.Code, "code")
	def(&o.Fields.Answer, "answer")
	if o.Snapshot == "" {
		return errors.New("bombadil.Options names no Snapshot: the spec's extract that holds the page's request log")
	}
	if o.Show != nil {
		show := *o.Show
		def(&show.Action, "show")
		if show.Key == "" {
			return errors.New("bombadil.Options.Show names no Key: the snapshot's field naming what the page shows")
		}
		if len(show.Fields) == 0 {
			show.Fields = []string{show.Key}
		}
		o.Show = &show
	}
	return nil
}

// State is one line of a trace, as the mapping reads it.
//
// Experimental: State, Request and States are the shape of Bombadil's
// trace as this package reads it, for a mapping of one's own (webjobs'
// rerunnable action lists); they follow Bombadil's format, which is not
// versioned, and may change without notice. Log and the request log it
// gives are the stable way in.
type State struct {
	Line int       // its line in the trace, from 1
	Time time.Time // Bombadil's timestamp
	Ref  string    // "trace.jsonl:N screenshot=screenshots/F"
	// Click is the custom action that led to the state, if it is one of
	// Options.Clicks ("" for none, or one of Bombadil's own: Wait, Click,
	// Reload, ...)
	Click string
	// Snapshot: the state has the snapshot (Options.Snapshot); Log and
	// Shown are read from it
	Snapshot bool
	Log      []Request      // the page's log, in the order sent
	Shown    map[string]any // the snapshot's Show fields (nil without Show)
}

// Request is an entry of the page's log: a request as sent, and its own
// answer once it came. Experimental, as State is.
type Request struct {
	N      int
	Action string
	Args   map[string]any // what it sent: the entry's other fields
	Code   *int64         // nil until its answer came; 0: none came
	Answer map[string]any // the answer's fields (nil: none)
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

// States reads a trace's lines. A last line cut off (no newline, not a
// whole object: Bombadil killed while writing it) returns the states
// before it with a *qtrace.TornTail error; any other malformed line is
// an error and no states. Experimental, as State is.
func States(r io.Reader, opt Options) ([]State, error) {
	if err := opt.defaults(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []State
	lines := bytes.Split(data, []byte("\n"))
	for i, raw := range lines {
		n := i + 1
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(raw, &e); err != nil {
			if i == len(lines)-1 { // no newline after it: the writer stopped here
				return out, &qtrace.TornTail{Line: n, Text: string(raw)}
			}
			return nil, fmt.Errorf("%s:%d: not a Bombadil trace entry: %v", opt.Name, n, err)
		}
		s, err := opt.state(n, &e)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", opt.Name, n, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func (o *Options) state(n int, e *entry) (State, error) {
	if e.Timestamp == nil {
		return State{}, errors.New("an entry with no timestamp")
	}
	s := State{Line: n, Time: time.UnixMicro(*e.Timestamp), Ref: fmt.Sprintf("%s:%d", o.Name, n)}
	if e.State.Screenshot != "" {
		s.Ref += " screenshot=" + path.Join("screenshots", path.Base(strings.ReplaceAll(e.State.Screenshot, `\`, "/")))
	}
	var err error
	if s.Click, err = o.click(e.Action); err != nil {
		return State{}, err
	}
	for _, snap := range e.Snapshots {
		if snap.Name == nil || *snap.Name != o.Snapshot {
			continue
		}
		s.Snapshot = true
		if err := o.snapshot(&s, snap.Value); err != nil {
			return State{}, err
		}
		break
	}
	if !s.Snapshot && s.Click != "" {
		return State{}, fmt.Errorf("%s with no %s snapshot after it: the spec's extract %q is what says what the click sent and what came of it (export it, and keep its name)", s.Click, o.Snapshot, o.Snapshot)
	}
	return s, nil
}

// click is the entry's custom action, if it is one the mapping knows
// ("" for Bombadil's own actions: Wait, Click, Reload, ...).
func (o *Options) click(raw json.RawMessage) (string, error) {
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
	if _, ok := o.Clicks[a.Custom.Name]; !ok {
		return "", fmt.Errorf("custom action %s is none the adapter maps (%s): add it to Options.Clicks, or rename it in the spec", a.Custom.Name, strings.Join(sorted(o.Clicks), ", "))
	}
	return a.Custom.Name, nil
}

// snapshot reads the page's log and what it shows from the snapshot.
func (o *Options) snapshot(s *State, raw json.RawMessage) error {
	var v map[string]json.RawMessage
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("snapshot %s is not an object with a %s: %v", o.Snapshot, o.Log, err)
	}
	var log []map[string]json.RawMessage
	if l, ok := v[o.Log]; !ok {
		return fmt.Errorf("snapshot %s has no %s: the page's log of its requests, which the extract reads", o.Snapshot, o.Log)
	} else if err := json.Unmarshal(l, &log); err != nil {
		return fmt.Errorf("snapshot %s's %s is not a list of requests: %v", o.Snapshot, o.Log, err)
	}
	f := o.Fields
	for _, e := range log {
		var r Request
		if json.Unmarshal(e[f.Number], &r.N) != nil || json.Unmarshal(e[f.Action], &r.Action) != nil || r.N < 1 || r.Action == "" {
			return fmt.Errorf("a request in %s's %s with no %s (a number from 1) or no %s: each entry names its number and its action", o.Snapshot, o.Log, f.Number, f.Action)
		}
		if c, ok := e[f.Code]; ok && string(c) != "null" {
			var code int64
			if err := json.Unmarshal(c, &code); err != nil {
				return fmt.Errorf("request %d's %s is not a status: %v", r.N, f.Code, err)
			}
			r.Code = &code
		}
		if a, ok := e[f.Answer]; ok && string(a) != "null" {
			if err := unmarshal(a, &r.Answer); err != nil {
				return fmt.Errorf("request %d's %s is not an object: %v", r.N, f.Answer, err)
			}
		}
		r.Args = map[string]any{}
		for k, raw := range e {
			if k == f.Number || k == f.Action || k == f.Code || k == f.Answer {
				continue
			}
			var x any
			if err := unmarshal(raw, &x); err != nil {
				return err
			}
			r.Args[k] = x
		}
		s.Log = append(s.Log, r)
	}
	if o.Show != nil {
		s.Shown = map[string]any{}
		for _, k := range o.Show.Fields {
			if raw, ok := v[k]; ok {
				var x any
				if err := unmarshal(raw, &x); err != nil {
					return err
				}
				s.Shown[k] = x
			}
		}
	}
	return nil
}

// unmarshal decodes JSON as a step's value: a whole number an int64.
func unmarshal(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	switch x := v.(type) {
	case *any:
		*x = number(*x)
	case *map[string]any:
		for k, e := range *x {
			(*x)[k] = number(e)
		}
	}
	return nil
}

func number(v any) any {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	if i, err := n.Int64(); err == nil {
		return i
	}
	if f, err := n.Float64(); err == nil {
		return f
	}
	return n.String()
}

// LogFile reads the trace at path (Log).
func LogFile(path string, opt Options) (*qdriver.Log, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Log(f, opt)
}

// Log reads a trace.jsonl as a request log: its steps are Log.Steps
// (qtest.Validate's), and gotogether validate reads it once written
// (qdriver.Log.Write). A torn last line returns the log of the lines
// before it with the *qtrace.TornTail (Log.Torn), which a caller may
// report and go on with: its steps end with a loss record, and the run
// has no verdict where the lost line could matter.
func Log(r io.Reader, opt Options) (*qdriver.Log, error) {
	if err := opt.defaults(); err != nil {
		return nil, err
	}
	states, err := States(r, opt)
	var torn *qtrace.TornTail
	if err != nil && !errors.As(err, &torn) {
		return nil, err
	}
	sends := map[string][]string{} // a request's action -> the clicks that send it
	for click, actions := range opt.Clicks {
		for _, a := range actions {
			sends[a] = append(sends[a], click)
		}
	}
	for _, c := range sends {
		sort.Strings(c)
	}
	c := &converter{opt: &opt, sends: sends, sent: map[int]*Request{}, answered: map[int]bool{}, shown: map[string]string{}}
	for i := range states {
		if err := c.state(&states[i], i > 0); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", opt.Name, states[i].Line, err)
		}
		c.prevTime, c.prevRef = states[i].Time, states[i].Ref
	}
	l, nerr := qdriver.New(opt.Header, c.records)
	if nerr != nil { // the mapping made records the protocol refuses: the adapter's bug, not the trace's
		return nil, fmt.Errorf("%s: %w", opt.Name, nerr)
	}
	l.Torn = torn
	return l, err
}

type converter struct {
	opt     *Options
	sends   map[string][]string
	records []qdriver.Record
	// the state before this one: its time and ref
	prevTime time.Time
	prevRef  string
	lastN    int              // the last request seen sent
	sent     map[int]*Request // by the page's number
	answered map[int]bool
	shown    map[string]string // what is shown -> its fields last shown
}

// state adds the records of what s shows that the state before did not:
// the requests its click sent, the answers that came, the display.
func (c *converter) state(s *State, started bool) error {
	if !s.Snapshot {
		return nil
	}
	first := 0
	for i, r := range s.Log {
		if i > 0 && r.N != s.Log[i-1].N+1 {
			return fmt.Errorf("the page's log skips from request %d to %d: it lists requests in the order sent, numbered from 1", s.Log[i-1].N, r.N)
		}
		if i == 0 {
			first = r.N
		}
	}
	if len(s.Log) > 0 && s.Log[len(s.Log)-1].N > c.lastN && first > c.lastN+1 {
		return fmt.Errorf("requests %d to %d fell out of the page's log between two snapshots (it lists the last %d): their answers are unknown", c.lastN+1, first-1, len(s.Log))
	}
	// the requests sent since the state before: the click's
	for i := range s.Log {
		r := &s.Log[i]
		if r.N <= c.lastN {
			continue
		}
		clicks, ok := c.sends[r.Action]
		switch {
		case !ok:
			return fmt.Errorf("request %d is a %q, which the mapping does not know (%s): add the click that sends it to Options.Clicks", r.N, r.Action, strings.Join(sorted(c.sends), ", "))
		case !slices.Contains(clicks, s.Click):
			return fmt.Errorf("request %d (%s) was sent with no %s before it: a request no click sent", r.N, r.Action, strings.Join(clicks, " or "))
		case !started:
			return fmt.Errorf("request %d (%s) in the first state: no state before its click", r.N, r.Action)
		}
		c.records = append(c.records, qdriver.Record{Event: qdriver.Request, Req: strconv.Itoa(r.N), Action: r.Action,
			Time: c.prevTime, Ref: c.prevRef, Args: r.Args})
		c.sent[r.N] = r
		c.lastN = r.N
	}
	// the answers that came: each completes its own request alone
	for _, r := range s.Log {
		if r.Code == nil || c.sent[r.N] == nil || c.answered[r.N] {
			continue
		}
		c.answered[r.N] = true
		args := r.Answer
		if fields := c.opt.Fields.AnswerFields; fields != nil {
			args = map[string]any{}
			for _, k := range fields {
				args[k] = ""
				if v, ok := r.Answer[k]; ok {
					args[k] = v
				}
			}
		}
		code := *r.Code
		c.records = append(c.records, qdriver.Record{Event: qdriver.Response, Req: strconv.Itoa(r.N), Time: s.Time, Ref: s.Ref,
			Code: &code, Args: args})
	}
	// what the page shows
	if show := c.opt.shown(s); show != nil {
		key := fmt.Sprint(show[c.opt.Show.Key])
		if now := fmt.Sprint(show); c.shown[key] != now {
			c.shown[key] = now
			c.records = append(c.records, qdriver.Record{Event: qdriver.Step, Action: c.opt.Show.Action, Time: s.Time, Ref: s.Ref, Args: show})
		}
	}
	return nil
}

// shown is what s shows (Options.Show's fields, a missing one ""), or
// nil if its Key is empty or absent, or there is no Show.
func (o *Options) shown(s *State) map[string]any {
	if o.Show == nil || s.Shown == nil {
		return nil
	}
	if k, ok := s.Shown[o.Show.Key]; !ok || k == "" || k == nil {
		return nil
	}
	out := map[string]any{}
	for _, f := range o.Show.Fields {
		out[f] = ""
		if v, ok := s.Shown[f]; ok && v != nil {
			out[f] = v
		}
	}
	return out
}

func sorted[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
