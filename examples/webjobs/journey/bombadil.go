package journey

import (
	"fmt"
	"io"
	"time"

	"github.com/chucklehead-dev/gotogether/qdriver"
	"github.com/chucklehead-dev/gotogether/qdriver/bombadil"
)

// Bombadil is webjobs' mapping of a Bombadil trace (package
// qdriver/bombadil): the spec's custom actions ui.submit and ui.cancel
// (bombadil/spec.ts), its result extract, the page's log entries ({n,
// op, key or id, code, job}: index.html), the job shown a show step, and
// the steps on recorder ui of actor and process webjobs (one with the
// server's), in thread page.
var Bombadil = bombadil.Options{ // read-only
	Header:   qdriver.Header{Recorder: "ui", Actor: "webjobs", Process: "webjobs", Thread: "page"},
	Snapshot: "result",
	Fields:   bombadil.Fields{Action: "op", Answer: "job", AnswerFields: []string{"id", "status"}},
	Clicks:   map[string][]string{"ui.submit": {"submit"}, "ui.cancel": {"cancel"}},
	Show:     &bombadil.Show{Key: "id", Fields: []string{"id", "status"}},
}

// FromTrace turns a Bombadil trace into the actions the page took, for
// Client.Play: so a failure Bombadil found can be shrunk by removing
// actions and rerunning them, without a browser.
//
//   - a click's requests (the page's log names what each sent), in the
//     order sent: a key's submissions one Submit of the key (Twice if
//     the click sent two of it), each cancel a Cancel of the key whose
//     job it names (from the submissions' answers the log showed);
//   - the page polls a running job by itself, every 100ms: before a
//     submission of another key (which the page offers only once its job
//     ended) the job before it is awaited (AwaitEnd at the page's
//     interval), before a cancel the time since the click before passes
//     (Sleep), and the trace's last job is awaited at its end.
//
// Bombadil's own actions (Wait, Click, ...) are no actions of the page's:
// their time is in the Sleeps. A trace the mapping cannot read is
// refused as bombadil.States refuses it, a torn one included.
func FromTrace(r io.Reader) ([]Action, error) {
	states, err := bombadil.States(r, Bombadil)
	if err != nil {
		return nil, err
	}
	var out []Action
	keyOf := map[string]string{} // job id -> the key a submission's answer named it for
	last, current := 0, ""       // the last request seen sent; the key of the job last submitted
	lastBefore := 0              // the last request seen sent before this state
	var clicked, prev time.Time  // the state before the last click; the last state with a snapshot
	for _, s := range states {
		if !s.Snapshot { // nothing the page did, and no state a click came after
			continue
		}
		// the requests this state's click sent, in the order sent: each
		// key's submissions one Submit (Twice for two), each cancel one
		// Cancel
		at := prev
		if at.IsZero() {
			at = s.Time
		}
		slept := false
		for k, r := range s.Log {
			key, _ := r.Args["key"].(string)
			if r.Code != nil && r.Action == "submit" && r.Answer != nil {
				keyOf[fmt.Sprint(r.Answer["id"])] = key
			}
			if r.N <= last {
				continue
			}
			last = r.N
			switch r.Action {
			case "submit":
				if p := s.Log[max(k-1, 0)]; k > 0 && p.N > lastBefore && p.Action == "submit" && p.Args["key"] == key {
					if sub, ok := out[len(out)-1].(Submit); ok && sub.Key == key {
						sub.Twice = true
						out[len(out)-1] = sub
						continue
					}
				}
				if current != "" && current != key {
					out = append(out, AwaitEnd{Key: current, Every: pageEvery})
				}
				current = key
				out = append(out, Submit{Key: key})
				clicked = at
			case "cancel":
				id, _ := r.Args["id"].(string)
				key, ok := keyOf[id]
				if !ok {
					return nil, fmt.Errorf("%s: a cancel of job %s, which no submission's answer in the log named", s.Ref, id)
				}
				if !slept && !clicked.IsZero() && at.After(clicked) {
					out = append(out, Sleep{D: at.Sub(clicked)})
				}
				slept = true
				out = append(out, Cancel{Key: key})
				clicked = at
			}
		}
		lastBefore = last
		prev = s.Time
	}
	if current != "" {
		out = append(out, AwaitEnd{Key: current, Every: pageEvery})
	}
	return out, nil
}

// pageEvery is how often the page polls its job (index.html): an await
// polls as the page did.
const pageEvery = 100 * time.Millisecond
