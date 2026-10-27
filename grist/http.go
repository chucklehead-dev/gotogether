package grist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// ErrDenied is a command or query an adapter refused to serve: the
// block is Internal, or its policy (Authorize) does not admit the
// request's actor in its tenant. Nothing ran, so nothing is recorded at
// the store; a client that records its calls records the refusal
// ("denied").
var ErrDenied = errors.New("grist: denied: the block's policy (Authorize) does not admit the actor, or the block is Internal")

type actorKey struct{}

// WithActor is ctx carrying the request's authenticated actor: what the
// application's authentication (a middleware in front of NewHandler's
// handler, Middleware) sets, and what a block's Policy and the command's
// Envelope.Actor get.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorOf is ctx's actor (WithActor), "" if none: an unauthenticated
// request, which Authenticated refuses.
func ActorOf(ctx context.Context) string { a, _ := ctx.Value(actorKey{}).(string); return a }

// A ServeOption configures NewHandler and Main (Middleware,
// ActorFromHeader, DevEndpoints). The zero ServeOption changes nothing.
type ServeOption struct{ apply func(*serveConfig) }

type serveConfig struct {
	middleware  []func(http.Handler) http.Handler
	actorHeader string
	catalog     bool
	deps        func(context.Context) context.Context
}

// Dependencies attaches the application's dependencies (a mailer, a
// payment client) to every request's context and, under Main, the
// node's: a processor's effect reaches them through its ctx, as under
// gristsim.Config.Setup.
func Dependencies(f func(context.Context) context.Context) ServeOption {
	return ServeOption{apply: func(c *serveConfig) { c.deps = f }}
}

// Middleware wraps the handler in m: the application's authentication
// (which calls WithActor on the request's context), logging, limits.
// Several wrap in order, the first given outermost.
func Middleware(m func(http.Handler) http.Handler) ServeOption {
	return ServeOption{apply: func(c *serveConfig) { c.middleware = append(c.middleware, m) }}
}

// ActorFromHeader takes the request's actor from header name, as set by
// an authenticating proxy in front of the server (or a test's client:
// client.Client's ActorHeader). Never expose such a server directly: any
// client could name any actor.
func ActorFromHeader(name string) ServeOption {
	return ServeOption{apply: func(c *serveConfig) { c.actorHeader = name }}
}

// DevEndpoints serves GET /catalog: the App's registry as JSON
// (App.Catalog), for a generator or a developer's tools.
func DevEndpoints() ServeOption {
	return ServeOption{apply: func(c *serveConfig) { c.catalog = true }}
}

// The API's codes, by outcome (docs/grist.md §5).
const (
	// RetryAfter is the Retry-After (seconds) of a 503: the outcome is
	// unknown (grist.ErrOutcomeUnknown) or nothing committed
	// (ErrUnavailable); retry under the same Idempotency-Key.
	RetryAfter = 1
	// MaxBody is the largest command body the API reads.
	MaxBody = 1 << 20
)

