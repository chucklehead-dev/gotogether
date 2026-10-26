// Package journey drives webjobs' API as its page does, without a
// browser, from a list of actions: what the per-push tier runs against
// the real server (and each mutant's), what a shrinker removes actions
// from and reruns, and what a Bombadil trace becomes
// (bombadiltrace.Actions). It records what bombadiltrace records of a
// Bombadil trace, on recorder "ui": each request an invocation
// (qobs.Invoke) and its own answer its completion (Call.Done: code, id,
// status), each change of the job shown a show step. Recorded live, with
// the client's own clock: the times are the requests', not a
// conversion's.
package journey

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
)

// An Action is one thing the page does. A cancel, a poll or an await of
// a key with no job (its submission removed, or never answered) does
// nothing: a list stays playable whatever is taken out of it.
type Action interface {
	fmt.Stringer
	isAction()
}

// Submit sends key's submission, twice at once with Twice (the page's
// double click); the first answer that names a job is shown.
type Submit struct {
	Key   string
	Twice bool
}

// Cancel cancels key's job; its answer, 200 or 409, is shown.
type Cancel struct{ Key string }

// Poll asks for key's job once; a change is shown.
type Poll struct{ Key string }

// AwaitEnd polls key's job every 20ms until it is shown ended, or for
// Client.Stuck: a job that never ends stops the play there, as the page
// would show it running for good.
type AwaitEnd struct{ Key string }

// Resubmit sends key's submission once more, late (a page that lost the
// first answer would): its answer is not shown.
type Resubmit struct{ Key string }

// Sleep waits D.
type Sleep struct{ D time.Duration }

func (Submit) isAction()   {}
func (Cancel) isAction()   {}
func (Poll) isAction()     {}
func (AwaitEnd) isAction() {}
func (Resubmit) isAction() {}
func (Sleep) isAction()    {}

func (a Submit) String() string {
	if a.Twice {
		return "submit " + a.Key + " twice"
	}
	return "submit " + a.Key
}
func (a Cancel) String() string   { return "cancel " + a.Key }
func (a Poll) String() string     { return "poll " + a.Key }
func (a AwaitEnd) String() string { return "await " + a.Key }
func (a Resubmit) String() string { return "resubmit " + a.Key }
func (a Sleep) String() string    { return "sleep " + a.D.String() }

// Client plays actions against the server at Base.
type Client struct {
	Base     string         // the server's URL
	Recorder *qobs.Recorder // recorder "ui"
	Journeys int            // Plan's journeys (default 10)
	// CancelWithin bounds when a journey that cancels does, after its
	// job was shown (default 500ms)
	CancelWithin time.Duration
	// Alternate: every second journey cancels (the first runs to ready),
	// instead of a coin's choice: a short run has both kinds
	Alternate bool
	// Stuck is how long an AwaitEnd waits for its job to end (default 3s)
	Stuck time.Duration
}

func (c *Client) defaults() {
	if c.Journeys == 0 {
		c.Journeys = 10
	}
	if c.Stuck == 0 {
		c.Stuck = 3 * time.Second
	}
	if c.CancelWithin == 0 {
		c.CancelWithin = 500 * time.Millisecond
	}
}

// Plan is seed's journeys as actions: each a double-clicked submission,
// in some (every second with Alternate, else by a coin) a cancel at a
// random moment, an await of the job's end, and every fourth its
// submission sent once more after its end. Keys are k<seed>-<n>.
func (c *Client) Plan(seed int64) []Action {
	c.defaults()
	rng := rand.New(rand.NewSource(seed))
	var out []Action
	for i := range c.Journeys {
		key := fmt.Sprintf("k%d-%d", seed, i+1)
		out = append(out, Submit{Key: key, Twice: true})
		if c.Alternate && i%2 == 1 || !c.Alternate && rng.Intn(2) == 0 {
			out = append(out, Sleep{time.Duration(rng.Int63n(int64(c.CancelWithin)))}, Cancel{Key: key})
		}
		out = append(out, AwaitEnd{Key: key})
		if i%4 == 3 {
			out = append(out, Resubmit{Key: key})
		}
	}
	return out
}

