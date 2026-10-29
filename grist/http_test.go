package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type Named struct {
	Name string `json:"name" grist:"tag"`
}

type Name struct {
	Name string `json:"name"`
}

type Many struct {
	N int `json:"n"`
}

// apiApp registers "name" (refused once a name is taken, served to
// editors), "secret" (internal) and the query "names" (served to
// anyone authenticated).
func apiApp(opts ...grist.Option) *grist.App {
	a := grist.New("names")
	named := grist.NewEvent[Named](a, "named")
	taken := grist.Rejection("taken")
	grist.NewCommand(a, "name", func(r *grist.Reads, c Name) ([]grist.Event, error) {
		if r.Exists(named, grist.Tag("name", c.Name)) {
			return nil, taken
		}
		return []grist.Event{named.Of(Named(c))}, nil
	}, append([]grist.Option{grist.Authorize(grist.Callers("editor"))}, opts...)...)
	grist.NewCommand(a, "secret", func(r *grist.Reads, c Name) ([]grist.Event, error) { return nil, nil }, grist.Internal())
	grist.NewQuery(a, "names", func(r *grist.Reads, q Name) (Many, error) {
		return Many{N: len(r.Select(grist.Selector{Types: []string{"named"}, Tags: []grist.TagPair{grist.Tag("name", q.Name)}}))}, nil
	}, grist.Authorize(grist.Authenticated))
	return a
}

