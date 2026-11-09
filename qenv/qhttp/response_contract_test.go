package qhttp_test

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// These boundaries affect both what the handler can write and the response its
// client sees. Run the same contract against net/http and the simulated wire;
// an implementation matching the other alone is not enough to satisfy it.
func TestResponseLengthAndHeaderCommit(t *testing.T) {
	type writeResult struct {
		n   int
		err error
	}
	for _, tc := range []struct {
		name, method, length, body string
		flush, lateHeader          bool
		write                      bool
		wantN                      int
		wantErr                    error
		wantBody, wantLength       string
		wantContentLength          int64
		wantEncoding               []string
	}{
		{name: "zero rejects bytes", method: "GET", length: "0", body: "x", write: true, wantErr: http.ErrContentLength, wantLength: "0", wantContentLength: 0},
		{name: "zero permits empty write", method: "GET", length: "0", write: true, wantLength: "0", wantContentLength: 0},
		{name: "exact length succeeds", method: "GET", length: "3", body: "abc", write: true, wantN: 3, wantBody: "abc", wantLength: "3", wantContentLength: 3},
		{name: "flush commits implicit head", method: "GET", body: "ok", flush: true, lateHeader: true, write: true, wantN: 2, wantBody: "ok", wantContentLength: -1, wantEncoding: []string{"chunked"}},
		{name: "HEAD preserves declared zero", method: "HEAD", length: "0", flush: true, wantLength: "0", wantContentLength: 0},
		{name: "empty HEAD has no inferred length", method: "HEAD", wantContentLength: -1},
	} {
		for _, profile := range []string{"net-http", "simulated"} {
			t.Run(tc.name+"/"+profile, func(t *testing.T) {
				var wrote writeResult
				h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Phase", "before")
					if tc.length != "" {
						w.Header().Set("Content-Length", tc.length)
					}
					if tc.flush {
						w.(http.Flusher).Flush()
					}
					if tc.lateHeader {
						w.Header().Set("X-Phase", "after")
						w.WriteHeader(http.StatusCreated) // the flushed 200 already committed
					}
					if tc.write {
						wrote.n, wrote.err = io.WriteString(w, tc.body)
					}
				})
				var resp *http.Response
				var got []byte
				read := func(ctx context.Context, client *http.Client, url string) {
					req, err := http.NewRequestWithContext(ctx, tc.method, url, nil)
					if err != nil {
						t.Error(err)
						return
					}
					resp, err = client.Do(req)
					if err != nil {
						t.Error(err)
						return
					}
					defer resp.Body.Close()
					got, err = io.ReadAll(resp.Body)
					if err != nil {
						t.Errorf("response body read: %v", err)
					}
				}
				if profile == "net-http" {
					srv := httptest.NewUnstartedServer(h)
					// The late WriteHeader is an intentional control.
					srv.Config.ErrorLog = log.New(io.Discard, "", 0)
					srv.Start()
					defer srv.Close()
					read(t.Context(), srv.Client(), srv.URL)
					// Close waits for handlers before their write results are read.
					// It also handles a failed request that never reached the handler.
					srv.Close()
				} else {
					s := qenv.New(qenv.Nominal{})
					defer s.Close()
					srv, _, client := deploy(s, false, h)
					s.Spawn("client", func(ctx context.Context) { read(ctx, client, srv.URL()) })
					if err := s.Run(time.Second); err != nil {
						t.Fatal(err)
					}
				}
				if wrote.n != tc.wantN || !errors.Is(wrote.err, tc.wantErr) {
					t.Errorf("handler write=(%d,%v), want (%d,%v)", wrote.n, wrote.err, tc.wantN, tc.wantErr)
				}
				if resp == nil {
					t.Fatal("no response")
				}
				if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Phase") != "before" {
					t.Errorf("committed head: status=%d phase=%q, want 200/before", resp.StatusCode, resp.Header.Get("X-Phase"))
				}
				if string(got) != tc.wantBody || resp.Header.Get("Content-Length") != tc.wantLength || resp.ContentLength != tc.wantContentLength || !slices.Equal(resp.TransferEncoding, tc.wantEncoding) {
					t.Errorf("response body=%q header length=%q content length=%d encoding=%v, want %q/%q/%d/%v", got, resp.Header.Get("Content-Length"), resp.ContentLength, resp.TransferEncoding, tc.wantBody, tc.wantLength, tc.wantContentLength, tc.wantEncoding)
				}
			})
		}
	}
}
