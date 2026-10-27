// Package qdriver reads a request log: what an external driver (a
// browser explorer, an end-to-end suite, a load tool) sent the system and
// what came back, in a small JSONL protocol the driver writes itself, and
// turns it into steps for the validator: each request an invocation
// record, completed only by that request's own answer.
//
// # The protocol, gotogether.requests/1
//
// One JSON object per line. The first is the header:
//
//	{"protocol": "gotogether.requests/1", "recorder": "ui", "actor": "webjobs", "process": "webjobs", "thread": "page", "driver": "playwright 1.56.1"}
//
// protocol is required and exact: a later version is refused with the
// fix (upgrade gotogether), never read as this one. recorder is required:
// the log's records are numbered in it, so two logs joined must not share
// one. actor (default "default"), process (default the recorder) and
// thread (default the recorder: one tab's actions are in program order)
// name the steps as a recorder's options would; driver is free text.
//
// Every other line is a record, an "event" of one of three kinds, each
// with its "time" (RFC 3339, the driver's own clock; in the log's order,
// never going back):
//
//	{"event": "request", "req": "1", "action": "submit", "time": "2026-10-27T09:00:00.120000Z", "args": {"key": "k1"}, "corr": "t1-1", "ref": "trace.zip screenshot=s1.png"}
//	{"event": "response", "req": "1", "time": "2026-10-27T09:00:00.164000Z", "code": 202, "args": {"id": "j1", "status": "accepted"}}
//	{"event": "step", "action": "show", "time": "2026-10-27T09:00:00.170000Z", "args": {"id": "j1", "status": "accepted"}}
//
// The kinds:
//   - request: the driver sent a request. req is its identity in the log
//     (a string, unique), action the model's action it invokes, args
//     what the request sent (the key in its body, the id in its URL),
//     never the state shown before it was sent. It is an invocation
//     record, never a step: a request sent is not an operation done.
//   - response: that request's own answer, and only it completes the
//     call. code is required: the answer's status (an HTTP status), 0 if
//     none came (a network error, which error may say). args are the
//     answer's fields the model reads; the completion's arguments are
//     what the request sent, then the answer's fields it did not send,
//     then code. outcome is the step's (default "ok": the call completed
//     with the code it got, and the model judges the code). A response
//     of no request, or a second one, is refused; a request with none
//     stays pending (qtrace.Trace.Pending), so a run that needs every
//     call answered does not finish.
//   - step: something the driver saw that answers no request (the page's
//     display changed, a poll's answer): a step of its own, which
//     completes nothing. It names no req.
//
// Any record may carry obs (observations of the state after it, on a
// response or a step), thread (another tab's program order, on a request
// or a step; a response is its request's), order (domain clocks,
// qtrace.Step.Order) and ref (the artifact the record came from: a trace
// line, a screenshot, qtrace.Step.Ref). A request's corr (the id it
// carried to the server, a request-id header) is put in its invocation's
// and completion's refs as corr=<id>, so a failure names the server
// records to look for; no check reads a ref. A field the protocol does
// not name is refused, so a misspelt "arg" is not silently dropped.
//
// A last line cut off (no newline, not a whole object: the driver killed
// while writing it) is a lost tail: the records before it, a loss record
// after them, and a *qtrace.TornTail, as qtrace.ReadJSONL reads a step
// log. Any other malformed line is an error naming it.
//
// # Reading one
//
// gotogether validate reads such a log as it reads a step log (package
// input detects it by its header), so `gotogether validate -binding
// app.binding.yaml server.jsonl requests.jsonl` checks a run a driver
// made. In Go, ReadFile and Log.Steps give the steps qtest.Validate
// takes. Package qdriver/bombadil builds a Log from a Bombadil trace;
// adapters/playwright in the repository writes one from Playwright's
// request and response events.
//
// The steps keep the driver's times, its calls and its order: they are
// written as they are (Seq, Call, Time, Ref), never recorded again
// through a qobs.Recorder, which would stamp them with the time of the
// conversion and put the whole run after every server step.
package qdriver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Protocol is the version of the request log this package reads and
// writes: a header's protocol field.
const Protocol = "gotogether.requests/1"