// serve serves a on s over httptest, the caller taken from X-Caller.
func serve(t *testing.T, a *grist.App, s grist.Store) *httptest.Server {
	t.Helper()
	h, err := grist.NewHandler(a, s, grist.CallerFromHeader("X-Caller"), grist.DevEndpoints())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// do sends a request and returns its code, body and Retry-After.
func do(t *testing.T, method, url, caller, key, body string) (int, map[string]any, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if caller != "" {
		req.Header.Set("X-Caller", caller)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s %s: a body that is no JSON object: %s", method, url, b)
	}
	return resp.StatusCode, m, resp.Header.Get("Retry-After")
}

// The API's codes, by outcome: a commit's receipt (200, the same on a
// retry of its key, nothing appended twice), a rejection's name (422),
// a reused key (422, the key's own answer standing), no key or
// arguments that do not decode (400), an unknown block or tenant (404),
// a denial (403: a caller the policy refuses, an internal block); a
// query's result and position (200).
func TestAPICodes(t *testing.T) {
	st := newStoreNamed(t, "names")
	srv := serve(t, apiApp(), st)
	cmd := srv.URL + "/t/t1/commands/name"
	code, body, _ := do(t, "POST", cmd, "editor", "k1", `{"name":"a"}`)
	if code != 200 || body["pos"] != float64(1) {
		t.Fatalf("a commit: %d %v, want 200 with its receipt at 1", code, body)
	}
	if code, again, _ := do(t, "POST", cmd, "editor", "k1", `{"name":"a"}`); code != 200 || again["pos"] != float64(1) {
		t.Errorf("a retry of k1: %d %v, want the first answer (200 at 1)", code, again)
	}
	cases := []struct {
		method, url, caller, key, body string
		code                           int
		rejected                       string
	}{
		{"POST", cmd, "editor", "k2", `{"name":"a"}`, 422, "taken"},
		{"POST", cmd, "editor", "k1", `{"name":"b"}`, 422, "idempotency-key-reused"},
		{"POST", cmd, "editor", "", `{"name":"c"}`, 400, ""},
		{"POST", cmd, "editor", "k3", `{"nmae":"c"}`, 400, ""},
		{"POST", cmd, "editor", "grist:x", `{"name":"c"}`, 400, ""},
		{"POST", srv.URL + "/t/t1/commands/nope", "editor", "k4", `{}`, 404, ""},
		{"POST", srv.URL + "/t/t9/commands/name", "editor", "k5", `{"name":"c"}`, 404, ""},
		{"POST", cmd, "viewer", "k6", `{"name":"c"}`, 403, "denied"},
		{"POST", cmd, "", "k7", `{"name":"c"}`, 403, "denied"},
		{"POST", srv.URL + "/t/t1/commands/secret", "editor", "k8", `{"name":"c"}`, 404, ""},
		{"GET", srv.URL + "/t/t1/queries/names?name=a", "", "", "", 403, "denied"},
		{"GET", srv.URL + "/t/t1/queries/names?nmae=a", "viewer", "", "", 400, ""},
		{"GET", cmd, "editor", "k9", "", 405, ""},
	}
	for _, c := range cases {
		code, body, _ := doAny(t, c.method, c.url, c.caller, c.key, c.body)
		if code != c.code || c.rejected != "" && body["rejected"] != c.rejected {
			t.Errorf("%s %s (caller %q, key %q, %s): %d %v, want %d %s", c.method, c.url, c.caller, c.key, c.body, code, body, c.code, c.rejected)
		}
	}
	code, body, _ = do(t, "GET", srv.URL+"/t/t1/queries/names?name=a", "viewer", "", "")
	if res, _ := body["result"].(map[string]any); code != 200 || res["n"] != float64(1) || body["pos"] != float64(1) {
		t.Errorf("a query: %d %v, want 200, n 1 through 1", code, body)
	}
	if n := count(t, st, "named"); n != 1 {
		t.Errorf("%d named events, want 1: only k1 committed", n)
	}
	code, body, _ = do(t, "GET", srv.URL+"/catalog", "", "", "")
	if cmds, _ := body["commands"].([]any); code != 200 || len(cmds) != 2 {
		t.Errorf("the catalog: %d %v, want its two commands", code, body)
	}
}

// doAny is do for a body that may not be JSON (net/http's own 405).
func doAny(t *testing.T, method, url, caller, key, body string) (int, map[string]any, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if caller != "" {
		req.Header.Set("X-Caller", caller)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	return resp.StatusCode, m, resp.Header.Get("Retry-After")
}

func newStoreNamed(t *testing.T, name string) *memstore.Store {
	s := memstore.New(name)
	if err := s.CreateTenant(context.Background(), "t1"); err != nil {
		t.Fatal(err)
	}
	return s
}

// Every block is denied by default: an App with a command or query that
// declares neither Authorize nor Internal is not served, naming each.
func TestAPIDeniesByDefault(t *testing.T) {
	a := grist.New("open")
	named := grist.NewEvent[Named](a, "named")
	grist.NewCommand(a, "name", func(r *grist.Reads, c Name) ([]grist.Event, error) {
		return []grist.Event{named.Of(Named(c))}, nil
	})
	grist.NewQuery(a, "names", func(r *grist.Reads, q Name) (Many, error) { return Many{}, nil })
	_, err := grist.NewHandler(a, memstore.New("open"))
	if err == nil || !strings.Contains(err.Error(), "command name") || !strings.Contains(err.Error(), "query names") {
		t.Errorf("NewHandler of an App whose blocks declare no policy: %v, want an error naming both", err)
	}
	b := grist.New("both")
	grist.NewCommand(b, "x", func(r *grist.Reads, c Name) ([]grist.Event, error) { return nil, nil },
		grist.Authorize(grist.Authenticated), grist.Internal())
	if err := b.Verify(); err == nil || !strings.Contains(err.Error(), "both Authorize and Internal") {
		t.Errorf("Verify of a block both authorized and internal: %v", err)
	}
}

// unknownAppends commits nothing and answers every append with an
// outcome unknown, or, notCommitted, with an error before the commit.
type unknownAppends struct {
	*memstore.Store
	notCommitted bool
}

func (s unknownAppends) Append(context.Context, string, store.Batch) (store.Position, error) {
	err := errors.New("connection lost")
	if s.notCommitted {
		return 0, store.NotCommitted(err)
	}
	return 0, err
}

// conflicting fails every conditioned append's condition: the decision's
// reads changed on every try.
type conflicting struct{ *memstore.Store }

func (s conflicting) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if b.Condition != nil {
		return 0, &store.ConditionFailed{Event: 1}
	}
	return s.Store.Append(ctx, t, b)
}

// 503 with Retry-After, saying whether the outcome is unknown or nothing
// committed (retry under the same key either way); 409 on a conflict.
func TestAPIRetryAndConflict(t *testing.T) {
	for _, c := range []struct {
		st    grist.Store
		code  int
		field string
		want  string
	}{
		{unknownAppends{Store: newStoreNamed(t, "names")}, 503, "retry", "outcome-unknown"},
		{unknownAppends{Store: newStoreNamed(t, "names"), notCommitted: true}, 503, "retry", "unavailable"},
		{conflicting{newStoreNamed(t, "names")}, 409, "rejected", "conflict"},
	} {
		srv := serve(t, apiApp(grist.TransientTries(2), grist.ConditionTries(2)), c.st)
		code, body, after := do(t, "POST", srv.URL+"/t/t1/commands/name", "editor", "k1", `{"name":"a"}`)
		if code != c.code || body[c.field] != c.want || (code == 503) != (after != "") {
			t.Errorf("%T: %d %v (Retry-After %q), want %d with %s %s", c.st, code, body, after, c.code, c.field, c.want)
		}
	}
}

// The client over qhttp in a simulation: a command whose answer is lost
// is sent again under its key, and answered with the first receipt; the
// call is recorded once (invoked, then done) at the client, the commit
// once at the store; a refusal comes back as the API's.
func TestClientRetriesOverQHTTP(t *testing.T) {
	sink := &qobs.MemorySink{}
	base := qenv.NewSeeded(1)
	base.Rates = qenv.NoFaults
	script := &qenv.Script{Base: base, Faults: []qenv.Scripted{{Kind: "rpc.api.response", Name: "lost"}}}
	s := qenv.New(qenv.WithContext(script, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))))
	defer s.Close()
	a := apiApp()
	st := simstore.New(qenv.NewStore(s, "names", qenv.Serializable))
	st.CreateTenant(s.Context(), "t1")
	var srv *qhttp.Server
	served := map[string]int{} // requests that reached the handler, by key
	count := grist.Middleware(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			served[r.Header.Get("Idempotency-Key")]++
			h.ServeHTTP(w, r)
		})
	})
	node := s.Process("node", true, func(ctx context.Context) {
		h, err := grist.NewHandler(a, st, grist.CallerFromHeader("X-Caller"), count)
		if err != nil {
			panic(err)
		}
		if err := a.Start(ctx, st); err != nil {
			panic(err)
		}
		srv.Handle(ctx, h)
	})
	srv = qhttp.NewServer(s, "api", node)
	tr := qhttp.NewTransport(srv)
	tr.Timeout = time.Second
	c := &client.Client{App: a, URL: srv.URL(), HTTP: &http.Client{Transport: tr}, CallerHeader: "X-Caller"}
	var first, second, denied error
	s.Spawn("client", func(ctx context.Context) {
		ctx = qobs.Thread(ctx)
		first = c.Do(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"a"}`))
		second = c.Do(ctx, grist.Envelope{ID: "k2", Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"a"}`))
		denied = c.Do(ctx, grist.Envelope{ID: "k3", Tenant: "t1", Caller: "viewer"}, "name", []byte(`{"name":"b"}`))
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.Settle(time.Minute); err != nil {
		t.Fatal(err)
	}
	if first != nil || !errors.Is(second, grist.Rejection("taken")) || !errors.Is(denied, grist.ErrDenied) {
		t.Errorf("answers %v, %v, %v: want ok (after the lost answer), taken, denied", first, second, denied)
	}
	if served["k1"] != 2 {
		t.Errorf("k1 reached the handler %d times: want 2 (its first answer lost, then its retry, answered by its marker)", served["k1"])
	}
	invoked, done, commits := map[string]int{}, map[string]string{}, 0
	for _, x := range sink.Steps() {
		switch {
		case x.Actor == "names/t1/clients" && x.Invocation:
			invoked[x.Args["id"].(string)]++
		case x.Actor == "names/t1/clients" && x.Call != 0:
			done[x.Args["id"].(string)] = x.Outcome
		case x.Actor == "names/t1" && x.Action == "name" && x.Outcome == "ok":
			commits++
		}
	}
	if invoked["k1"] != 1 || done["k1"] != "ok" || done["k2"] != "taken" || done["k3"] != "denied" || commits != 1 {
		t.Errorf("client calls invoked %v, done %v, %d commits at the store: want each call once (k1 ok after its retry, k2 taken, k3 denied), one commit", invoked, done, commits)
	}
}

