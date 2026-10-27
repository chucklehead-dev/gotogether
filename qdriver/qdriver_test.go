package qdriver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

const header = `{"protocol":"gotogether.requests/1","recorder":"ui","actor":"webjobs","process":"webjobs","thread":"page"}` + "\n"

func at(ms int) string {
	return time.UnixMilli(1793042049000 + int64(ms)).UTC().Format(time.RFC3339Nano)
}

func read(t *testing.T, lines ...string) (*Log, error) {
	t.Helper()
	return Read(strings.NewReader(header + strings.Join(lines, "\n") + "\n"))
}

// A request is an invocation record at its own time, its response the
// completion (Call its seq) with what the request sent, the answer's
// fields it did not send, the answer's value of each field the request
// sent too as answer_<field> (an answer naming another key: the server
// applied another value, which the model must be able to see, and a
// warning names it; the first version dropped it), then the code; a
// step completes nothing; refs carry the request's corr; a request with
// no answer is pending.
func TestSteps(t *testing.T) {
	l, err := read(t,
		`{"event":"request","req":"1","action":"submit","time":"`+at(0)+`","args":{"key":"k1"},"corr":"t-1","ref":"shot-0.png"}`,
		`{"event":"request","req":"2","action":"submit","time":"`+at(1)+`","args":{"key":"k1"},"corr":"t-2"}`,
		`{"event":"response","req":"1","time":"`+at(40)+`","code":202,"args":{"id":"j1","status":"accepted","key":"other"},"ref":"shot-40.png"}`,
		`{"event":"step","action":"show","time":"`+at(41)+`","args":{"id":"j1","status":"accepted"},"obs":{"n":1.5}}`,
		`{"event":"request","req":"c","action":"cancel","time":"`+at(50)+`","args":{"id":"j1"},"thread":"tab2"}`,
		`{"event":"response","req":"c","time":"`+at(60)+`","code":0,"error":"net::ERR_ABORTED"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	steps := l.Steps()
	type want struct {
		action string
		inv    bool
		call   uint64
		ms     int
		args   string
		ref    string
		thread string
	}
	wants := []want{
		{"submit", true, 0, 0, "map[key:k1]", "shot-0.png corr=t-1", "page"},
		{"submit", true, 0, 1, "map[key:k1]", "corr=t-2", "page"},
		{"submit", false, 1, 40, "map[answer_key:other code:202 id:j1 key:k1 status:accepted]", "shot-40.png corr=t-1", "page"},
		{"show", false, 0, 41, "map[id:j1 status:accepted]", "", "page"},
		{"cancel", true, 0, 50, "map[id:j1]", "", "tab2"},
		{"cancel", false, 5, 60, "map[code:0 id:j1]", "", "tab2"},
	}
	if len(steps) != len(wants) {
		t.Fatalf("%d records, want %d: %+v", len(steps), len(wants), steps)
	}
	for i, w := range wants {
		s := steps[i]
		got := want{s.Action, s.Invocation, s.Call, int(s.Time.UnixMilli() - 1793042049000), fmt.Sprint(s.Args), s.Ref, s.Thread}
		if got != w || s.Seq != uint64(i+1) || s.Recorder != "ui" || s.Actor != "webjobs" || s.Process != "webjobs" || s.Source != fmt.Sprintf("line %d", i+2) {
			t.Errorf("record %d: %+v (seq %d, %s/%s/%s, %s), want %+v", i+1, got, s.Seq, s.Recorder, s.Actor, s.Process, s.Source, w)
		}
		if !s.Invocation && s.Outcome != "ok" {
			t.Errorf("record %d: outcome %q, want ok (the model judges the code)", i+1, s.Outcome)
		}
	}
	if steps[2].Args["code"] != int64(202) || steps[3].Obs["n"] != 1.5 || steps[5].Error != "net::ERR_ABORTED" {
		t.Errorf("values: code %T %v, obs %v, error %q", steps[2].Args["code"], steps[2].Args["code"], steps[3].Obs, steps[5].Error)
	}
	ws := l.Warnings()
	if len(ws) != 1 || !strings.Contains(ws[0], `line 4: the response to request 1 (submit) answers key="other", the request sent "k1": the completion's key is what was sent, the answer's is answer_key`) {
		t.Errorf("warnings %q, want the answer's other key named", ws)
	}
	traces, issues := qtrace.Reconstruct(steps)
	if len(issues) > 0 || len(traces) != 1 {
		t.Fatalf("reconstructed: %v %+v", issues, traces)
	}
	tr := traces[0]
	if len(tr.Steps) != 3 || len(tr.Pending) != 1 || tr.Pending[0].Seq != 2 || len(tr.Issues) != 0 {
		t.Fatalf("steps %+v pending %+v issues %v: want the double click's second request pending", tr.Steps, tr.Pending, tr.Issues)
	}
	if got := tr.Steps[0].Invoked; !got.Equal(time.UnixMilli(1793042049000)) {
		t.Errorf("submit's completion invoked at %v, want its request's time", got)
	}
}

// The header's defaults are a recorder's: actor "default", process and
// thread the recorder.
func TestHeaderDefaults(t *testing.T) {
	l, err := Read(strings.NewReader(`{"protocol":"gotogether.requests/1","recorder":"pw"}` + "\n" +
		`{"event":"step","action":"show","time":"` + at(0) + `"}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := l.Steps()[0]
	if s.Actor != "default" || s.Process != "pw" || s.Thread != "pw" {
		t.Errorf("%+v", s)
	}
}

// What the protocol does not allow is refused with the line and the fix.
func TestRefused(t *testing.T) {
	req := func(id string, ms int) string {
		return `{"event":"request","req":"` + id + `","action":"submit","time":"` + at(ms) + `","args":{"key":"k"}}`
	}
	for _, c := range []struct {
		name   string
		header string
		lines  []string
		want   string
	}{
		{"a later version", `{"protocol":"gotogether.requests/2","recorder":"ui"}`, nil, "line 1: protocol gotogether.requests/2 is a later version than this gotogether reads (gotogether.requests/1): upgrade gotogether"},
		{"no recorder", `{"protocol":"gotogether.requests/1"}`, nil, "line 1: the header names no recorder"},
		{"no header", `{"event":"request","req":"1","action":"a","time":"` + at(0) + `"}`, nil, "line 1: not the header of a request log"},
		{"a misspelt field", "", []string{`{"event":"request","req":"1","action":"a","time":"` + at(0) + `","arg":{"k":1}}`}, `line 2: not a record of a request log (gotogether.requests/1): json: unknown field "arg"`},
		{"a response to no request", "", []string{`{"event":"response","req":"9","time":"` + at(0) + `","code":200}`}, "line 2: a response to request 9, which no request before it sent"},
		{"a second response", "", []string{req("1", 0), `{"event":"response","req":"1","time":"` + at(1) + `","code":200}`, `{"event":"response","req":"1","time":"` + at(2) + `","code":200}`}, "line 4: a second response to request 1"},
		{"a response with no code", "", []string{req("1", 0), `{"event":"response","req":"1","time":"` + at(1) + `"}`}, "line 3: the response to request 1 has no code: give the answer's status, 0 if none came"},
		{"a request sent twice", "", []string{req("1", 0), req("1", 1)}, "line 3: request 1 was sent before (line 2)"},
		{"a request with its answer", "", []string{`{"event":"request","req":"1","action":"a","time":"` + at(0) + `","code":200}`}, "line 2: request 1 carries an answer"},
		{"a step answering a request", "", []string{req("1", 0), `{"event":"step","action":"show","req":"1","time":"` + at(1) + `"}`}, "line 3: step show names a request"},
		{"time going back", "", []string{req("1", 5), req("2", 1)}, "line 3: time"},
		{"no time", "", []string{`{"event":"step","action":"show"}`}, "line 2: a step with no time"},
		{"an unknown event", "", []string{`{"event":"click","action":"a","time":"` + at(0) + `"}`}, `line 2: event "click" is none of the protocol's (request, response, step)`},
		{"the code as an argument", "", []string{req("1", 0), `{"event":"response","req":"1","time":"` + at(1) + `","code":200,"args":{"code":1}}`}, `line 3: the response to request 1 has an argument "code"`},
		{"a request's argument named code", "", []string{`{"event":"request","req":"1","action":"a","time":"` + at(0) + `","args":{"code":"SAVE10"}}`}, `line 2: request 1 has an argument "code", the name of the completion's code (CodeArg), which would replace it`},
		{"an answer's field under the answer's name", "", []string{req("1", 0), `{"event":"response","req":"1","time":"` + at(1) + `","code":200,"args":{"key":"x","answer_key":"y"}}`}, "line 3: the response to request 1 answers both key, which the request sent, and answer_key"},
		{"a request's field under the answer's name", "", []string{`{"event":"request","req":"1","action":"a","time":"` + at(0) + `","args":{"key":"k","answer_key":"y"}}`, `{"event":"response","req":"1","time":"` + at(1) + `","code":200,"args":{"key":"x"}}`}, "line 3: request 1 sent both key and answer_key"},
		{"two objects on a line", "", []string{req("1", 0) + req("2", 1), req("3", 2)}, "line 2: not a record of a request log (gotogether.requests/1): trailing data"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := c.header
			if h == "" {
				h = strings.TrimSpace(header)
			}
			l, err := Read(strings.NewReader(h + "\n" + strings.Join(c.lines, "\n") + "\n"))
			if err == nil || !strings.Contains(err.Error(), c.want) || l != nil {
				t.Fatalf("got %v (log %v), want an error with %q and no log", err, l != nil, c.want)
			}
		})
	}
	if _, err := New(Header{Protocol: Protocol, Recorder: "ui"}, []Record{{Event: Response, Req: "1", Time: time.Now()}}); err == nil || !strings.HasPrefix(err.Error(), "record 0: a response to request 1") {
		t.Errorf("New: %v", err)
	}
}

