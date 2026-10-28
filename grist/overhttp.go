package grist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CauseHeader is the header a call over HTTP carries its cause in (OverHTTP
// sends it, ServeCalls reads it): <app>/<tenant>/<processor>/<trigger>/<head>,
// each part path-escaped. With the Idempotency-Key (the call's key) it is
// what places the dependency's record of the call in the caller's tenant's
// trace, after the commit holding its trigger.
const CauseHeader = "Grist-Cause"

// callTimeout bounds a call over HTTP when OverHTTP is given no client: an
// answer that does not come within it is an unknown outcome, as a lost one
// is (a call may have happened).
const callTimeout = 10 * time.Second

// OverHTTP is a transport calling a dependency over HTTP: POST
// base/<call>, the request's JSON as the body, the call's key as its
// Idempotency-Key and its cause as Grist-Cause (CauseHeader), so a
// dependency the team owns serves it with ServeCalls and a third party
// deduplicates by the key. c is the client (nil: one bounded by
// callTimeout); OverHTTP uses a copy of it that follows no redirect. Its
// answers: a 2xx is the response (its body); a 422 with {"rejected":
// name} the dependency's refusal (a *Rejected: Do's declared refusal, if
// the call declares it); anything else an unknown outcome
// (ErrOutcomeUnknown: the call may have happened): a 5xx, a reset, a
// timeout, and also a 3xx (a redirect may follow an application, as
// POST-redirect-GET does, and following it would send the call's key,
// cause and body to another URL: a second send) and another 4xx. A 4xx
// of no declared name says the request was refused, but nothing the
// call declares says that nothing happened (a third party's 409 for a
// key in use, a gateway's 429): only a declared refusal is the
// dependency's word that it did not, so only it is the effect's
// grist.failed, and the caller's step, which records unknown for every
// answer that is no declared refusal, agrees with the outcome event. It
// makes one attempt: a transport never retries an unknown outcome (an
// at-most-once effect's one call would become several).
func OverHTTP(base string, c *http.Client) Transport {
	if c == nil {
		c = &http.Client{Timeout: callTimeout}
	}
	cc := *c
	cc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &overHTTP{base: strings.TrimSuffix(base, "/"), c: &cc}
}

type overHTTP struct {
	base string
	c    *http.Client
}

func (o *overHTTP) Call(ctx context.Context, ci CallInfo, call string, req json.RawMessage) (json.RawMessage, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/"+url.PathEscape(call), bytes.NewReader(req))
	if err != nil {
		// never sent, but Do recorded the call's invocation, and its step
		// records unknown for every answer that is no declared refusal
		return nil, unknownCall("call %s to %s: its request: %v", call, o.base, err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", ci.Key)
	r.Header.Set(CauseHeader, encodeCause(ci))
	resp, err := o.c.Do(r)
	if err != nil {
		// no answer: the request may have reached the dependency
		return nil, unknownCall("call %s to %s: %v", call, o.base, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, defaultMaxBody+1))
	switch {
	case err != nil:
		return nil, unknownCall("call %s to %s: its answer was cut: %v", call, o.base, err)
	case len(b) > defaultMaxBody:
		return nil, unknownCall("call %s to %s: an answer past %d bytes", call, o.base, defaultMaxBody)
	}
	var e apiError
	_ = json.Unmarshal(b, &e)
	switch c := resp.StatusCode; {
	case c >= 200 && c < 300:
		return b, nil
	case c == http.StatusUnprocessableEntity && e.Rejected != "":
		return nil, &Rejected{Name: e.Rejected}
	case c >= 300 && c < 400:
		return nil, unknownCall("call %s to %s answered %d, a redirect (to %s), which a call does not follow: give the dependency's own URL", call, o.base, c, resp.Header.Get("Location"))
	case c >= 400 && c < 500:
		return nil, unknownCall("call %s to %s answered %d, no refusal the call declares: %.200s", call, o.base, c, strings.TrimSpace(string(b)))
	}
	return nil, unknownCall("call %s to %s answered %d: %.200s", call, o.base, resp.StatusCode, strings.TrimSpace(string(b)))
}

// unknownCall is a call's unknown outcome: errors.Is ErrOutcomeUnknown,
// in words of its own (ErrOutcomeUnknown's are a command's, whose advice,
// a retry under the same id, an at-most-once effect must not take). Its
// text is what an at-most-once effect's grist.unknown carries.
func unknownCall(format string, args ...any) error {
	return &callUnknown{msg: "grist: " + fmt.Sprintf(format, args...) + ": its outcome is unknown (the call may have happened)"}
}

type callUnknown struct{ msg string }

func (e *callUnknown) Error() string        { return e.msg }
func (e *callUnknown) Is(target error) bool { return target == ErrOutcomeUnknown }

// encodeCause is c as CauseHeader's value.
func encodeCause(c CallInfo) string {
	return strings.Join([]string{url.PathEscape(c.App), url.PathEscape(c.Tenant), url.PathEscape(c.Processor),
		strconv.FormatInt(int64(c.Trigger), 10), strconv.FormatInt(int64(c.Head), 10)}, "/")
}

// userNames are the names of the processors that use ci.
func userNames(ci *callInfo) []string {
	out := make([]string, len(ci.users))
	for i, p := range ci.users {
		out[i] = p.name
	}
	return out
}

// decodeCause is CauseHeader's value v and the key as a CallInfo.
func decodeCause(v, key string) (CallInfo, error) {
	parts := strings.Split(v, "/")
	if len(parts) != 5 {
		return CallInfo{}, fmt.Errorf("%s %q: want <app>/<tenant>/<processor>/<trigger>/<head>, as grist.OverHTTP sends it", CauseHeader, v)
	}
	var s [3]string
	for i := range s {
		var err error
		if s[i], err = url.PathUnescape(parts[i]); err != nil {
			return CallInfo{}, fmt.Errorf("%s %q: %v", CauseHeader, v, err)
		}
	}
	trigger, terr := strconv.ParseInt(parts[3], 10, 64)
	head, herr := strconv.ParseInt(parts[4], 10, 64)
	if terr != nil || herr != nil || trigger < 1 || head < trigger {
		return CallInfo{}, fmt.Errorf("%s %q: its trigger and head are positions, the head at or past the trigger", CauseHeader, v)
	}
	if err := CheckTenant(s[1]); err != nil {
		return CallInfo{}, fmt.Errorf("%s %q: %v", CauseHeader, v, err)
	}
	if key == "" {
		return CallInfo{}, errors.New("no Idempotency-Key: a call carries its key (the effect's), as grist.OverHTTP sends it")
	}
	return CallInfo{App: s[0], Tenant: s[1], Processor: s[2], Trigger: Position(trigger), Head: Position(head), Key: key}, nil
}

// ServeCalls is an HTTP handler serving dependency d's calls with h, for
// a dependency the team owns: POST /<call> as OverHTTP sends it, each
// call by its handler (Call.Handler), whose wrapper records the call's
// application at its site, <call>Served (or <call>Replayed), into the
// recorder of the request's context: wrap the handler in
// qobshttp.Handler(h, rec) with the service's own recorder (its own step
// file, validated with the caller's as two recorders of one run, as
// gristtest.TierConfig.Serve does). Each record joins the caller's
// tenant's trace at its cause (the Grist-Cause and Idempotency-Key
// headers): its actor the tenant, placed after the commit holding its
// trigger, and never before an earlier record of the key (one
// ServeCalls orders its records of each key, whichever node sent them:
// serve a dependency from one). The generated model judges each record's
// request against the processor's rule, so a request no grist caller
// sent for a trigger is rejected there: ServeCalls serves a dependency
// only the App calls.
//
// It answers 200 with the response, 422 with {"rejected": name} for a
// declared refusal, 400 for a request without its cause or key, of
// another App, of a processor that does not use the call (Uses), whose
// key is not its effect's (EffectKey: processor/tenant/trigger, as a
// grist caller sends it), or that does not decode (nothing recorded:
// nothing was applied), 404 for a call d does not declare, 405 for
// another method than POST, and 500 for a handler's error that is no
// declared refusal (recorded with it). OverHTTP takes every answer but
// 2xx and a declared refusal for an unknown outcome.
//
// ServeCalls authenticates nobody: the cause is the caller's word, and
// whoever reaches it makes the dependency apply calls, recorded as the
// App's. Serve it where only the App's nodes reach it (Main's
// ServeDependency listens on loopback), or behind the service's own
// authentication (mutual TLS, a token checked in a wrapping handler).
//
// nil h serves d's default double (DefaultHandlers), which panics for a
// call answering a response with fields, naming the fix. It panics too
// for handlers that do not serve each of d's calls (Handlers.Serves).
func ServeCalls(d *Dependency, h Handlers) http.Handler {
	if h == nil {
		h = DefaultHandlers(d)
	}
	if err := h.Serves(d); err != nil {
		panic(fmt.Sprintf("grist.ServeCalls(%s): %v", d.name, err))
	}
	return &callServer{d: d, local: Local(h).(*local)}
}

type callServer struct {
	d     *Dependency
	local *local
}

func (s *callServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call := strings.TrimPrefix(r.URL.Path, "/")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		fail(w, http.StatusMethodNotAllowed, "grist: dependency %s's calls are POST /<call>", s.d.name)
		return
	}
	if _, ok := s.local.by[call]; !ok {
		names := make([]string, 0, len(s.d.calls))
		for _, c := range s.d.calls {
			names = append(names, c.name)
		}
		fail(w, http.StatusNotFound, "grist: dependency %s has no call %q (it has %v)", s.d.name, call, names)
		return
	}
	info, err := decodeCause(r.Header.Get(CauseHeader), r.Header.Get("Idempotency-Key"))
	ci := s.local.by[call].call
	switch {
	case err != nil:
	case info.App != s.d.app.Name:
		err = fmt.Errorf("a call of App %q: dependency %s serves App %s's calls, whose trace its records join", info.App, s.d.name, s.d.app.Name)
	case !slices.ContainsFunc(ci.users, func(p *processorInfo) bool { return p.name == info.Processor }):
		// its record would name a processor no rule makes the call for
		err = fmt.Errorf("a call of processor %q, which does not use call %s (grist.Uses): its users are %v", info.Processor, ci.label(), userNames(ci))
	case info.Key != effectKeyOf(info.Processor, info.Tenant, info.Trigger):
		// a grist caller's key is its effect's: another would record a
		// second application of the trigger under a key of its own
		err = fmt.Errorf("Idempotency-Key %q: a call for processor %s's trigger %d in tenant %s carries its effect's key, %s (grist.EffectKey)", info.Key, info.Processor, info.Trigger, info.Tenant, effectKeyOf(info.Processor, info.Tenant, info.Trigger))
	}
	if err != nil {
		fail(w, http.StatusBadRequest, "grist: dependency %s: %v", s.d.name, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, defaultMaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, "grist: dependency %s: the request's body: %v", s.d.name, err)
		return
	}
	out, err := s.local.Call(r.Context(), info, call, body)
	var rej *Rejected
	var und *undecoded
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(out)
	case errors.As(err, &rej):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: err.Error(), Rejected: rej.Name})
	case errors.As(err, &und):
		fail(w, http.StatusBadRequest, "%v", err)
	default:
		fail(w, http.StatusInternalServerError, "%v", err)
	}
}