// NewHandler is app's HTTP/JSON API on s (net/http only), a plain
// http.Handler, so qhttp.NewServer serves it in a simulation:
//
//	POST /t/{tenant}/commands/{name}   body: the arguments (JSON); header Idempotency-Key: the command id
//	GET  /t/{tenant}/queries/{name}?field=value...   a query's arguments by JSON name (a string as is, else JSON)
//	GET  /catalog                      with DevEndpoints
//
// A command answers 200 with its receipt ({"pos", "events": [{"tenant",
// "pos"}]}), the key's first answer on every retry; 422 with a
// rejection's name ({"rejected": "name-taken"}, or
// "idempotency-key-reused" for a key that names another command or
// other arguments: the key's own answer stands); 409 on a conflict after
// the condition's tries (also the key's final answer); 503 with
// Retry-After when the outcome is unknown or nothing committed
// ({"retry": "outcome-unknown" | "unavailable"}: send it again under the
// same key); 400 without an Idempotency-Key or with arguments that do
// not decode; 403 when denied; 404 for an unknown block or tenant; 500
// for the decision's own error. A query answers 200 with {"result",
// "pos"} (the position it read through), 422, 503 or 500 alike.
//
// Every block is denied by default: NewHandler refuses an App with a
// command or query that declares neither Authorize nor Internal, and
// answers 403 to an actor its policy refuses. The actor is the request
// context's (WithActor: the application's middleware, or
// ActorFromHeader). The handler records nothing itself: a command's
// steps are its commit's (relayed from the store's step log) or its
// refusal's, a query's its own, as on every adapter; a client records
// its side (grist/client).
func NewHandler(app *App, s Store, opts ...ServeOption) (http.Handler, error) {
	if err := app.Verify(); err != nil {
		return nil, err
	}
	var cfg serveConfig
	for _, o := range opts {
		if o.apply != nil {
			o.apply(&cfg)
		}
	}
	var open []string
	for _, n := range app.Commands() {
		if o := app.commands[n].opts; o.authorize == nil && !o.internal {
			open = append(open, "command "+n)
		}
	}
	for _, n := range app.Queries() {
		if o := app.queries[n].opts; o.authorize == nil && !o.internal {
			open = append(open, "query "+n)
		}
	}
	if len(open) > 0 {
		return nil, fmt.Errorf("grist: %s cannot be served: %v declare neither Authorize nor Internal, and every block is denied by default: "+
			"give each grist.Authorize(policy) (grist.Authenticated, grist.Actors(...)) or grist.Internal()", app.Name, open)
	}
	api := &api{app: app, s: s}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /t/{tenant}/commands/{name}", api.command)
	mux.HandleFunc("GET /t/{tenant}/queries/{name}", api.query)
	if cfg.catalog {
		mux.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, app.Catalog())
		})
	}
	var h http.Handler = mux
	if deps := cfg.deps; deps != nil {
		inner := h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { inner.ServeHTTP(w, r.WithContext(deps(r.Context()))) })
	}
	if name := cfg.actorHeader; name != "" {
		inner := h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a := r.Header.Get(name); a != "" {
				r = r.WithContext(WithActor(r.Context(), a))
			}
			inner.ServeHTTP(w, r)
		})
	}
	for i := len(cfg.middleware) - 1; i >= 0; i-- {
		h = cfg.middleware[i](h)
	}
	return h, nil
}

type api struct {
	app *App
	s   Store
}

// apiError is an error's body: its text, a refusal's name, or how to
// retry.
type apiError struct {
	Error    string `json:"error"`
	Rejected string `json:"rejected,omitempty"`
	Retry    string `json:"retry,omitempty"`
}

// The retry kinds of a 503's body.
const (
	retryUnknown     = "outcome-unknown"
	retryUnavailable = "unavailable"
)

// receiptJSON is a receipt as the API writes it.
type receiptJSON struct {
	Pos    Position      `json:"pos"`
	Events []eventRefDTO `json:"events"`
}

type eventRefDTO struct {
	Tenant string   `json:"tenant"`
	Pos    Position `json:"pos"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		code, b = http.StatusInternalServerError, []byte(`{"error":"grist: the answer did not encode"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(append(b, '\n'))
}

func fail(w http.ResponseWriter, code int, format string, args ...any) {
	writeJSON(w, code, apiError{Error: fmt.Sprintf(format, args...)})
}

// allowed reports whether o's block is served to the request's actor in
// tenant, answering 403 if not.
func allowed(w http.ResponseWriter, r *http.Request, o options, tenant string) bool {
	if o.internal || o.authorize == nil || !o.authorize(ActorOf(r.Context()), tenant) {
		writeJSON(w, http.StatusForbidden, apiError{Error: ErrDenied.Error(), Rejected: "denied"})
		return false
	}
	return true
}

// answer writes err's response (nil: none written, the caller writes
// the result).
func answer(w http.ResponseWriter, err error) {
	var rej *Rejected
	switch {
	case errors.As(err, &rej):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: err.Error(), Rejected: rej.Name})
	case errors.Is(err, ErrConflict):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error(), Rejected: conflictOutcome})
	case errors.Is(err, ErrOutcomeUnknown):
		w.Header().Set("Retry-After", strconv.Itoa(RetryAfter))
		writeJSON(w, http.StatusServiceUnavailable, apiError{Error: err.Error(), Retry: retryUnknown})
	case errors.Is(err, ErrUnavailable):
		w.Header().Set("Retry-After", strconv.Itoa(RetryAfter))
		writeJSON(w, http.StatusServiceUnavailable, apiError{Error: err.Error(), Retry: retryUnavailable})
	case errors.Is(err, store.ErrUnknownTenant):
		writeJSON(w, http.StatusNotFound, apiError{Error: err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
	}
}