// A last line cut off is a lost tail: the records before it, a loss
// record after the last seq, and the *qtrace.TornTail.
func TestTornTail(t *testing.T) {
	last := `{"event":"response","req":"1","time":"` + at(9) + `","code":202}`
	l, err := Read(strings.NewReader(header + `{"event":"request","req":"1","action":"submit","time":"` + at(0) + `"}` + "\n" + last[:20]))
	var torn *qtrace.TornTail
	if !errors.As(err, &torn) || torn.Line != 3 || l == nil || l.Torn != torn {
		t.Fatalf("err %v, log %+v: want a torn tail at line 3", err, l)
	}
	steps := l.Steps()
	if len(steps) != 2 || steps[1].Lost != "suffix" || steps[1].LostAfter != 1 || steps[1].Recorder != "ui" {
		t.Fatalf("steps %+v, want the request and a loss after seq 1", steps)
	}
	// a whole line, however wrong, is no torn tail
	if _, err := Read(strings.NewReader(header + `{"event":"step","action":"a","time":"` + at(0) + `","oops":1}`)); err == nil || errors.As(err, &torn) {
		t.Errorf("a whole last line with an unknown field: %v", err)
	}
}

// A log written is read back as the same steps; Sniff knows it by its
// header, of any version, and nothing else.
func TestWriteSniff(t *testing.T) {
	code := int64(200)
	l, err := New(Header{Protocol: Protocol, Recorder: "ui", Driver: "go"}, []Record{
		{Event: Request, Req: "1", Action: "get", Time: time.UnixMilli(1000), Args: map[string]any{"n": 3, "q": "a<b"}, Corr: "x"},
		{Event: Response, Req: "1", Time: time.UnixMilli(1001), Code: &code, Args: map[string]any{"v": 2.5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := l.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if !Sniff(buf.Bytes()) || !strings.Contains(buf.String(), `"a<b"`) {
		t.Fatalf("not sniffed, or escaped: %s", buf.String())
	}
	back, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	a, b := l.Steps(), back.Steps()
	for i := range a {
		a[i].Source, b[i].Source = "", ""
		if fmt.Sprintf("%+v", a[i]) != fmt.Sprintf("%+v", b[i]) || !a[i].Time.Equal(b[i].Time) {
			t.Errorf("step %d: %+v, read back %+v", i, a[i], b[i])
		}
	}
	for _, s := range []string{`{"protocol":"gotogether.requests/9"}`, `{"protocol":"gotogether.requests/1","recorder":"r"}`} {
		if !Sniff([]byte(s)) {
			t.Errorf("%s not sniffed", s)
		}
	}
	for _, s := range []string{`{"quint.action":"a"}`, `{"protocol":"other/1"}`, `not json`} {
		if Sniff([]byte(s)) {
			t.Errorf("%s sniffed", s)
		}
	}
}

// A record's arguments and observations are a step's scalars, as qobs
// records them: an object or an array its JSON text (numbers as
// written), an integer past int64 a json.Number, whole (a float64 would
// round it). They were Go maps and slices, which no template function
// reads; toQuint, a structured parameter's default, now decodes them by
// the model's type.
func TestNestedValues(t *testing.T) {
	big := "123456789012345678901234567890"
	l, err := read(t,
		`{"event":"request","req":"1","action":"commit","time":"`+at(0)+`","args":{"rs":[{"typ":"counter","tags":[["name","x"]]}],"pair":[7,true],"tally":{"a":[1,2]},"nested":{"a":{"b":[[1,"one"]]}},"n":`+big+`}}`,
		`{"event":"response","req":"1","time":"`+at(1)+`","code":200,"args":{"batch":{"events":[{"tag":"Created","counter":"c","name":"n"},"Reset"],"at":1,"cause":null}},"obs":{"log":["Reset"]}}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	steps := l.Steps()
	inv, done := steps[0], steps[1]
	for k, want := range map[string]any{"rs": `[{"tags":[["name","x"]],"typ":"counter"}]`, "pair": `[7,true]`, "nested": `{"a":{"b":[[1,"one"]]}}`, "n": json.Number(big)} {
		if inv.Args[k] != want || done.Args[k] != want {
			t.Errorf("%s: %T %v and %T %v, want %T %v", k, inv.Args[k], inv.Args[k], done.Args[k], done.Args[k], want, want)
		}
	}
	if done.Obs["log"] != `["Reset"]` {
		t.Errorf("obs %v", done.Obs)
	}
	skip.Quint(t)
	m, err := binding.LoadModel("quint", "../binding/testdata/typed.qnt", "typed")
	if err != nil {
		t.Fatal(err)
	}
	b := &binding.Binding{Spec: "typed.qnt", Module: "typed", Actions: map[string]binding.Action{"commit": {}}}
	tr := &qtrace.Trace{Actor: "webjobs", Processes: []string{"webjobs"}, Steps: []qtrace.Step{done}}
	plan, err := b.Plan(m, tr)
	if err != nil {
		t.Fatal(err)
	}
	created := itf.VariantOf("Created", itf.RecordOf("counter", itf.StrOf("c"), "name", itf.StrOf("n")))
	for name, want := range map[string]string{
		"batch":  itf.RecordOf("events", itf.ListOf(created, itf.Enum("Reset")), "at", itf.IntOf(1), "cause", itf.Enum("None")).String(),
		"rs":     itf.SetOf(itf.RecordOf("typ", itf.StrOf("counter"), "tags", itf.SetOf(itf.TupleOf(itf.StrOf("name"), itf.StrOf("x"))))).String(),
		"pair":   itf.TupleOf(itf.IntOf(7), itf.BoolOf(true)).String(),
		"tally":  `Map("a" -> [1, 2])`,
		"nested": itf.RecordOf("a", itf.RecordOf("b", itf.ListOf(itf.TupleOf(itf.IntOf(1), itf.StrOf("one"))))).String(),
		"n":      big,
	} {
		if got := plan.Calls[0].Args[name]; got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
}
