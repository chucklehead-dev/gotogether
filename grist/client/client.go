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
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/calls"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// The defaults of a Client's retries.
const (
	// DefaultMaxAttempts is how many requests Do sends for one call
	// before leaving it pending.
	DefaultMaxAttempts = 50
	// DefaultRetryDelay is how long Do waits after a request that got no
	// answer (a timeout, a refused or reset connection) before sending it
	// again; after a 503, it waits the response's Retry-After.
	DefaultRetryDelay = 100 * time.Millisecond
)

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
	// MaxAttempts and RetryDelay bound Do's retries (0: the defaults).
	MaxAttempts int
	RetryDelay  time.Duration
}

// A StatusError is an answer the API gives for a request no retry
// fixes: 400 (no Idempotency-Key, arguments that do not decode), 404 (an
// unknown block or tenant), 500 (the decision's own error), or a code
// grist does not send.
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

// send sends one request and reads its answer.
func (c *Client) send(ctx context.Context, env grist.Envelope, req *http.Request) (*http.Response, []byte, error) {
	if c.ActorHeader != "" && env.Actor != "" {
		req.Header.Set(c.ActorHeader, env.Actor)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, nil, &retryable{err: err, after: c.delay()}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, &retryable{err: err, after: c.delay()}
	}
	return resp, b, nil
}

// refusal is the error of a non-200 answer.
func (c *Client) refusal(resp *http.Response, b []byte) error {
	var e body
	json.Unmarshal(b, &e)
	if e.Error == "" {
		e.Error = strings.TrimSpace(string(b))
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
		after := c.delay()
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
			after = time.Duration(s) * time.Second
		}
		base := grist.ErrUnavailable
		if e.Retry == "outcome-unknown" {
			base = grist.ErrOutcomeUnknown
		}
		return &retryable{err: fmt.Errorf("%w (the server: %s)", base, e.Error), after: after}
	}
	return &StatusError{Code: resp.StatusCode, Message: e.Error}
}

func (c *Client) path(kind, tenant, name string) string {
	return strings.TrimSuffix(c.URL, "/") + "/t/" + url.PathEscape(tenant) + "/" + kind + "/" + url.PathEscape(name)
}

// Command sends command name with JSON arguments args once, under
// env.ID as its Idempotency-Key. It returns the receipt, or the API's
// refusal: a *grist.Rejected (grist.ErrKeyReused among them),
// grist.ErrConflict, grist.ErrDenied, an error that is
// grist.ErrOutcomeUnknown (a 503 saying so, or no answer at all: the
// request may have committed) or grist.ErrUnavailable (send it again
// under the same id), or a *StatusError.
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
		return grist.Receipt{}, &retryable{err: fmt.Errorf("%w: %w", grist.ErrOutcomeUnknown, err), after: c.delay()}
	}
	if resp.StatusCode != http.StatusOK {
		return grist.Receipt{}, c.refusal(resp, b)
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
		return nil, 0, &retryable{err: fmt.Errorf("%w: %w", grist.ErrUnavailable, err), after: c.delay()}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, c.refusal(resp, b)
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
// string field as it is, any other as its JSON text.
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
// done with the answer). A request that got no answer, or a 503, is sent
// again under the same id (the same Idempotency-Key: a key's first
// answer is final, so a retry of a committed command gets its receipt),
// after RetryDelay or the 503's Retry-After, at most MaxAttempts times;
// a call never answered stays pending. It returns nil, the refusal (a
// *grist.Rejected, grist.ErrConflict, grist.ErrDenied), a *StatusError,
// or, for a call never answered, the last try's error
// (grist.ErrOutcomeUnknown or grist.ErrUnavailable).
func (c *Client) Do(ctx context.Context, env grist.Envelope, name string, args json.RawMessage) error {
	if c.App == nil {
		return errors.New("grist/client: a Client without its App: set Client.App (its blocks' types are what the calls record)")
	}
	query := calls.IsQuery(c.App, name)
	attempts := c.MaxAttempts
	if attempts <= 0 {
		attempts = DefaultMaxAttempts
	}
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
			if try < attempts {
				qenv.Sleep(ctx, rt.after)
			}
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