// Run plays seed's plan.
func (c *Client) Run(ctx context.Context, seed int64) error { return c.Play(ctx, c.Plan(seed)) }

// Play plays actions in order. It returns early, with no error, at an
// AwaitEnd whose job never ended.
func (c *Client) Play(ctx context.Context, actions []Action) error {
	c.defaults()
	ctx = qobs.WithRecorder(ctx, c.Recorder)
	p := &page{rec: ctx}
	ids := map[string]string{} // key -> its job, once an answer named one
	for _, a := range actions {
		switch a := a.(type) {
		case Submit:
			n := 1
			if a.Twice {
				n = 2
			}
			var wg sync.WaitGroup
			var once sync.Once
			for range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					code, ans := c.submit(ctx, a.Key)
					if code/100 == 2 && ans.ID != "" {
						once.Do(func() { ids[a.Key] = ans.ID; p.show(ans.ID, ans.Status) })
					}
				}()
			}
			wg.Wait()
		case Resubmit:
			c.submit(ctx, a.Key)
		case Cancel:
			id, ok := ids[a.Key]
			if !ok {
				continue
			}
			code, ans := c.request(ctx, "cancel", []any{"id", id}, http.MethodPost, c.Base+"/jobs/"+id+"/cancel", nil)
			if (code == http.StatusOK || code == http.StatusConflict) && ans.ID != "" {
				p.show(ans.ID, ans.Status)
			}
		case Poll:
			if id, ok := ids[a.Key]; ok {
				if err := c.poll(p, id); err != nil {
					return err
				}
			}
		case AwaitEnd:
			id, ok := ids[a.Key]
			if !ok {
				continue
			}
			start := time.Now()
			for p.shown(id) != "ready" && p.shown(id) != "cancelled" {
				if time.Since(start) > c.Stuck {
					return nil // the page shows it running for good
				}
				if err := c.poll(p, id); err != nil {
					return err
				}
				time.Sleep(20 * time.Millisecond)
			}
		case Sleep:
			time.Sleep(a.D)
		default:
			return fmt.Errorf("journey: action %v (%T) is none Play knows", a, a)
		}
	}
	return nil
}

func (c *Client) submit(ctx context.Context, key string) (int, answer) {
	body, _ := json.Marshal(map[string]string{"key": key})
	return c.request(ctx, "submit", []any{"key", key}, http.MethodPost, c.Base+"/jobs", body)
}

// poll asks for job id once (a poll is no call of the model) and shows a
// change.
func (c *Client) poll(p *page, id string) error {
	res, err := http.Get(c.Base + "/jobs/" + id)
	if err != nil {
		return err
	}
	var a answer
	json.NewDecoder(res.Body).Decode(&a)
	res.Body.Close()
	if res.StatusCode == http.StatusOK && a.ID == id {
		p.show(a.ID, a.Status)
	}
	return nil
}

// page is what the client shows: one job at a time, whose status only
// moves on.
type page struct {
	mu       sync.Mutex
	rec      context.Context
	id, last string
}

// show records a change of the job shown, as the page's show does.
func (p *page) show(id, status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.id == id && p.last != "accepted" && status == "accepted" {
		return // stale
	}
	if p.id != id || p.last != status {
		p.id, p.last = id, status
		qobs.Record(p.rec, "show", nil, "id", id, "status", status)
	}
}

// shown is the status shown of job id ("" if another is shown).
func (p *page) shown(id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.id != id {
		return ""
	}
	return p.last
}

type answer struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// request sends one request, recorded at its invocation and completed
// with its own answer: the HTTP status (0: none) and the job it named.
func (c *Client) request(ctx context.Context, action string, kv []any, method, url string, body []byte) (int, answer) {
	call := qobs.Invoke(ctx, action, kv...)
	code, a := 0, answer{}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		if res, err := http.DefaultClient.Do(req); err == nil {
			code = res.StatusCode
			json.NewDecoder(res.Body).Decode(&a)
			res.Body.Close()
		}
	}
	done := []any{"code", int64(code), "status", a.Status}
	if action == "submit" {
		done = append(done, "id", a.ID)
	}
	call.Done(nil, done...)
	return code, a
}