func (a *api) command(w http.ResponseWriter, r *http.Request) {
	tenant, name := r.PathValue("tenant"), r.PathValue("name")
	c := a.app.commands[name]
	if c == nil {
		fail(w, http.StatusNotFound, "grist: %s has no command %q (it has %v)", a.app.Name, name, a.app.Commands())
		return
	}
	if err := CheckTenant(tenant); err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		fail(w, http.StatusBadRequest, "grist: command %s without an Idempotency-Key header: send the command id in it, the same on every retry", name)
		return
	}
	if err := checkID(key); err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	if !allowed(w, r, c.opts, tenant) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, "grist: command %s: the body: %v", name, err)
		return
	}
	if err := decodeStrict(body, c.args); err != nil {
		fail(w, http.StatusBadRequest, "grist: command %s: arguments %s: %v (want %s)", name, body, err, schemaText(c.args))
		return
	}
	rc, err := a.app.Execute(r.Context(), a.s, Envelope{ID: key, Tenant: tenant, Actor: ActorOf(r.Context())}, name, body)
	if err != nil {
		answer(w, err)
		return
	}
	out := receiptJSON{Pos: rc.Pos, Events: []eventRefDTO{}}
	for _, e := range rc.Events {
		out.Events = append(out.Events, eventRefDTO{Tenant: e.Tenant, Pos: e.Pos})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) query(w http.ResponseWriter, r *http.Request) {
	tenant, name := r.PathValue("tenant"), r.PathValue("name")
	q := a.app.queries[name]
	if q == nil {
		fail(w, http.StatusNotFound, "grist: %s has no query %q (it has %v)", a.app.Name, name, a.app.Queries())
		return
	}
	if err := CheckTenant(tenant); err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	if !allowed(w, r, q.opts, tenant) {
		return
	}
	args, err := QueryArgs(q.args, r.URL.Query())
	if err != nil {
		fail(w, http.StatusBadRequest, "grist: query %s: %v", name, err)
		return
	}
	res, pos, err := a.app.Query(r.Context(), a.s, Envelope{Tenant: tenant, Actor: ActorOf(r.Context())}, name, args)
	if err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Result json.RawMessage `json:"result"`
		Pos    Position        `json:"pos"`
	}{res, pos})
}

// decodeStrict decodes body as a value of typ, refusing a field typ does
// not have (a misspelled argument would otherwise be its zero value).
func decodeStrict(body []byte, typ reflect.Type) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(reflect.New(typ).Interface()); err != nil {
		return err
	}
	if d.More() {
		return errors.New("more than one JSON value")
	}
	return nil
}

// QueryArgs are a query's URL parameters as its JSON arguments of type
// typ: each parameter a field by its JSON name, a string field's value as
// it is, any other's parsed as JSON (by=3, open=true, tags=["a","b"]).
// A parameter typ has no field of, or one given twice, is an error; a
// field not given is its zero value.
func QueryArgs(typ reflect.Type, params map[string][]string) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	fields := map[string]reflect.Type{}
	if typ != nil && typ.Kind() == reflect.Struct {
		for i := 0; i < typ.NumField(); i++ {
			if f := typ.Field(i); f.IsExported() {
				fields[rec.JSONName(f)] = f.Type
			}
		}
	}
	for k, vs := range params {
		ft, ok := fields[k]
		if !ok {
			return nil, fmt.Errorf("parameter %q is no argument (want %s)", k, schemaText(typ))
		}
		if len(vs) != 1 {
			return nil, fmt.Errorf("parameter %q given %d times: give it once", k, len(vs))
		}
		if ft.Kind() == reflect.String {
			b, _ := json.Marshal(vs[0])
			obj[k] = b
			continue
		}
		if !json.Valid([]byte(vs[0])) {
			return nil, fmt.Errorf("parameter %q: %q is no JSON value (a %s)", k, vs[0], ft)
		}
		obj[k] = json.RawMessage(vs[0])
	}
	b, _ := json.Marshal(obj)
	if typ != nil {
		if err := decodeStrict(b, typ); err != nil {
			return nil, fmt.Errorf("arguments %s: %v (want %s)", b, err, schemaText(typ))
		}
	}
	return b, nil
}

// schemaText is typ's JSON Schema, for an error's fix.
func schemaText(typ reflect.Type) string {
	b, _ := json.Marshal(Schema(typ))
	return string(b)
}