// Arguments grist refuses are the client's error (400), not the
// server's (500): a NUL byte in an argument a decision reads as a tag (in
// a command's body and in a query's parameter), a body that is not
// UTF-8 (encoding/json would read U+FFFD), a null body, a required field
// missing or null, an Idempotency-Key past MaxIDBytes; a body past
// MaxBody is 413; an Internal block is 404, as an unknown one, and no
// 404 names it. Before, the first two were 500, invalid UTF-8 committed
// U+FFFD, a null body or a missing field decided on the zero value, and
// an internal block answered 403.
func TestAPIRefusesBadArgs(t *testing.T) {
	st := newStoreNamed(t, "names")
	h, err := grist.NewHandler(apiApp(), st, grist.CallerFromHeader("X-Caller"), grist.MaxBody(64))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	cmd := srv.URL + "/t/t1/commands/name"
	for _, c := range []struct {
		method, url, key, body string
		code                   int
	}{
		{"POST", cmd, "k1", `{"name":"a\u0000"}`, 400},
		{"GET", srv.URL + "/t/t1/queries/names?name=%00", "", "", 400},
		{"GET", srv.URL + "/t/t1/queries/names?name=%ff", "", "", 400},
		{"POST", cmd, "k2", "{\"name\":\"\xff\"}", 400},
		{"POST", cmd, "k3", `null`, 400},
		{"POST", cmd, "k4", `{}`, 400},
		{"POST", cmd, "k5", `{"name":null}`, 400},
		{"GET", srv.URL + "/t/t1/queries/names", "", "", 400},
		{"POST", cmd, strings.Repeat("k", grist.MaxIDBytes+1), `{"name":"a"}`, 400},
		{"POST", cmd, "k6", `{"name":"` + strings.Repeat("a", 64) + `"}`, 413},
		{"POST", srv.URL + "/t/t1/commands/secret", "k7", `{"name":"a"}`, 404},
	} {
		code, body, _ := doAny(t, c.method, c.url, "editor", c.key, c.body)
		if code != c.code {
			t.Errorf("%s %s (key %.10q, %q): %d %v, want %d", c.method, c.url, c.key, c.body, code, body, c.code)
		}
		if msg, _ := body["error"].(string); code == 404 && strings.Contains(msg, "[name secret]") {
			t.Errorf("a 404 naming the internal block: %s", msg)
		}
	}
	if n := count(t, st, "named"); n != 0 {
		t.Errorf("%d named events: every request was refused before deciding", n)
	}
	// the same refusals in process: App.Execute's JSON and a typed Execute
	a := apiApp()
	env := grist.Envelope{ID: "k8", Tenant: "t1", Caller: "editor"}
	for _, body := range []string{`{"name":"a\u0000"}`, "{\"name\":\"\xff\"}", `null`, `{}`} {
		if _, err := a.Execute(context.Background(), st, env, "name", []byte(body)); !errors.Is(err, grist.ErrBadArgs) {
			t.Errorf("App.Execute of %q: %v, want ErrBadArgs", body, err)
		}
	}
}

