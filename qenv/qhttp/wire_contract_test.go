package qhttp_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestResponseBufferBoundaryAndStatusText(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		code, size   int
		chunked      bool
	}{
		{"below buffer", "200 OK", 200, 2047, false},
		{"exact buffer", "200 OK", 200, 2048, false},
		{"above buffer", "200 OK", 200, 2049, true},
		{"custom status", "299 status code 299", 299, 3, false},
	} {
		for _, profile := range []string{"net-http", "simulated"} {
			t.Run(tc.name+"/"+profile, func(t *testing.T) {
				body := strings.Repeat("x", tc.size)
				h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.code)
					if n, err := io.WriteString(w, body); n != len(body) || err != nil {
						t.Errorf("handler write=(%d,%v), want (%d,nil)", n, err, len(body))
					}
				})
				withHTTPContract(t, profile, h, func(ctx context.Context, client *http.Client, url string) {
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
					got, err := io.ReadAll(resp.Body)
					if err != nil || string(got) != body || resp.Status != tc.status {
						t.Errorf("response status=%q body bytes=%d read error=%v, want %q/%d/nil", resp.Status, len(got), err, tc.status, len(body))
					}
					wantLength, wantHeader := int64(tc.size), strconv.Itoa(tc.size)
					var wantEncoding []string
					if tc.chunked {
						wantLength, wantHeader, wantEncoding = -1, "", []string{"chunked"}
					}
					if resp.ContentLength != wantLength || resp.Header.Get("Content-Length") != wantHeader || !slices.Equal(resp.TransferEncoding, wantEncoding) {
						t.Errorf("response framing: length=%d header=%q encoding=%v; want %d/%q/%v", resp.ContentLength, resp.Header.Get("Content-Length"), resp.TransferEncoding, wantLength, wantHeader, wantEncoding)
					}
				})
			})
		}
	}
}

func TestInvalidResponseLengthIsRemovedFromHandlerHeader(t *testing.T) {
	for _, length := range []string{"invalid", "-1"} {
		for _, profile := range []string{"net-http", "simulated"} {
			t.Run(length+"/"+profile, func(t *testing.T) {
				h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Length", length)
					w.WriteHeader(http.StatusOK)
					if got := w.Header().Get("Content-Length"); got != "" {
						t.Errorf("handler header retained invalid Content-Length %q after WriteHeader", got)
					}
					if _, err := io.WriteString(w, "abc"); err != nil {
						t.Error(err)
					}
				})
				withHTTPContract(t, profile, h, func(ctx context.Context, client *http.Client, url string) {
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
					got, err := io.ReadAll(resp.Body)
					if err != nil || string(got) != "abc" || resp.ContentLength != -1 || resp.Header.Get("Content-Length") != "" || !slices.Equal(resp.TransferEncoding, []string{"chunked"}) {
						t.Errorf("invalid-length response body=%q read error=%v length=%d header=%q encoding=%v", got, err, resp.ContentLength, resp.Header.Get("Content-Length"), resp.TransferEncoding)
					}
				})
			})
		}
	}
}

func TestRequestDeclaredAndUnknownLength(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		length       int64
		wantLength   int64
		wantEncoding []string
	}{
		{"declared POST", http.MethodPost, 3, 3, nil},
		{"unknown POST", http.MethodPost, 0, -1, []string{"chunked"}},
		{"unknown GET", http.MethodGet, 0, -1, []string{"chunked"}},
	} {
		for _, profile := range []string{"net-http", "simulated"} {
			t.Run(tc.name+"/"+profile, func(t *testing.T) {
				h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != "abc" || r.ContentLength != tc.wantLength || !slices.Equal(r.TransferEncoding, tc.wantEncoding) {
						t.Errorf("request body=%q error=%v length=%d encoding=%v; want abc/nil/%d/%v", body, err, r.ContentLength, r.TransferEncoding, tc.wantLength, tc.wantEncoding)
					}
					w.WriteHeader(http.StatusNoContent)
				})
				withHTTPContract(t, profile, h, func(ctx context.Context, client *http.Client, url string) {
					req, err := http.NewRequestWithContext(ctx, tc.method, url, io.NopCloser(strings.NewReader("abc")))
					if err != nil {
						t.Error(err)
						return
					}
					req.ContentLength = tc.length
					resp, err := client.Do(req)
					if err != nil {
						t.Error(err)
						return
					}
					resp.Body.Close()
					if resp.StatusCode != http.StatusNoContent {
						t.Errorf("response status=%d, want %d", resp.StatusCode, http.StatusNoContent)
					}
				})
			})
		}
	}
}

func withHTTPContract(t *testing.T, profile string, h http.Handler, request func(context.Context, *http.Client, string)) {
	t.Helper()
	if profile == "net-http" {
		srv := httptest.NewUnstartedServer(h)
		srv.Config.ErrorLog = log.New(io.Discard, "", 0) // invalid lengths are intentional controls
		srv.Start()
		defer srv.Close()
		request(t.Context(), srv.Client(), srv.URL)
		return
	}
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	srv, _, client := deploy(s, false, h)
	s.Spawn("client", func(ctx context.Context) { request(ctx, client, srv.URL()) })
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
}
