// Package harness runs payd, unmodified, in a qenv simulation (sim.go:
// database/sql over qsql, net/http over qhttp, a processor double) and for
// real (real.go: SQLite, httptest), and records the model's steps around
// it: the clients' requests and responses at a RoundTripper around the
// SDK's transport, the charges at the database's commit, the captures in
// the processor double. payd itself records nothing.
package harness

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"

	"github.com/chucklehead-dev/gotogether/qobs"
)

// Recording is an http.RoundTripper that records each charge's attempt
// as two steps, request (client, key, account, amount) before it is sent
// and response (client, key, status, id) after: status 0 and id
// "unknown" when no response came (a timeout, a reset, a refusal), the
// charge's id for a 2xx. Other requests pass through unrecorded.
type Recording struct {
	Base   http.RoundTripper
	Client string
}

// RoundTrip implements http.RoundTripper.
func (rt *Recording) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.URL.Path != "/v1/charges" {
		return rt.Base.RoundTrip(req)
	}
	ctx := req.Context()
	var body []byte
	if req.Body != nil {
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	var ask struct {
		Account string `json:"account"`
		Amount  int64  `json:"amount"`
	}
	json.Unmarshal(body, &ask)
	key := req.Header.Get("Idempotency-Key")
	qobs.Record(ctx, "request", nil, "client", rt.Client, "key", key, "account", ask.Account, "amount", ask.Amount)
	resp, err := rt.Base.RoundTrip(req)
	status, id := 0, "unknown"
	if err == nil {
		b, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(b))
		if rerr == nil {
			status = resp.StatusCode
			var ch struct {
				ID string `json:"id"`
			}
			if status/100 == 2 && json.Unmarshal(b, &ch) == nil {
				id = ch.ID
			}
		}
	}
	qobs.Record(ctx, "response", nil, "client", rt.Client, "key", key, "status", status, "id", id)
	return resp, err
}

// sortedKeys are m's keys in order: a run's setup must not depend on a
// map's.
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
