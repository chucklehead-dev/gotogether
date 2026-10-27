package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	}, append([]grist.Option{grist.Authorize(grist.Actors("editor"))}, opts...)...)
	grist.NewCommand(a, "secret", func(r *grist.Reads, c Name) ([]grist.Event, error) { return nil, nil }, grist.Internal())
	grist.NewQuery(a, "names", func(r *grist.Reads, q Name) (Many, error) {
		return Many{N: len(r.Select(grist.Selector{Types: []string{"named"}, Tags: []grist.TagPair{grist.Tag("name", q.Name)}}))}, nil
	}, grist.Authorize(grist.Authenticated))
	return a
}

// serve serves a on s over httptest, the actor taken from X-Actor.
func serve(t *testing.T, a *grist.App, s grist.Store) *httptest.Server {
	t.Helper()
	h, err := grist.NewHandler(a, s, grist.ActorFromHeader("X-Actor"), grist.DevEndpoints())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// do sends a request and returns its code, body and Retry-After.
func do(t *testing.T, method, url, actor, key, body string) (int, map[string]any, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if actor != "" {
		req.Header.Set("X-Actor", actor)
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
// a denial (403: an actor the policy refuses, an internal block); a
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
		method, url, actor, key, body string
		code                          int
		rejected                      string
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
		{"POST", srv.URL + "/t/t1/commands/secret", "editor", "k8", `{"name":"c"}`, 403, "denied"},
		{"GET", srv.URL + "/t/t1/queries/names?name=a", "", "", "", 403, "denied"},
		{"GET", srv.URL + "/t/t1/queries/names?nmae=a", "viewer", "", "", 400, ""},
		{"GET", cmd, "editor", "k9", "", 405, ""},
	}
	for _, c := range cases {
		code, body, _ := doAny(t, c.method, c.url, c.actor, c.key, c.body)
		if code != c.code || c.rejected != "" && body["rejected"] != c.rejected {
			t.Errorf("%s %s (actor %q, key %q, %s): %d %v, want %d %s", c.method, c.url, c.actor, c.key, c.body, code, body, c.code, c.rejected)
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
func doAny(t *testing.T, method, url, actor, key, body string) (int, map[string]any, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if actor != "" {
		req.Header.Set("X-Actor", actor)
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
		h, err := grist.NewHandler(a, st, grist.ActorFromHeader("X-Actor"), count)
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
	c := &client.Client{App: a, URL: srv.URL(), HTTP: &http.Client{Transport: tr}, ActorHeader: "X-Actor"}
	var first, second, denied error
	s.Spawn("client", func(ctx context.Context) {
		ctx = qobs.Thread(ctx)
		first = c.Do(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Actor: "editor"}, "name", []byte(`{"name":"a"}`))
		second = c.Do(ctx, grist.Envelope{ID: "k2", Tenant: "t1", Actor: "editor"}, "name", []byte(`{"name":"a"}`))
		denied = c.Do(ctx, grist.Envelope{ID: "k3", Tenant: "t1", Actor: "viewer"}, "name", []byte(`{"name":"b"}`))
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
		case x.Actor == "t1/clients" && x.Invocation:
			invoked[x.Args["id"].(string)]++
		case x.Actor == "t1/clients" && x.Call != 0:
			done[x.Args["id"].(string)] = x.Outcome
		case x.Actor == "t1" && x.Action == "name" && x.Outcome == "ok":
			commits++
		}
	}
	if invoked["k1"] != 1 || done["k1"] != "ok" || done["k2"] != "taken" || done["k3"] != "denied" || commits != 1 {
		t.Errorf("client calls invoked %v, done %v, %d commits at the store: want each call once (k1 ok after its retry, k2 taken, k3 denied), one commit", invoked, done, commits)
	}
}