// protocolFamily prefixes every version's name: a header with another
// version is a request log this package cannot read, not some other file.
const protocolFamily = "gotogether.requests/"

// The kinds of record (Record.Event).
const (
	Request  = "request"  // the driver sent a request: an invocation
	Response = "response" // its own answer: the call's completion
	Observed = "step"     // something seen that answers no request: a step of its own
)

// Header is a log's first line: the protocol's version and the names its
// steps are recorded under.
type Header struct {
	Protocol string `json:"protocol"`
	Recorder string `json:"recorder"`
	Actor    string `json:"actor,omitempty"`
	Process  string `json:"process,omitempty"`
	Thread   string `json:"thread,omitempty"`
	// Driver is free text: the tool and its version
	Driver string `json:"driver,omitempty"`
}

// Record is one line after the header: a request, a response or a step
// (Event). Which fields each kind takes is the package's doc's.
type Record struct {
	Event  string    `json:"event"`
	Req    string    `json:"req,omitempty"`
	Action string    `json:"action,omitempty"`
	Time   time.Time `json:"time"`
	// Code is a response's status, required on one (0: no answer came)
	Code    *int64           `json:"code,omitempty"`
	Error   string           `json:"error,omitempty"`
	Outcome string           `json:"outcome,omitempty"`
	Args    map[string]any   `json:"args,omitempty"`
	Obs     map[string]any   `json:"obs,omitempty"`
	Corr    string           `json:"corr,omitempty"`
	Thread  string           `json:"thread,omitempty"`
	Order   map[string]int64 `json:"order,omitempty"`
	Ref     string           `json:"ref,omitempty"`
}

// CodeArg is the completion's argument that carries a response's code.
const CodeArg = "code"

// Log is a request log: its header and records, checked against the
// protocol (New, Read).
type Log struct {
	Header  Header
	Records []Record
	// Torn is a log whose last line was cut off: Records are those
	// before it, and Steps ends with a loss record after them.
	Torn *qtrace.TornTail
	// lines are the records' line numbers in the file read (Read), for
	// the steps' Source; nil for a log built with New
	lines []int
}

// New checks records against the protocol and returns the log. An error
// names the record (its index from 0) and what to fix.
func New(h Header, records []Record) (*Log, error) {
	l := &Log{Header: h, Records: records}
	if err := l.check(func(i int) string { return fmt.Sprintf("record %d", i) }); err != nil {
		return nil, err
	}
	return l, nil
}

// ReadFile reads the log at path (Read).
func ReadFile(path string) (*Log, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f)
}

// Read reads a request log. A torn last line returns the log of the
// records before it with a *qtrace.TornTail error, which a caller may
// report and go on with (Log.Torn is set too); any other error returns
// no log.
func Read(r io.Reader) (*Log, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	l := &Log{}
	lines := bytes.Split(data, []byte("\n"))
	header := false
	for i, raw := range lines {
		n := i + 1
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var err error
		if !header {
			err = decode(raw, &l.Header)
		} else {
			var rec Record
			if err = decode(raw, &rec); err == nil {
				l.Records, l.lines = append(l.Records, rec), append(l.lines, n)
			}
		}
		if err != nil {
			if i == len(lines)-1 && !errors.Is(err, errTrailing) && !json.Valid(raw) && header {
				l.Torn = &qtrace.TornTail{Line: n, Text: string(raw)}
				break
			}
			what := "a record"
			if !header {
				what = "the header"
			}
			return nil, fmt.Errorf("line %d: not %s of a request log (%s): %v", n, what, Protocol, err)
		}
		header = true
	}
	if !header {
		return nil, fmt.Errorf("no header: a request log's first line is {\"protocol\": %q, \"recorder\": ...}", Protocol)
	}
	if err := l.check(func(i int) string {
		if i < 0 {
			return "line 1"
		}
		return fmt.Sprintf("line %d", l.lines[i])
	}); err != nil {
		return nil, err
	}
	if l.Torn != nil {
		return l, l.Torn
	}
	return l, nil
}

