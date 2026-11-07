package qhttp_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
)

type ownedRequestBody struct {
	io.Reader
	closes atomic.Int32
}

func (b *ownedRequestBody) Close() error {
	b.closes.Add(1)
	return nil
}

type failedRequestReader struct{ err error }

func (r failedRequestReader) Read([]byte) (int, error) { return 0, r.err }

// RoundTripper owns request-body cleanup even when it cannot send the request.
// Observe release after the scenario settles, allowing Close to be asynchronous
// rather than imposing an immediate-close requirement at RoundTrip's return.
func TestRoundTripReleasesRequestBodies(t *testing.T) {
	for _, mode := range []string{"success", "hostname with port", "body read error", "unknown host", "unsupported scheme", "outside simulation", "canceled", "expired deadline"} {
		t.Run(mode, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			const payload = "a request whose body has an owner"
			var calls int
			var received string
			srv, tr, _ := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				data, err := io.ReadAll(r.Body)
				if err != nil && mode != "body read error" {
					t.Error(err)
				}
				received = string(data)
				w.WriteHeader(http.StatusNoContent)
			}))
			readErr := errors.New("request source failed")
			body := &ownedRequestBody{Reader: strings.NewReader(payload)}
			if mode == "body read error" {
				body.Reader = failedRequestReader{readErr}
			}
			var gotErr error
			var status int
			request := func(ctx context.Context) {
				if mode == "canceled" {
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					ctx = canceled
				}
				if mode == "expired deadline" {
					deadline, cancel := qhttp.WithTimeout(ctx, time.Second)
					defer cancel()
					qenv.Sleep(ctx, 2*time.Second)
					ctx = deadline
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL(), body)
				if err != nil {
					t.Error(err)
					return
				}
				switch mode {
				case "hostname with port":
					req.URL.Host += ":8080"
				case "unknown host":
					req.URL.Host = "unregistered"
				case "unsupported scheme":
					req.URL.Scheme = "ftp"
				}
				resp, err := tr.RoundTrip(req)
				gotErr = err
				if resp != nil {
					status = resp.StatusCode
					if _, err := io.Copy(io.Discard, resp.Body); err != nil {
						t.Error(err)
					}
					resp.Body.Close()
				}
			}
			if mode == "outside simulation" {
				request(context.Background())
			} else {
				s.Spawn("client", request)
			}
			if err := s.Run(5 * time.Second); err != nil {
				t.Fatal(err)
			}
			if body.closes.Load() == 0 {
				t.Error("transport did not release the request body by scenario completion")
			}
			switch mode {
			case "success", "hostname with port":
				if gotErr != nil || status != http.StatusNoContent || calls != 1 || received != payload {
					t.Fatalf("request result: error=%v status=%d calls=%d body=%q", gotErr, status, calls, received)
				}
			case "body read error":
				if !errors.Is(gotErr, readErr) {
					t.Errorf("read error=%v; want source error", gotErr)
				}
			case "unknown host":
				if dns, ok := errors.AsType[*net.DNSError](gotErr); !ok || !dns.IsNotFound {
					t.Errorf("unknown host error=%v; want DNS not found", gotErr)
				}
			case "unsupported scheme":
				if gotErr == nil || !strings.Contains(gotErr.Error(), "unsupported protocol scheme") {
					t.Errorf("scheme error=%v", gotErr)
				}
			case "outside simulation":
				if gotErr == nil || !strings.Contains(gotErr.Error(), "send it from a task") {
					t.Errorf("outside simulation error=%v", gotErr)
				}
			case "canceled":
				if !errors.Is(gotErr, context.Canceled) {
					t.Errorf("canceled error=%v", gotErr)
				}
			case "expired deadline":
				if !errors.Is(gotErr, context.DeadlineExceeded) {
					t.Errorf("expired deadline error=%v", gotErr)
				}
			}
			if mode != "success" && mode != "hostname with port" && mode != "body read error" && calls != 0 {
				t.Errorf("failed request invoked handler %d times", calls)
			}
		})
	}
}

// Complete application bytes do not imply complete HTTP framing. Missing the
// terminating chunk or its final CRLF must surface as a read error, not success.
func TestChunkedResponsesHaveCompleteFraming(t *testing.T) {
	for _, mode := range []string{"flushed stream", "large buffered response"} {
		t.Run(mode, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			payload := "streamed body"
			if mode == "large buffered response" {
				payload = strings.Repeat("large response ", 250)
			}
			srv, tr, _ := deploy(s, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "flushed stream" {
					w.(http.Flusher).Flush()
				}
				if _, err := io.WriteString(w, payload); err != nil {
					t.Error(err)
				}
			}))
			var data []byte
			var readErr error
			var encoding []string
			s.Spawn("client", func(ctx context.Context) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL(), nil)
				if err != nil {
					t.Error(err)
					return
				}
				resp, err := tr.RoundTrip(req)
				if err != nil {
					t.Error(err)
					return
				}
				defer resp.Body.Close()
				encoding = resp.TransferEncoding
				data, readErr = io.ReadAll(resp.Body)
			})
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
			if len(encoding) != 1 || encoding[0] != "chunked" {
				t.Errorf("response transfer encoding=%v; want chunked", encoding)
			}
			if readErr != nil || string(data) != payload {
				t.Fatalf("chunked read error=%v bytes=%d body matches=%v; want complete %d-byte body", readErr, len(data), string(data) == payload, len(payload))
			}
		})
	}
}
