package qhttp_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// The Transport records a request as its effect, at the send: method,
// path, query, body and each header's first value (the idempotency key's
// among them), once, the Service under it recording nothing of its own.
func TestTransportRecordsEffects(t *testing.T) {
	t.Parallel()
	sink := &qobs.MemorySink{}
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(t.Context(), qobs.NewRecorder(sink))))
	_, _, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	run(t, s, func(ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://api/captures?dry=1", strings.NewReader(`{"amount":10}`))
		req.Header.Set("Idempotency-Key", "cap:o1")
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("request: %v", err)
			return
		}
		resp.Body.Close()
	})
	var effects []qtrace.Step
	for _, r := range sink.Steps() {
		if r.Effect != "" {
			effects = append(effects, r)
		}
	}
	if len(effects) != 1 {
		t.Fatalf("%d effect records, want the request's alone: %v", len(effects), effects)
	}
	e := effects[0]
	want := map[string]any{"method": "POST", "path": "/captures", "query": "dry=1", "body": `{"amount":10}`, "header.Idempotency-Key": "cap:o1"}
	if e.Effect != "http.api.request" {
		t.Errorf("kind %s", e.Effect)
	}
	for k, v := range want {
		if e.Args[k] != v {
			t.Errorf("field %s: %v, want %v (all: %v)", k, e.Args[k], v, e.Args)
		}
	}
}
