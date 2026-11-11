package qhttp_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
)

// WithTimeout maps the context's bound exactly into virtual time. Only a
// strictly shorter context bound changes the timeout's provenance; an equal
// bound leaves the configured transport limit responsible for ending the wait.
func TestTimeoutProvenanceUsesStrictShortening(t *testing.T) {
	for _, tc := range []struct {
		name         string
		deadline     time.Duration
		wantAfter    time.Duration
		wantDeadline bool
	}{
		{"shorter context", 500 * time.Millisecond, 500 * time.Millisecond, true},
		{"equal context", time.Second, time.Second, false},
		{"longer context", 2 * time.Second, time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			s.Check = true
			defer s.Close()
			calls := 0
			srv, tr, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				qenv.Sleep(r.Context(), 3*time.Second)
				w.WriteHeader(http.StatusNoContent)
			}))
			tr.Timeout = time.Second
			s.Spawn("client", func(ctx context.Context) {
				ctx, cancel := qhttp.WithTimeout(ctx, tc.deadline)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL(), nil)
				if err != nil {
					t.Error(err)
					return
				}
				start := s.Elapsed()
				resp, err := client.Do(req)
				if resp != nil {
					resp.Body.Close()
					t.Errorf("received a response instead of a timeout: %d", resp.StatusCode)
				}
				te, ok := errors.AsType[*qhttp.TimeoutError](err)
				if !ok {
					t.Errorf("request error=%v, want TimeoutError", err)
					return
				}
				elapsed := s.Elapsed() - start
				if te.After != tc.wantAfter || elapsed != tc.wantAfter || te.Deadline != tc.wantDeadline || !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("timeout after=%v elapsed=%v deadline=%v error=%v; want %v/%v/%v", te.After, elapsed, te.Deadline, err, tc.wantAfter, tc.wantAfter, tc.wantDeadline)
				}
			})
			if err := s.Run(4 * time.Second); err != nil {
				t.Fatal(err)
			}
			// Timeout describes the wait, not whether the request was processed:
			// this handler received it even though its answer arrived too late.
			if calls != 1 {
				t.Errorf("handler ran %d times, want one delivered request", calls)
			}
		})
	}
}
