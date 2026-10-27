// Package client calls a grist App's HTTP API (grist.NewHandler) and
// records its calls where the client sees them, as a simulation's
// clients do (actor <tenant>/clients: qobs.Invoke before the first
// request, Call.Done with the answer, every retry inside the one call),
// so a client-side binding judges a real tier's clients as it judges a
// simulation's. Over qhttp's Transport it is gristsim's HTTP tier
// (gristsim.Config.HTTP); over a real http.Client, a real one.
//
// The server records the store's side (commits from its step log,
// refusals at their commit, queries on return); the client records only
// its own.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/calls"
	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// The defaults of a Client's retries.
const (
	// DefaultMaxAttempts is how many requests Do sends for one call
	// before leaving it pending.
	DefaultMaxAttempts = 50
	// DefaultRetryDelay is the first retry's backoff: Do waits about it
	// after a request that got no answer (a timeout, a refused or reset
	// connection), doubling with every retry of the call up to
	// DefaultMaxRetryDelay, jittered; after a 503 it waits at least the
	// response's Retry-After.
	DefaultRetryDelay = 100 * time.Millisecond
	// DefaultMaxRetryDelay caps the backoff.
	DefaultMaxRetryDelay = 5 * time.Second
)

// A Budget bounds a Client's retries, over all its calls: a token bucket
// that every call fills by Ratio and every retry drains by one, holding
// at most Max (and Max at the start). When it is empty a call is not
// retried: it ends with its last error, a command's pending (its outcome
// unknown). So a server that stops answering gets at most Ratio retries
// per call beyond Max, not MaxAttempts per call: retries that multiply
// the load of an overloaded server are what keep it from recovering
// after the overload is gone (a metastable retry storm; examples/
// retrystorm shows backoff alone keeping the storm and a budget ending
// it). Safe for concurrent use.
type Budget struct {
	// Ratio is the retries one call earns (0.2: one retry per five calls).
	Ratio float64
	// Max is the most retries kept, and the bucket's start.
	Max float64

	mu      sync.Mutex
	started bool
	tokens  float64
}

// DefaultBudget is a Budget of Ratio 0.2 and Max 10 (Finagle's
// RetryBudget's defaults, without its time-based reserve). A Client
// without one makes one of these for itself.
func DefaultBudget() *Budget { return &Budget{Ratio: 0.2, Max: 10} }

func (b *Budget) fill() {
	if !b.started {
		b.started, b.tokens = true, b.Max
	}
}

// deposit records a new call.
func (b *Budget) deposit() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fill()
	b.tokens = min(b.Max, b.tokens+b.Ratio)
}

// withdraw takes a retry's token, false when the budget is spent.
func (b *Budget) withdraw() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fill()
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Tokens is the retries the budget holds now.
func (b *Budget) Tokens() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fill()
	return b.tokens
}

// A Client calls one App's API at URL.
type Client struct {
	// App is the application served at URL: its blocks' argument and
	// result types are what a call's step records.
	App *grist.App
	// URL is the server's base URL ("http://localhost:8080"; in a
	// simulation, qhttp.Server.URL).
	URL string
	// HTTP sends the requests (nil: http.DefaultClient). In a simulation,
	// one whose Transport is a qhttp.Transport, its Timeout the call's.
	HTTP *http.Client
	// ActorHeader, if set, carries each call's Envelope.Actor (for a
	// server with grist.ActorFromHeader: tests and demos, or behind a
	// proxy that authenticates). Otherwise authenticate in HTTP's
	// Transport.
	ActorHeader string
	// MaxAttempts bounds Do's requests for one call; RetryDelay and
	// MaxRetryDelay are its backoff's first wait and cap (0: the
	// defaults).
	MaxAttempts   int
	RetryDelay    time.Duration
	MaxRetryDelay time.Duration
	// Budget bounds the retries of all of Do's calls (nil: a
	// DefaultBudget of the Client's own, made at its first call).
	Budget *Budget

	once  sync.Once
	calls atomic.Uint64 // Do's calls so far: each one's jitter is its own
}

// A StatusError is an answer the API gives for a request no retry
// fixes: 400 (no Idempotency-Key, arguments grist refuses), 404 (an
// unknown block or tenant), 413 (a body too large), 500 (the decision's
// own error, in grist's body), or a 4xx grist does not send. A 5xx
// without grist's body (a proxy's 502 or 504, a load balancer's 503) is
// no StatusError: no grist answered, so a command's outcome is unknown
// and Do sends it again under its key.
type StatusError struct {
	Code int
	// Message is the body's error text.
	Message string
}

func (e *StatusError) Error() string { return fmt.Sprintf("grist API %d: %s", e.Code, e.Message) }

// body is the API's error body (grist's apiError).
type body struct {
	Error    string `json:"error"`
	Rejected string `json:"rejected"`
	Retry    string `json:"retry"`
}

// retryable is an answer that says to send the request again (a 503, or
// no answer), and when.
type retryable struct {
	err   error
	after time.Duration
}