// errTrailing is a line's error when its object is followed by more.
var errTrailing = errors.New("trailing data") // read-only

// decode reads one line's object into v, refusing a field v does not
// have and anything after the object.
func decode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%w after the object: write one object per line", errTrailing)
	}
	return nil
}

// Sniff reports whether data begins with a request log's header (of any
// version: Read refuses one it cannot read, with the fix).
func Sniff(data []byte) bool {
	var first struct {
		Protocol string `json:"protocol"`
	}
	if json.NewDecoder(bytes.NewReader(data)).Decode(&first) != nil {
		return false
	}
	return strings.HasPrefix(first.Protocol, protocolFamily)
}

// check holds l to the protocol; at names record i (-1: the header).
func (l *Log) check(at func(int) string) error {
	h := l.Header
	switch {
	case h.Protocol == Protocol:
	case strings.HasPrefix(h.Protocol, protocolFamily):
		return fmt.Errorf("%s: protocol %s is a later version than this gotogether reads (%s): upgrade gotogether, or have the driver write %s", at(-1), h.Protocol, Protocol, Protocol)
	default:
		return fmt.Errorf("%s: protocol %q is not %q: a request log's header names its protocol", at(-1), h.Protocol, Protocol)
	}
	if h.Recorder == "" {
		return fmt.Errorf("%s: the header names no recorder (\"recorder\": \"ui\"): the log's records are numbered in it, and two logs joined must not share one", at(-1))
	}
	reqs := map[string]int{} // req -> its record
	answered := map[string]bool{}
	var last time.Time
	for i := range l.Records {
		r := &l.Records[i]
		bad := func(format string, a ...any) error {
			return fmt.Errorf("%s: %s", at(i), fmt.Sprintf(format, a...))
		}
		if r.Time.IsZero() {
			return bad("a %s with no time: give each record the driver's time of it (RFC 3339)", orRecord(r.Event))
		}
		if r.Time.Before(last) {
			return bad("time %s is before the record before it (%s): records are in the order the driver saw them, on one clock", r.Time.Format(time.RFC3339Nano), last.Format(time.RFC3339Nano))
		}
		last = r.Time
		switch r.Event {
		case Request:
			switch {
			case r.Req == "":
				return bad("a request with no req: name it (\"req\": \"1\") so its response can name it")
			case r.Action == "":
				return bad("request %s names no action: the model's action it invokes", r.Req)
			case reqs[r.Req] != 0:
				return bad("request %s was sent before (%s): each request has its own req", r.Req, at(reqs[r.Req]-1))
			case r.Code != nil || r.Error != "" || r.Outcome != "" || r.Obs != nil:
				return bad("request %s carries an answer (code, error, outcome or obs): its answer is a response of its own", r.Req)
			}
			reqs[r.Req] = i + 1
		case Response:
			switch {
			case r.Req == "":
				return bad("a response with no req: name the request it answers")
			case reqs[r.Req] == 0:
				return bad("a response to request %s, which no request before it sent", r.Req)
			case answered[r.Req]:
				return bad("a second response to request %s: a request has one answer", r.Req)
			case r.Code == nil:
				return bad("the response to request %s has no code: give the answer's status, 0 if none came", r.Req)
			case r.Action != "" && r.Action != l.Records[reqs[r.Req]-1].Action:
				return bad("the response to request %s names action %s, its request %s: leave it out", r.Req, r.Action, l.Records[reqs[r.Req]-1].Action)
			case r.Thread != "" && r.Thread != l.Records[reqs[r.Req]-1].Thread:
				return bad("the response to request %s names thread %s: a response is in its request's thread; leave it out", r.Req, r.Thread)
			case r.Corr != "":
				return bad("the response to request %s has a corr: a request's corr is the request's", r.Req)
			}
			if _, ok := r.Args[CodeArg]; ok {
				return bad("the response to request %s has an argument %q: the code is the response's own field", r.Req, CodeArg)
			}
			answered[r.Req] = true
		case Observed:
			switch {
			case r.Action == "":
				return bad("a step names no action")
			case r.Req != "" || r.Code != nil || r.Corr != "":
				return bad("step %s names a request (req, code or corr): a step answers none; a request's answer is a response", r.Action)
			}
		default:
			return bad("event %q is none of the protocol's (%s, %s, %s)", r.Event, Request, Response, Observed)
		}
	}
	return nil
}

