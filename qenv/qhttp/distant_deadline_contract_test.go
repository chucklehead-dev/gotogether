package qhttp_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
)

// A valid distant context must not hang while its remaining duration is
// quantized. The much shorter transport bound still limits response waiting.
func TestDistantContextDeadlineKeepsTransportBound(t *testing.T) {
	for _, year := range []int{2200, 2500} {
		t.Run(strconv.Itoa(year), func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			s.Check = true
			defer s.Close()
			srv, tr, client := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				qenv.Sleep(r.Context(), 2*time.Second)
				w.WriteHeader(http.StatusNoContent)
			}))
			tr.Timeout = time.Second
			completed := false
			s.Spawn("client", func(ctx context.Context) {
				ctx, cancel := context.WithDeadline(ctx, time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC))
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
					t.Errorf("received response %d instead of transport timeout", resp.StatusCode)
				}
				te, ok := errors.AsType[*qhttp.TimeoutError](err)
				if !ok || te.After != time.Second || te.Deadline || s.Elapsed()-start != time.Second {
					t.Errorf("distant-deadline error=%v elapsed=%v; want transport timeout after 1s", err, s.Elapsed()-start)
				}
				completed = true
			})
			if err := s.Run(3 * time.Second); err != nil {
				t.Fatal(err)
			}
			if !completed {
				t.Fatal("client did not finish within the simulation horizon")
			}
		})
	}
}
