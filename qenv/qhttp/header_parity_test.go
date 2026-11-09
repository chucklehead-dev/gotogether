package qhttp_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Header rejection and suppression affect framing even when the handler wrote
// only a small body. Check the public client response against the same contract
// on the actual net/http server and its simulated counterpart.
func TestResponseSuppressedAndInvalidLength(t *testing.T) {
	for _, tc := range []struct {
		name, method, length, body string
		encoding                   string
		status                     int
		badLength                  bool
		wantLength                 string
		wantContentLength          int64
		wantEncoding               []string
	}{
		{name: "304 suppresses representation headers", method: "GET", status: 304, length: "5", wantContentLength: 0},
		{name: "304 suppresses declared zero", method: "GET", status: 304, length: "0", encoding: "chunked", wantContentLength: 0},
		{name: "invalid length body is chunked", method: "GET", status: 200, length: "invalid", body: "abc", wantContentLength: -1, wantEncoding: []string{"chunked"}},
		{name: "negative length body is chunked", method: "GET", status: 200, length: "-1", body: "abc", wantContentLength: -1, wantEncoding: []string{"chunked"}},
		{name: "invalid empty body is chunked", method: "GET", status: 200, length: "invalid", wantContentLength: -1, wantEncoding: []string{"chunked"}},
		{name: "HEAD invalid length is rejected", method: "HEAD", status: 200, length: "invalid", body: "abc", badLength: true},
		{name: "HEAD invalid empty body is rejected", method: "HEAD", status: 200, length: "invalid", badLength: true},
		{name: "valid length stays explicit", method: "GET", status: 200, length: "3", body: "abc", wantLength: "3", wantContentLength: 3},
		{name: "HEAD valid length stays explicit", method: "HEAD", status: 200, length: "3", body: "abc", wantLength: "3", wantContentLength: 3},
		{name: "leading zero length is preserved", method: "GET", status: 200, length: "003", body: "abc", wantLength: "003", wantContentLength: 3},
		{name: "signed length is not repaired", method: "GET", status: 200, length: "+3", body: "abc", badLength: true},
		{name: "signed zero is not repaired", method: "GET", status: 200, length: "-0", badLength: true},
	} {
		for _, profile := range []string{"net-http", "simulated"} {
			t.Run(tc.name+"/"+profile, func(t *testing.T) {
				h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Length", tc.length)
					if tc.status == http.StatusNotModified {
						w.Header().Set("Content-Type", "application/example")
					}
					if tc.encoding != "" {
						w.Header().Set("Transfer-Encoding", tc.encoding)
					}
					w.WriteHeader(tc.status)
					if tc.status == http.StatusOK {
						if n, err := io.WriteString(w, tc.body); n != len(tc.body) || err != nil {
							t.Errorf("handler write=(%d,%v), want (%d,nil)", n, err, len(tc.body))
						}
					}
				})
				read := func(ctx context.Context, client *http.Client, url string) {
					req, err := http.NewRequestWithContext(ctx, tc.method, url, nil)
					if err != nil {
						t.Error(err)
						return
					}
					resp, err := client.Do(req)
					if tc.badLength {
						if err == nil || !strings.Contains(err.Error(), "bad Content-Length") {
							t.Errorf("request error %v, want invalid Content-Length rejection", err)
						}
						if resp != nil {
							resp.Body.Close()
						}
						return
					}
					if err != nil {
						t.Error(err)
						return
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					wantBody := tc.body
					if tc.method == http.MethodHead {
						wantBody = ""
					}
					if err != nil || string(body) != wantBody {
						t.Errorf("body=%q err=%v, want %q/nil", body, err, wantBody)
					}
					if resp.StatusCode != tc.status || resp.Header.Get("Content-Length") != tc.wantLength || resp.ContentLength != tc.wantContentLength || !slices.Equal(resp.TransferEncoding, tc.wantEncoding) {
						t.Errorf("response status=%d header length=%q content length=%d encoding=%v, want %d/%q/%d/%v", resp.StatusCode, resp.Header.Get("Content-Length"), resp.ContentLength, resp.TransferEncoding, tc.status, tc.wantLength, tc.wantContentLength, tc.wantEncoding)
					}
					if tc.status == http.StatusNotModified && resp.Header.Get("Content-Type") != "" {
						t.Errorf("304 retained Content-Type %q", resp.Header.Get("Content-Type"))
					}
				}
				if profile == "net-http" {
					done := make(chan struct{})
					srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						defer close(done)
						h.ServeHTTP(w, r)
					}))
					srv.Config.ErrorLog = log.New(io.Discard, "", 0) // invalid headers are intentional controls
					srv.Start()
					defer srv.Close()
					read(t.Context(), srv.Client(), srv.URL)
					<-done
				} else {
					s := qenv.New(qenv.Nominal{})
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
