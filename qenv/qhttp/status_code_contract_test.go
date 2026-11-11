package qhttp_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Match the public net/http boundary: informational statuses do not commit the
// final response, 999 is a valid final status, and codes outside 100..999 panic.
// Protocol upgrade 101 is outside the simulated transport's contract.
func TestResponseStatusCodeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		code  int
		panic bool
		final int
	}{
		{100, false, http.StatusCreated},
		{199, false, http.StatusCreated},
		{999, false, 999},
		{99, true, http.StatusCreated},
		{1000, true, http.StatusCreated},
	} {
		for _, profile := range []string{"net-http", "simulated"} {
			t.Run(fmt.Sprintf("%d/%s", tc.code, profile), func(t *testing.T) {
				h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("X-Phase", "before")
					panicked := statusWriteHeaderPanicked(w, tc.code)
					if panicked != tc.panic {
						t.Errorf("WriteHeader(%d) panicked=%v, want %v", tc.code, panicked, tc.panic)
					}
					w.Header().Set("X-Phase", "final")
					if panicked && !tc.panic {
						// Keep an unexpected public-API panic an explicit assertion
						// failure rather than an escaped handler panic.
						w.WriteHeader(http.StatusCreated)
					} else if tc.code != 999 {
						w.WriteHeader(tc.final)
					}
					if _, err := io.WriteString(w, "final body"); err != nil {
						t.Errorf("write final body: %v", err)
					}
				})
				read := func(ctx context.Context, client *http.Client, url string) {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
					if err != nil {
						t.Error(err)
						return
					}
					resp, err := client.Do(req)
					if err != nil {
						t.Error(err)
						return
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					wantPhase := "final"
					if tc.code == 999 {
						wantPhase = "before" // the first final WriteHeader committed it
					}
					if err != nil || resp.StatusCode != tc.final || string(body) != "final body" || resp.Header.Get("X-Phase") != wantPhase {
						t.Errorf("response status=%d body=%q phase=%q err=%v, want %d/final body/%s/nil", resp.StatusCode, body, resp.Header.Get("X-Phase"), err, tc.final, wantPhase)
					}
				}
				if profile == "net-http" {
					srv := httptest.NewServer(h)
					defer srv.Close()
					read(t.Context(), srv.Client(), srv.URL)
				} else {
					s := qenv.New(qenv.Nominal{})
					s.Check = true
					defer s.Close()
					srv, _, client := deploy(s, false, h)
					s.Spawn("client", func(ctx context.Context) { read(ctx, client, srv.URL()) })
					if err := s.Run(time.Second); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func statusWriteHeaderPanicked(w http.ResponseWriter, code int) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	w.WriteHeader(code)
	return false
}
