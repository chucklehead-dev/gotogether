package qhttp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"strconv"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// chunkingSize is net/http's bufferBeforeChunkingSize: a response whose
// handler wrote more than this without declaring a Content-Length goes out
// chunked, since the server had to send its head before the body ended.
const chunkingSize = 2048

// responseWriter is the handler's http.ResponseWriter: it buffers the
// response, then writes it as net/http's server would have (wire). Not
// httptest.ResponseRecorder: httptest registers a command-line flag, and
// its result is no wire's.
type responseWriter struct {
	req     *http.Request
	header  http.Header // the handler's, mutable until WriteHeader
	sent    http.Header // as of WriteHeader: later changes are not sent
	code    int         // 0 until WriteHeader
	length  int64       // the declared Content-Length, or -1
	body    bytes.Buffer
	written int64 // bytes the handler wrote, kept or not (HEAD)
	flushed bool  // Flush before the end: the head left without a length
}

func newResponseWriter(req *http.Request) *responseWriter {
	return &responseWriter{req: req, header: http.Header{}, length: -1}
}

// Header implements http.ResponseWriter.
func (w *responseWriter) Header() http.Header { return w.header }

// WriteHeader implements http.ResponseWriter. As net/http's, it panics on
// an invalid code and ignores a second call; an interim (1xx) response is
// dropped, as a client's Transport consumes it without a trace.
func (w *responseWriter) WriteHeader(code int) {
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", code))
	}
	if w.code != 0 || code >= 100 && code <= 199 && code != http.StatusSwitchingProtocols {
		return
	}
	w.code, w.sent = code, w.header.Clone()
	if cl := w.sent.Get("Content-Length"); cl != "" {
		if v, err := strconv.ParseInt(cl, 10, 64); err == nil && v >= 0 {
			w.length = v
		} else {
			w.sent.Del("Content-Length") // net/http drops an invalid one
		}
	}
}

// Write implements http.ResponseWriter.
func (w *responseWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !bodyAllowed(w.code) {
		return 0, http.ErrBodyNotAllowed
	}
	w.written += int64(len(p))
	if w.length >= 0 && w.written > w.length {
		return 0, http.ErrContentLength
	}
	w.body.Write(p) // for HEAD too: wire sends none, but sniffs and measures it
	return len(p), nil
}

// Flush implements http.Flusher: the head leaves now, so a response
// without a declared length is chunked.
func (w *responseWriter) Flush() {
	if w.code == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.flushed = true
}

// wire is the response as net/http's server writes it once the handler
// has returned (its response.finishRequest, chunkWriter.writeHeader): a
// Date from the server's clock, a sniffed Content-Type, a Content-Length
// if the whole body fit before the head left, else chunked.
func (w *responseWriter) wire(ctx context.Context) []byte {
	if w.code == 0 {
		w.WriteHeader(http.StatusOK)
	}
	h, code := w.sent, w.code
	if _, ok := h["Date"]; !ok {
		h.Set("Date", qenv.Now(ctx).UTC().Format(http.TimeFormat))
	}
	te := h.Get("Transfer-Encoding")
	h.Del("Transfer-Encoding")
	h.Del("Content-Length") // written below, from what was declared
	head := w.req.Method == http.MethodHead
	chunked := false
	var length string
	switch {
	case !bodyAllowed(code):
		if code == http.StatusNotModified && w.length >= 0 {
			length = strconv.FormatInt(w.length, 10)
		}
	case w.length >= 0:
		length = strconv.FormatInt(w.length, 10)
	case te == "chunked" || w.flushed || w.written > chunkingSize:
		chunked = !head
	case !head || w.written > 0:
		length = strconv.FormatInt(w.written, 10)
	}
	if _, ok := h["Content-Type"]; !ok && te == "" && bodyAllowed(code) && w.body.Len() > 0 {
		h.Set("Content-Type", http.DetectContentType(w.body.Bytes()))
	}
	var b bytes.Buffer
	text := http.StatusText(code)
	if text == "" {
		text = "status code " + strconv.Itoa(code)
	}
	fmt.Fprintf(&b, "HTTP/1.1 %03d %s\r\n", code, text)
	h.Write(&b) // sorted: the same bytes every run
	if chunked {
		b.WriteString("Transfer-Encoding: chunked\r\n")
	} else if length != "" {
		b.WriteString("Content-Length: " + length + "\r\n")
	}
	b.WriteString("\r\n")
	if head || !bodyAllowed(code) {
		return b.Bytes()
	}
	if chunked {
		cw := httputil.NewChunkedWriter(&b)
		cw.Write(w.body.Bytes())
		cw.Close()
		b.WriteString("\r\n") // no trailers
		return b.Bytes()
	}
	// a body shorter than its declared length is cut off, as the server
	// closing the connection leaves it: the client's read fails with
	// io.ErrUnexpectedEOF
	b.Write(w.body.Bytes())
	return b.Bytes()
}

// bodyAllowed is net/http's bodyAllowedForStatus.
func bodyAllowed(code int) bool {
	switch {
	case code >= 100 && code <= 199, code == http.StatusNoContent, code == http.StatusNotModified:
		return false
	}
	return true
}