func (r *retryable) Error() string { return r.err.Error() }
func (r *retryable) Unwrap() error { return r.err }

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) delay() time.Duration {
	if c.RetryDelay > 0 {
		return c.RetryDelay
	}
	return DefaultRetryDelay
}

// backoff is the wait before retry n (1-based) of a call whose jitter
// seed is seed, at least floor (a 503's Retry-After): RetryDelay doubling
// per retry up to MaxRetryDelay, of which a uniform part is jittered
// ("equal jitter": half kept, half drawn), so the retries of calls that
// failed together do not come back together. The draw is a hash of the
// call and the retry, not a random source: a simulation's run stays a
// function of its choices, and calls still spread.
func (c *Client) backoff(n int, seed uint64, floor time.Duration) time.Duration {
	limit := c.MaxRetryDelay
	if limit <= 0 {
		limit = DefaultMaxRetryDelay
	}
	d := c.delay() << min(n-1, 20)
	if d > limit || d <= 0 {
		d = limit
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "%d/%d", seed, n)
	half := d / 2
	d = half + time.Duration(h.Sum64()%uint64(half+1))
	return max(d, floor)
}

func (c *Client) budget() *Budget {
	c.once.Do(func() {
		if c.Budget == nil {
			c.Budget = DefaultBudget()
		}
	})
	return c.Budget
}

// send sends one request and reads its answer.
func (c *Client) send(ctx context.Context, env grist.Envelope, req *http.Request) (*http.Response, []byte, error) {
	if c.ActorHeader != "" && env.Actor != "" {
		req.Header.Set(c.ActorHeader, env.Actor)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, nil, &retryable{err: err}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, &retryable{err: err}
	}
	return resp, b, nil
}

// refusal is the error of a non-200 answer to a command (or a query).
func (c *Client) refusal(resp *http.Response, b []byte, query bool) error {
	var e body
	gristBody := json.Unmarshal(b, &e) == nil && e.Error != ""
	if !gristBody {
		e.Error = strings.TrimSpace(string(b))
	}
	if resp.StatusCode >= 500 && !gristBody || resp.StatusCode == http.StatusServiceUnavailable && e.Retry == "" {
		// no grist answered (a proxy's 502 or 504, a load balancer's 503):
		// the request may have reached one that committed it
		base := grist.ErrOutcomeUnknown
		if query {
			base = grist.ErrUnavailable
		}
		return &retryable{err: fmt.Errorf("%w (%d without grist's answer: %.200s)", base, resp.StatusCode, e.Error), after: retryAfter(resp)}
	}
	switch resp.StatusCode {
	case http.StatusUnprocessableEntity:
		if e.Rejected != "" {
			return &grist.Rejected{Name: e.Rejected}
		}
	case http.StatusConflict:
		return grist.ErrConflict
	case http.StatusForbidden:
		return grist.ErrDenied
	case http.StatusServiceUnavailable:
		base := grist.ErrUnavailable
		if e.Retry == "outcome-unknown" {
			base = grist.ErrOutcomeUnknown
		}
		return &retryable{err: fmt.Errorf("%w (the server: %s)", base, e.Error), after: retryAfter(resp)}
	}
	return &StatusError{Code: resp.StatusCode, Message: e.Error}
}

// retryAfter is resp's Retry-After in seconds, 0 if none: the least a
// retry waits.
func retryAfter(resp *http.Response) time.Duration {
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	return 0
}

func (c *Client) path(kind, tenant, name string) string {
	return strings.TrimSuffix(c.URL, "/") + "/t/" + url.PathEscape(tenant) + "/" + kind + "/" + url.PathEscape(name)
}

