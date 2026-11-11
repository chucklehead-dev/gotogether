package qhttp_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
)

// Server.Service exposes the caller-NUL-wire envelope. A malformed wire must
// receive HTTP's client error without invoking the application's handler.
func TestServerRejectsMalformedWire(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		status     int
		calls      int
	}{
		{"valid", "GET / HTTP/1.1\r\nHost: api\r\n\r\n", http.StatusCreated, 1},
		{"invalid request line", "not an HTTP request\r\n\r\n", http.StatusBadRequest, 0},
		{"invalid header", "GET / HTTP/1.1\r\nHost: api\r\ninvalid header\r\n\r\n", http.StatusBadRequest, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			s.Check = true
			defer s.Close()
			calls := 0
			srv, _, _ := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(http.StatusCreated)
				io.WriteString(w, "served")
			}))
			s.Spawn("client", func(ctx context.Context) {
				wire, err := srv.Service.Call(ctx, []byte("client:0\x00"+tc.wire), time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL(), nil)
				if err != nil {
					t.Error(err)
					return
				}
				resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(wire)), req)
				if err != nil {
					t.Error(err)
					return
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil || resp.StatusCode != tc.status {
					t.Errorf("wire response status=%d body=%q read error=%v, want %d/nil", resp.StatusCode, body, err, tc.status)
				}
				if tc.status == http.StatusBadRequest && !strings.Contains(string(body), "400 Bad Request") {
					t.Errorf("malformed request response lacks explanation: %q", body)
				}
				if tc.status == http.StatusCreated && string(body) != "served" {
					t.Errorf("valid request body=%q, want served", body)
				}
			})
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
			if calls != tc.calls {
				t.Errorf("application handler ran %d times, want %d", calls, tc.calls)
			}
		})
	}
}

// dialStartupSchedule picks the first queued client, then the last queued
// bootstrap task at the dial seam; all later choices and faults are nominal.
// The fixture has exactly those two tasks until the bootstrap runs.
type dialStartupSchedule struct{ turns int }

func (c *dialStartupSchedule) Choose(kind string, n int) int {
	if kind == "sched.task" {
		c.turns++
		if c.turns == 2 {
			return n - 1
		}
	}
	return 0
}

// A dial is a scheduling seam before the listening check. Force startup
// during that dial; a server still sleeping at the check genuinely refuses.
func TestDialAllowsServerStartup(t *testing.T) {
	for _, delay := range []time.Duration{0, time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			schedule := &dialStartupSchedule{}
			s := qenv.New(schedule)
			s.Check = true
			defer s.Close()
			var srv *qhttp.Server
			var client *http.Client
			booted, calls := false, 0
			s.Spawn("client", func(ctx context.Context) {
				if booted {
					t.Error("fixture did not start the client before server bootstrap")
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL(), nil)
				if err != nil {
					t.Error(err)
					return
				}
				resp, err := client.Do(req)
				if delay > 0 {
					if !errors.Is(err, syscall.ECONNREFUSED) || resp != nil || booted {
						t.Errorf("unready server response=%v error=%v booted=%v, want refusal before startup", resp, err, booted)
					}
					if resp != nil {
						resp.Body.Close()
					}
					return
				}
				if err != nil {
					t.Errorf("dial failed despite ready startup task: %v", err)
					return
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil || resp.StatusCode != http.StatusOK || string(body) != "ready" || !booted {
					t.Errorf("startup response status=%d body=%q error=%v booted=%v", resp.StatusCode, body, err, booted)
				}
			})
			p := s.Process("api", false, func(ctx context.Context) {
				if delay > 0 {
					qenv.Sleep(ctx, delay)
				}
				srv.Handle(ctx, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					io.WriteString(w, "ready")
				}))
				booted = true
			})
			srv = qhttp.NewServer(s, "api", p)
			client = &http.Client{Transport: qhttp.NewTransport(srv)}
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if delay > 0 {
				wantCalls = 0
			}
			if !booted || calls != wantCalls {
				t.Errorf("server booted=%v calls=%d, want true/%d", booted, calls, wantCalls)
			}
		})
	}
}
