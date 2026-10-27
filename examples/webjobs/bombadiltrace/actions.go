package bombadiltrace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/webjobs/journey"
)

// Actions turns a trace into the actions the page took, for a Go client
// to play again (journey.Client.Play): so a failure Bombadil found can be
// shrunk by removing actions and rerunning them, without a browser.
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
// refused as Read refuses it.
func Actions(r io.Reader, opt Options) ([]journey.Action, error) {
	opt.defaults()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []journey.Action
	keyOf := map[string]string{} // job id -> the key a submission's answer named it for
	last, current := 0, ""       // the last request seen sent; the key of the job last submitted
	lastBefore := 0              // the last request seen sent before this entry
	var clicked time.Time        // the state before the last click
	c := &converter{opt: opt}
	for i, raw := range bytes.Split(data, []byte("\n")) {
		n := i + 1
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("%s:%d: not a Bombadil trace entry: %v", opt.Name, n, err)
		}
		if e.Timestamp == nil {
			return nil, fmt.Errorf("%s:%d: an entry with no timestamp", opt.Name, n)
		}
		click, err := c.click(e.Action)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", opt.Name, n, err)
		}
		cur, err := c.result(&e)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", opt.Name, n, err)
		}
		if cur == nil {
			if click != "" {
				return nil, fmt.Errorf("%s:%d: %s with no %s snapshot after it", opt.Name, n, click, opt.Snapshot)
			}
			continue
		}
		// the requests this entry's click sent, in the order sent: each
		// key's submissions one Submit (Twice for two), each cancel one
		// Cancel
		at := c.before(&e)
		slept := false
		for k, r := range cur.Log {
			if r.Code != nil && r.Op == "submit" && r.Job != nil {
				keyOf[r.Job.ID] = r.Key
			}
			if r.N <= last {
				continue
			}
			last = r.N
			switch r.Op {
			case "submit":
				if k > 0 && cur.Log[k-1].N > lastBefore && cur.Log[k-1].Op == "submit" && cur.Log[k-1].Key == r.Key {
					if sub, ok := out[len(out)-1].(journey.Submit); ok && sub.Key == r.Key {
						sub.Twice = true
						out[len(out)-1] = sub
						continue
					}
				}
				if current != "" && current != r.Key {
					out = append(out, journey.AwaitEnd{Key: current, Every: pageEvery})
				}
				current = r.Key
				out = append(out, journey.Submit{Key: r.Key})
				clicked = at
			case "cancel":
				key, ok := keyOf[r.ID]
				if !ok {
					return nil, fmt.Errorf("%s:%d: a cancel of job %s, which no submission's answer in the log named", opt.Name, n, r.ID)
				}
				if !slept && !clicked.IsZero() && at.After(clicked) {
					out = append(out, journey.Sleep{D: at.Sub(clicked)})
				}
				slept = true
				out = append(out, journey.Cancel{Key: key})
				clicked = at
			}
		}
		lastBefore = last
		c.prevTime = time.UnixMicro(*e.Timestamp)
	}
	if current != "" {
		out = append(out, journey.AwaitEnd{Key: current, Every: pageEvery})
	}
	return out, nil
}

// pageEvery is how often the page polls its job (index.html): an await
// polls as the page did.
const pageEvery = 100 * time.Millisecond

// before is the time of the state before e's click: the click came after.
func (c *converter) before(e *entry) time.Time {
	if c.prevTime.IsZero() {
		return time.UnixMicro(*e.Timestamp)
	}
	return c.prevTime
}
