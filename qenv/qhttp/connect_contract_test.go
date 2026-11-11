package qhttp_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Exercise ordinary CONNECT requests handled without Hijack or a tunnel. As
// net/http does, an unknown-length CONNECT does not invent chunked framing;
// explicit length or chunking remains meaningful. Close bounds unframed bytes
// to this request rather than leaving a real server waiting for another one.
func TestConnectRequestFraming(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		length       int64
		encoding     []string
		wantBody     string
		wantLength   int64
		wantEncoding []string
	}{
		{name: "empty unknown", length: 0},
		{name: "nonempty zero length", body: "abc", length: 0},
		{name: "nonempty negative length", body: "abc", length: -1},
		{name: "declared length", body: "abc", length: 3, wantBody: "abc", wantLength: 3},
		{name: "explicit chunking", body: "abc", encoding: []string{"chunked"}, wantBody: "abc", wantLength: -1, wantEncoding: []string{"chunked"}},
	} {
		for _, profile := range []string{"net-http", "simulated"} {
			t.Run(tc.name+"/"+profile, func(t *testing.T) {
				type observed struct {
					body     string
					length   int64
					encoding []string
					err      error
				}
				seen := make(chan observed, 1)
				h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					seen <- observed{string(body), r.ContentLength, r.TransferEncoding, err}
					w.WriteHeader(http.StatusNoContent)
				})
				request := func(ctx context.Context, client *http.Client, url string) {
					req, err := http.NewRequestWithContext(ctx, http.MethodConnect, url, io.NopCloser(strings.NewReader(tc.body)))
					if err != nil {
						t.Error(err)
						return
					}
					req.ContentLength, req.TransferEncoding, req.Close = tc.length, tc.encoding, true
					resp, err := client.Do(req)
					if err != nil {
						t.Error(err)
						return
					}
					defer resp.Body.Close()
					if resp.StatusCode != http.StatusNoContent {
						t.Errorf("response status=%d, want 204", resp.StatusCode)
					}
				}
				if profile == "net-http" {
					srv := httptest.NewServer(h)
					defer srv.Close()
					request(t.Context(), srv.Client(), srv.URL)
					srv.Close() // join the real handler before checking its observation
				} else {
					s := qenv.New(qenv.Nominal{})
					s.Check = true
					defer s.Close()
					srv, _, client := deploy(s, false, h)
					s.Spawn("client", func(ctx context.Context) { request(ctx, client, srv.URL()) })
					if err := s.Run(time.Second); err != nil {
						t.Fatal(err)
					}
				}
				if len(seen) != 1 {
					t.Fatal("handler did not observe exactly one CONNECT request")
				}
				got := <-seen
				if got.err != nil || got.body != tc.wantBody || got.length != tc.wantLength || !slices.Equal(got.encoding, tc.wantEncoding) {
					t.Errorf("CONNECT body=%q read error=%v length=%d encoding=%v; want %q/nil/%d/%v", got.body, got.err, got.length, got.encoding, tc.wantBody, tc.wantLength, tc.wantEncoding)
				}
			})
		}
	}
}