func orRecord(event string) string {
	if event == "" {
		return "record"
	}
	return event
}

// Steps turns the log into steps and records, numbered from 1 in the
// log's order: each request an invocation record (qtrace.Step.Invocation),
// its response the completion (Call its invocation's Seq), each step a
// step. A torn log ends with a loss record after its last seq
// (qtrace.MarkLost).
func (l *Log) Steps() []qtrace.Step {
	h := l.Header
	actor, process, thread := h.Actor, h.Process, h.Thread
	if actor == "" {
		actor = "default"
	}
	if process == "" {
		process = h.Recorder
	}
	if thread == "" {
		thread = h.Recorder
	}
	type sent struct {
		seq uint64
		rec *Record
	}
	calls := map[string]sent{}
	out := make([]qtrace.Step, 0, len(l.Records)+1)
	for i := range l.Records {
		r := &l.Records[i]
		s := qtrace.Step{Action: r.Action, Seq: uint64(len(out) + 1), Recorder: h.Recorder, Actor: actor, Process: process,
			Thread: thread, Time: r.Time, Ref: r.Ref, Order: maps.Clone(r.Order), Obs: values(r.Obs)}
		if r.Thread != "" {
			s.Thread = r.Thread
		}
		if l.lines != nil {
			s.Source = fmt.Sprintf("line %d", l.lines[i])
		}
		switch r.Event {
		case Request:
			s.Invocation = true
			s.Args = values(r.Args)
			s.Ref = withCorr(r.Ref, r.Corr)
			calls[r.Req] = sent{s.Seq, r}
		case Response:
			req := calls[r.Req]
			s.Action, s.Call, s.Thread = req.rec.Action, req.seq, thread
			if req.rec.Thread != "" {
				s.Thread = req.rec.Thread
			}
			s.Outcome, s.Error = r.Outcome, r.Error
			if s.Outcome == "" {
				s.Outcome = "ok"
			}
			s.Args = values(r.Args)
			maps.Copy(s.Args, values(req.rec.Args)) // what the request sent, whatever the answer says
			s.Args[CodeArg] = *r.Code
			if s.Ref == "" {
				s.Ref = req.rec.Ref
			}
			s.Ref = withCorr(s.Ref, req.rec.Corr)
		case Observed:
			s.Outcome = "ok"
			s.Args = values(r.Args)
		}
		out = append(out, s)
	}
	if l.Torn != nil {
		out = qtrace.MarkLost(out, qtrace.Lost{Recorder: h.Recorder, After: uint64(len(out))})
	}
	return out
}

// withCorr adds a request's corr to a ref.
func withCorr(ref, corr string) string {
	switch {
	case corr == "":
		return ref
	case ref == "":
		return "corr=" + corr
	}
	return ref + " corr=" + corr
}

// Write writes the log in the protocol: its header, then a line per
// record. A torn log is written whole, without its torn line.
func (l *Log) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(l.Header); err != nil {
		return err
	}
	for i := range l.Records {
		if err := enc.Encode(&l.Records[i]); err != nil {
			return err
		}
	}
	return nil
}

// values copies a record's arguments or observations as a step holds
// them: a JSON number an int64 if it is whole, else a float64, and a Go
// int an int64 (a record built with New), at any depth.
func values(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = value(v)
	}
	return out
}

func value(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case map[string]any:
		return values(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = value(e)
		}
		return out
	}
	return v
}