// A command's answer taken from its key's marker before deciding says so
// (Idempotent-Replayed: true); its first answer does not.
func TestAPIReplayedHeader(t *testing.T) {
	srv := serve(t, apiApp(), newStoreNamed(t, "names"))
	send := func(key, body string) (int, string) {
		req, _ := http.NewRequest("POST", srv.URL+"/t/t1/commands/name", strings.NewReader(body))
		req.Header.Set("X-Caller", "editor")
		req.Header.Set("Idempotency-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get(grist.ReplayedHeader)
	}
	for i, c := range []struct {
		key, body, replayed string
		code                int
	}{
		{"k1", `{"name":"a"}`, "", 200}, {"k1", `{"name":"a"}`, "true", 200},
		{"k2", `{"name":"a"}`, "", 422}, {"k2", `{"name":"a"}`, "true", 422},
	} {
		if code, replayed := send(c.key, c.body); code != c.code || replayed != c.replayed {
			t.Errorf("request %d (%s): %d, %s %q; want %d, %q", i, c.key, code, grist.ReplayedHeader, replayed, c.code, c.replayed)
		}
	}
}

// The application's authentication wins over CallerFromHeader: a request
// a middleware authenticated as a viewer that names itself an editor in
// the header is a viewer's (403), where the header once overrode it
// (200). A request no middleware authenticated takes the header's caller.
func TestCallerHeaderDoesNotOverride(t *testing.T) {
	auth := grist.Middleware(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "Bearer viewer-token" {
				r = r.WithContext(grist.WithCaller(r.Context(), "viewer"))
			}
			h.ServeHTTP(w, r)
		})
	})
	h, err := grist.NewHandler(apiApp(), newStoreNamed(t, "names"), auth, grist.CallerFromHeader("X-Caller"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, c := range []struct {
		token string
		code  int
	}{{"viewer-token", 403}, {"", 200}} {
		req, _ := http.NewRequest("POST", srv.URL+"/t/t1/commands/name", strings.NewReader(`{"name":"`+c.token+`"}`))
		req.Header.Set("X-Caller", "editor")
		req.Header.Set("Idempotency-Key", "k-"+c.token)
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.code {
			t.Errorf("authenticated as %q, X-Caller editor: %d, want %d", c.token, resp.StatusCode, c.code)
		}
	}
}

// Opt is a query's arguments with an optional (pointer) field of each
// kind: QueryArgs takes a pointer to a string as a string, and leaves an
// absent pointer nil.
type Opt struct {
	Name  string   `json:"name"`
	Alias *string  `json:"alias"`
	N     int      `json:"n"`
	Max   *int     `json:"max"`
	Open  bool     `json:"open"`
	Tags  []string `json:"tags"`
}

// QueryArgs and client.Params are each other's inverse, a pointer field
// included (?alias=a was refused as no JSON value); a required field
// left out is refused, an optional one is nil.
func TestQueryArgsParams(t *testing.T) {
	alias, max := "3", 7
	typ := reflect.TypeFor[Opt]()
	for _, v := range []Opt{
		{Name: "a", Alias: &alias, N: 2, Max: &max, Open: true, Tags: []string{"x", "y"}},
		{Name: "b", Tags: []string{}},
	} {
		in, _ := json.Marshal(v)
		params, err := client.Params(in)
		if err != nil {
			t.Fatal(err)
		}
		out, err := grist.QueryArgs(typ, params)
		if err != nil {
			t.Fatalf("QueryArgs(Params(%s)) = %v: %v", in, params, err)
		}
		var back Opt
		json.Unmarshal(out, &back)
		if !reflect.DeepEqual(back, v) {
			t.Errorf("%s through %v came back %s", in, params, out)
		}
	}
	if _, err := grist.QueryArgs(typ, map[string][]string{"name": {"a"}, "alias": {"a"}, "n": {"1"}, "open": {"false"}, "tags": {"[]"}}); err != nil {
		t.Errorf("?alias=a for a *string: %v", err)
	}
	if _, err := grist.QueryArgs(typ, map[string][]string{"name": {"a"}}); err == nil || !strings.Contains(err.Error(), `"n" missing`) {
		t.Errorf("a required field left out: %v, want it named", err)
	}
}

// Big is a query's arguments of integers past float64's exact range.
type Big struct {
	N int64  `json:"n"`
	U uint64 `json:"u"`
}

// A step records an integer argument exactly (the step's fields were
// decoded through float64: 2^53+1 recorded as 2^53, MaxInt64 as
// MinInt64), and refuses one past int64 (an unsigned field's), with the
// fix, in process and over HTTP (400).
func TestStepFieldsExact(t *testing.T) {
	a := grist.New("big")
	grist.NewQuery(a, "echo", func(r *grist.Reads, q Big) (Big, error) { return q, nil }, grist.Authorize(grist.Authenticated))
	st := memstore.New("big")
	st.CreateTenant(context.Background(), "t1")
	sink := &qobs.MemorySink{}
	ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))
	env := grist.Envelope{Tenant: "t1", Caller: "x"}
	for _, n := range []int64{1<<53 + 1, math.MaxInt64, math.MinInt64} {
		if _, _, err := a.Query(ctx, st, env, "echo", []byte(fmt.Sprintf(`{"n":%d,"u":1}`, n))); err != nil {
			t.Fatal(err)
		}
		steps := sink.Steps()
		got := steps[len(steps)-1]
		if got.Args["n"] != n || got.Obs["n"] != n {
			t.Errorf("n %d recorded as %v (obs %v)", n, got.Args["n"], got.Obs["n"])
		}
	}
	if _, _, err := a.Query(ctx, st, env, "echo", []byte(`{"n":1,"u":18446744073709551615}`)); !errors.Is(err, grist.ErrBadArgs) || !strings.Contains(err.Error(), "int64") {
		t.Errorf("u past int64 in process: %v, want ErrBadArgs naming the fix", err)
	}
	srv := serve(t, a, st)
	if code, body, _ := doAny(t, "GET", srv.URL+"/t/t1/queries/echo?n=1&u=18446744073709551615", "x", "", ""); code != 400 {
		t.Errorf("u past int64 over HTTP: %d %v, want 400", code, body)
	}
}

