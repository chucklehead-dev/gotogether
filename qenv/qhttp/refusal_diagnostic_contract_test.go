package qhttp_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
)

// Refusals before Handle are real no-listener outcomes. Their report note
// explains an otherwise step-less client, counts retries, and retains the
// first request's detail. A subsequent successful request is not a refusal.
func TestNoListenerRefusalsHaveCountedDiagnostics(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		name := "without recorder"
		if recorded {
			name = "with recorder"
		}
		t.Run(name, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			s.Check = true
			defer s.Close()
			notes := &hooks.Notes{}
			calls, refused := 0, 0
			ready, finished := false, false
			var srv *qhttp.Server
			p := s.Process("api", false, func(ctx context.Context) {
				qenv.Sleep(ctx, time.Second)
				srv.Handle(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.WriteHeader(http.StatusNoContent)
				}))
				ready = true
			})
			srv = qhttp.NewServer(s, "api", p)
			client := &http.Client{Transport: qhttp.NewTransport(srv)}
			s.Spawn("client", func(ctx context.Context) {
				if recorded {
					ctx = hooks.WithNotes(ctx, notes)
				} else if hooks.NotesOf(ctx) != nil {
					t.Error("no-recorder fixture unexpectedly inherited Notes")
					return
				}
				request := func(path string) (*http.Response, error) {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL()+path, nil)
					if err != nil {
						t.Error(err)
						return nil, err
					}
					return client.Do(req)
				}
				for _, path := range []string{"/first", "/second"} {
					qenv.Sleep(ctx, 100*time.Millisecond)
					resp, err := request(path)
					if resp != nil {
						resp.Body.Close()
					}
					if ready || resp != nil || !errors.Is(err, syscall.ECONNREFUSED) {
						t.Errorf("before Handle path=%s ready=%t response=%v error=%v; want ECONNREFUSED", path, ready, resp, err)
						return
					}
					refused++
				}
				var want []string
				if recorded {
					want = []string{"a request to api refused, nothing listening (2 times; the first: GET /first at 100ms: its process had not called Handle yet: start the server's process before its clients, or have them retry a refused connection)"}
				}
				if got := notes.Lines(); !slices.Equal(got, want) {
					t.Errorf("refusal diagnostics=%q, want %q", got, want)
				}
				qenv.Sleep(ctx, time.Second)
				resp, err := request("/ready")
				if err != nil {
					t.Errorf("ready request: %v", err)
					return
				}
				if resp == nil {
					t.Error("ready request returned no response")
					return
				}
				resp.Body.Close()
				if !ready || resp.StatusCode != http.StatusNoContent {
					t.Errorf("ready=%t status=%d; want ready with204", ready, resp.StatusCode)
				}
				if got := notes.Lines(); !slices.Equal(got, want) {
					t.Errorf("successful request changed refusal diagnostics: %q, want %q", got, want)
				}
				finished = true
			})
			if err := s.Run(3 * time.Second); err != nil {
				t.Fatal(err)
			}
			if !finished || !ready || refused != 2 || calls != 1 {
				t.Fatalf("scenario incomplete or wrong delivery: finished=%t ready=%t refusals=%d handler calls=%d; want true,true,2,1", finished, ready, refused, calls)
			}
		})
	}
}
