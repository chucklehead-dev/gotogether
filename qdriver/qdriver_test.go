package qdriver

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
// completion (Call its seq) with what the request sent, then the
// answer's fields it did not send, then the code; a step completes
// nothing; refs carry the request's corr; a request with no answer is
// pending.
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
		{"submit", false, 1, 40, "map[code:202 id:j1 key:k1 status:accepted]", "shot-40.png corr=t-1", "page"},
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
