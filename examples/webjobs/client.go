package harness

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

// Client drives webjobs' API as the page does, without a browser: the
// per-push tier's run of the real server (TestClient, and each mutant's
// TestMutant under -short). Each journey submits a key twice at once (the
// page's double click), shows the first answer that names a job, polls
// every 20ms and shows each change, and in some journeys cancels the job
// at a random moment. It records what the adapter records of a Bombadil
// trace, on recorder "ui": each request an invocation (qobs.Invoke) and
// its own answer its completion (Call.Done: code, id, status), each
// change of the job shown a show step. Recorded live, with the client's
// own clock: the times are the requests', not a conversion's.
type Client struct {
	Base     string         // the server's URL
	Recorder *qobs.Recorder // recorder "ui"
	Journeys int            // default 10
	Seed     int64          // its choices: whether and when to cancel
	// CancelWithin bounds when a journey that cancels does, after its
	// job was shown (default 500ms)
	CancelWithin time.Duration
	// Stuck is how long a journey waits for its job to end before the
	// client gives up, as a page stuck at "accepted" would never end it
	// (default 3s)
	Stuck time.Duration
}

// Run drives the journeys one after another. It returns early, with no
// error, at a journey whose job never ended: the page would show it
// running for good.
func (c *Client) Run(ctx context.Context) error {
	if c.Journeys == 0 {
		c.Journeys = 10
	}
	if c.Stuck == 0 {
		c.Stuck = 3 * time.Second
	}
	if c.CancelWithin == 0 {
		c.CancelWithin = 500 * time.Millisecond
	}
	ctx = qobs.WithRecorder(ctx, c.Recorder)
	rng := rand.New(rand.NewSource(c.Seed))
	for i := range c.Journeys {
		cancelAt := time.Duration(-1)
		if rng.Intn(2) == 0 {
			cancelAt = time.Duration(rng.Int63n(int64(c.CancelWithin)))
		}
		ended, err := c.journey(ctx, fmt.Sprintf("k%d-%d", c.Seed, i+1), cancelAt)
		if err != nil || !ended {
			return err
		}
	}
	return nil
}

// page is what the client shows: one job, whose status only moves on.
type page struct {
	mu       sync.Mutex
	rec      context.Context
	id, last string
}

// show records a change of the job shown, as the page's show does.
func (p *page) show(id, status string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.id == id && p.last != "accepted" && status == "accepted" {
		return false // stale
	}
	if p.id != id || p.last != status {
		p.id, p.last = id, status
		qobs.Record(p.rec, "show", nil, "id", id, "status", status)
	}
	return true
}

func (p *page) status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
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

// journey is one submission: its double click, its polls and maybe its
// cancel. ended says the job was shown ended.
func (c *Client) journey(ctx context.Context, key string, cancelAt time.Duration) (bool, error) {
	p := &page{rec: ctx}
	body, _ := json.Marshal(map[string]string{"key": key})
	var wg sync.WaitGroup
	var once sync.Once
	var id string
	for range 2 { // a double click: two requests, the first answer naming a job shown
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, a := c.request(ctx, "submit", []any{"key", key}, http.MethodPost, c.Base+"/jobs", body)
			if code/100 == 2 && a.ID != "" {
				once.Do(func() { id = a.ID; p.show(a.ID, a.Status) })
			}
		}()
	}
	wg.Wait()
	if id == "" {
		return true, nil // no job: nothing to wait for
	}
	start := time.Now()
	cancelled := cancelAt < 0
	for {
		if st := p.status(); st == "ready" || st == "cancelled" {
			return true, nil
		}
		if time.Since(start) > c.Stuck {
			return false, nil
		}
		if !cancelled && time.Since(start) >= cancelAt {
			cancelled = true
			code, a := c.request(ctx, "cancel", []any{"id", id}, http.MethodPost, c.Base+"/jobs/"+id+"/cancel", nil)
			if (code == http.StatusOK || code == http.StatusConflict) && a.ID != "" {
				p.show(a.ID, a.Status)
			}
			continue
		}
		res, err := http.Get(c.Base + "/jobs/" + id)
		if err != nil {
			return false, err
		}
		var a answer
		json.NewDecoder(res.Body).Decode(&a)
		res.Body.Close()
		if res.StatusCode == http.StatusOK && a.ID == id {
			p.show(a.ID, a.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