// Main's server bounds every phase of a connection: a client that
// trickles its headers or body, or idles, holds none for ever.
func TestMainServerBounds(t *testing.T) {
	srv := grist.NewMainServer(context.Background(), http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 || srv.MaxHeaderBytes <= 0 {
		t.Errorf("Main's server: read header %v, read %v, write %v, idle %v, max header %d: want every one set",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout, srv.MaxHeaderBytes)
	}
}

// scripted serves answers in turn (the last for ever) over qhttp in a
// simulation, noting each request's virtual time; it returns the times
// and the client's error.
func scripted(t *testing.T, budget *client.Budget, answers ...func(w http.ResponseWriter)) ([]time.Duration, error) {
	t.Helper()
	s := qenv.New(quietChooser())
	defer s.Close()
	a := apiApp()
	var at []time.Duration
	var srv *qhttp.Server
	node := s.Process("node", true, func(ctx context.Context) {
		srv.Handle(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			at = append(at, s.Elapsed())
			answers[min(len(at), len(answers))-1](w)
		}))
	})
	srv = qhttp.NewServer(s, "api", node)
	tr := qhttp.NewTransport(srv)
	tr.Timeout = time.Second
	c := &client.Client{App: a, URL: srv.URL(), HTTP: &http.Client{Transport: tr}, Budget: budget}
	var err error
	s.Spawn("client", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond) // the server listening
		err = c.Do(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"a"}`))
	})
	if rerr := s.Run(10 * time.Minute); rerr != nil {
		t.Fatal(rerr)
	}
	return at, err
}

// The client counts on its call's step the tries no server took: a
// router's 502 or bare 503 (no grist behind it), a refused connection;
// not a 504 (a gateway that timed out may have reached grist) nor grist's
// own 503, nor the answered try.
func TestClientCountsRefusedTries(t *testing.T) {
	refusedOf := func(steps []qtrace.Step) any {
		for _, st := range steps {
			if st.Action == "name" && !st.Invocation {
				return st.Args["refused"]
			}
		}
		return "no step"
	}
	sink := &qobs.MemorySink{}
	ok := status(200, `{"pos":1,"events":[{"tenant":"t1","pos":1}]}`)
	s := qenv.New(qenv.WithContext(quietChooser(), qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))))
	answers := []func(http.ResponseWriter){status(502, "bad gateway"), status(503, "no endpoint"), status(504, "timeout"),
		status(503, `{"error":"x","retry":"unavailable"}`), ok}
	var srv *qhttp.Server
	n := 0
	node := s.Process("node", true, func(ctx context.Context) {
		srv.Handle(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n++; answers[min(n, len(answers))-1](w) }))
	})
	srv = qhttp.NewServer(s, "api", node)
	tr := qhttp.NewTransport(srv)
	tr.Timeout = time.Second
	c := &client.Client{App: apiApp(), URL: srv.URL(), HTTP: &http.Client{Transport: tr}, Budget: &client.Budget{Ratio: 10, Max: 10}}
	s.Spawn("client", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond)
		if err := c.Do(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"a"}`)); err != nil {
			t.Error(err)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got := fmt.Sprint(refusedOf(sink.Steps())); got != "2" {
		t.Errorf("a 502, a bare 503, a 504, grist's 503, then ok: refused %v, want 2", got)
	}
	// a refused connection (as net's dialer reports one), then the answer
	sink = &qobs.MemorySink{}
	s = qenv.New(qenv.WithContext(quietChooser(), qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))))
	defer s.Close()
	node = s.Process("node", true, func(ctx context.Context) {
		srv.Handle(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ok(w) }))
	})
	srv = qhttp.NewServer(s, "api", node)
	c = &client.Client{App: apiApp(), URL: srv.URL(), HTTP: &http.Client{Transport: &refuseFirst{next: qhttp.NewTransport(srv)}}}
	s.Spawn("client", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond)
		if err := c.Do(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"a"}`)); err != nil {
			t.Error(err)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(refusedOf(sink.Steps())); got != "1" {
		t.Errorf("a refused connection, then ok: refused %v, want 1", got)
	}
}

// refuseFirst refuses its first request's connection, then sends by next.
type refuseFirst struct {
	next http.RoundTripper
	done bool
}

func (r *refuseFirst) RoundTrip(req *http.Request) (*http.Response, error) {
	if !r.done {
		r.done = true
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}
	return r.next.RoundTrip(req)
}

func quietChooser() qenv.Chooser {
	c := qenv.NewSeeded(1)
	c.Rates = qenv.NoFaults
	return c
}

func status(code int, body string, header ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

// The client's retries back off exponentially with jitter (RetryDelay
// doubling, half of each wait drawn), wait at least a 503's Retry-After,
// take a 5xx that is not grist's (a proxy's 502, 504 or bare 503) to a
// command as an unknown outcome to send again under the key (it was a
// final StatusError, a lost commit unretried), and stop at the retry
// budget, leaving the call pending.
func TestClientBackoffAndBudget(t *testing.T) {
	ok := status(200, `{"pos":1,"events":[{"tenant":"t1","pos":1}]}`)
	at, err := scripted(t, nil, status(502, "bad gateway"), status(504, "timeout"), status(503, "busy"), ok)
	if err != nil || len(at) != 4 {
		t.Fatalf("502, 504, a bare 503, then ok: %d requests, %v; want 4, the last answered", len(at), err)
	}
	for i, lo := range []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond} {
		if gap := at[i+1] - at[i]; gap < lo || gap > 2*lo {
			t.Errorf("retry %d after %v: want within [%v, %v] (100 ms doubling, half jittered)", i+1, gap, lo, 2*lo)
		}
	}
	at, err = scripted(t, nil, status(503, `{"error":"x","retry":"unavailable"}`, "Retry-After", "1"), ok)
	if err != nil || len(at) != 2 || at[1]-at[0] < time.Second {
		t.Errorf("a 503 with Retry-After 1: requests at %v, %v; want the retry a second or more after", at, err)
	}
	at, err = scripted(t, &client.Budget{Ratio: 0, Max: 2}, status(502, "bad gateway"))
	if len(at) != 3 || !errors.Is(err, grist.ErrOutcomeUnknown) || !strings.Contains(err.Error(), "budget") {
		t.Errorf("502s for ever with a budget of 2: %d requests, %v; want 3, the outcome unknown, the budget named", len(at), err)
	}
	if _, err := scripted(t, nil, status(500, `{"error":"the decision failed"}`)); !errors.As(err, new(*client.StatusError)) {
		t.Errorf("grist's 500: %v, want a final StatusError", err)
	}
}