// Command sends command name with JSON arguments args once, under
// env.ID as its Idempotency-Key. It returns the receipt, or the API's
// refusal: a *grist.Rejected (grist.ErrKeyReused among them),
// grist.ErrConflict, grist.ErrDenied, an error that is
// grist.ErrOutcomeUnknown (a 503 saying so, no answer at all, or a 5xx
// that is not grist's: the request may have committed) or
// grist.ErrUnavailable (send it again under the same id), or a
// *StatusError.
func (c *Client) Command(ctx context.Context, env grist.Envelope, name string, args json.RawMessage) (grist.Receipt, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.path("commands", env.Tenant, name), bytes.NewReader(args))
	if err != nil {
		return grist.Receipt{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", env.ID)
	resp, b, err := c.send(ctx, env, req)
	if err != nil {
		// no answer: the request may have been processed
		return grist.Receipt{}, &retryable{err: fmt.Errorf("%w: %w", grist.ErrOutcomeUnknown, err)}
	}
	if resp.StatusCode != http.StatusOK {
		return grist.Receipt{}, c.refusal(resp, b, false)
	}
	var r struct {
		Pos    grist.Position `json:"pos"`
		Events []struct {
			Tenant string         `json:"tenant"`
			Pos    grist.Position `json:"pos"`
		} `json:"events"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return grist.Receipt{}, &StatusError{Code: resp.StatusCode, Message: fmt.Sprintf("a receipt that does not decode (%v): %s", err, b)}
	}
	rc := grist.Receipt{Pos: r.Pos, Events: []grist.EventRef{}}
	for _, e := range r.Events {
		rc.Events = append(rc.Events, grist.EventRef{Tenant: e.Tenant, Pos: e.Pos})
	}
	return rc, nil
}

// Query sends query name with JSON arguments args once (as URL
// parameters: grist.QueryArgs's inverse), returning its result and the
// position it read through, or a refusal as Command's (a lost answer is
// grist.ErrUnavailable: a query commits nothing).
func (c *Client) Query(ctx context.Context, env grist.Envelope, name string, args json.RawMessage) (json.RawMessage, grist.Position, error) {
	params, err := Params(args)
	if err != nil {
		return nil, 0, fmt.Errorf("grist/client: query %s: %w", name, err)
	}
	u := c.path("queries", env.Tenant, name)
	if q := params.Encode(); q != "" {
		u += "?" + q
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, b, err := c.send(ctx, env, req)
	if err != nil {
		return nil, 0, &retryable{err: fmt.Errorf("%w: %w", grist.ErrUnavailable, err)}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, c.refusal(resp, b, true)
	}
	var r struct {
		Result json.RawMessage `json:"result"`
		Pos    grist.Position  `json:"pos"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, 0, &StatusError{Code: resp.StatusCode, Message: fmt.Sprintf("a result that does not decode (%v): %s", err, b)}
	}
	return r.Result, r.Pos, nil
}

// Params are a query's JSON arguments (an object) as URL parameters: a
// string field as it is, any other as its JSON text, a null one (a nil
// pointer) left out. grist.QueryArgs is its inverse.
func Params(args json.RawMessage) (url.Values, error) {
	v := url.Values{}
	if len(bytes.TrimSpace(args)) == 0 {
		return v, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(args, &obj); err != nil {
		return nil, fmt.Errorf("arguments %s are no JSON object: %w", args, err)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		raw := bytes.TrimSpace(obj[k])
		if string(raw) == "null" {
			continue // QueryArgs leaves an absent pointer nil
		}
		var s string
		if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &s) == nil {
			v.Set(k, s)
			continue
		}
		v.Set(k, string(raw))
	}
	return v, nil
}

// Do calls command or query name for env and records the call as a
// client's (actor <tenant>/clients: invoked before the first request,
// done with the answer). A request that got no answer, a 503, or a 5xx
// that is not grist's is sent again under the same id (the same
// Idempotency-Key: a key's first answer is final, so a retry of a
// committed command gets its receipt) after a jittered exponential
// backoff (RetryDelay doubling to MaxRetryDelay; at least a 503's
// Retry-After), at most MaxAttempts times and while the Budget holds a
// retry; a call never answered stays pending. It returns nil, the
// refusal (a *grist.Rejected, grist.ErrConflict, grist.ErrDenied), a
// *StatusError, or, for a call never answered, the last try's error
// (grist.ErrOutcomeUnknown or grist.ErrUnavailable). Arguments that are
// not the block's (an integer past the model's) are refused before any
// request, and not recorded.
func (c *Client) Do(ctx context.Context, env grist.Envelope, name string, args json.RawMessage) error {
	if c.App == nil {
		return errors.New("grist/client: a Client without its App: set Client.App (its blocks' types are what the calls record)")
	}
	if len(args) > 0 {
		if _, err := rec.StepFields(c.App.ArgsType(name), args, ""); err != nil {
			return fmt.Errorf("grist/client: %s: %w: %w", name, grist.ErrBadArgs, err)
		}
	}
	query := calls.IsQuery(c.App, name)
	attempts := c.MaxAttempts
	if attempts <= 0 {
		attempts = DefaultMaxAttempts
	}
	budget := c.budget()
	budget.deposit()
	seed := c.calls.Add(1)
	var last error
	calls.Record(ctx, c.App, env, name, args, func() calls.Answer {
		for try := 1; try <= attempts; try++ {
			var res json.RawMessage
			if query {
				res, _, last = c.Query(ctx, env, name, args)
			} else {
				_, last = c.Command(ctx, env, name, args)
			}
			var rt *retryable
			if !errors.As(last, &rt) {
				return answer(last, res)
			}
			if try == attempts {
				break
			}
			if !budget.withdraw() {
				last = fmt.Errorf("%w (grist/client: the retry budget is spent: not retried)", last)
				break
			}
			qenv.Sleep(ctx, c.backoff(try, seed, rt.after))
		}
		return calls.Answer{Pending: true}
	})
	return last
}

// answer is a call's final answer as its step records it.
func answer(err error, res json.RawMessage) calls.Answer {
	var rej *grist.Rejected
	switch {
	case err == nil:
		return calls.Answer{Result: res}
	case errors.As(err, &rej):
		return calls.Answer{Rejected: rej.Name}
	case errors.Is(err, grist.ErrConflict):
		return calls.Answer{Rejected: "conflict"}
	case errors.Is(err, grist.ErrDenied):
		return calls.Answer{Rejected: "denied"}
	}
	return calls.Answer{Err: err.Error()}
}
