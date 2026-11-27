// Package gzdoc keeps a JSON document in a gzip container, as compiled
// models are kept (gotogether.model/1 and gotogether.mbt/1, DESIGN §113):
// compressed with bytes that depend on the document alone, and read back
// plain or gzipped alike, by the gzip magic.
package gzdoc

import (
	"bytes"
	"compress/gzip"
	"io"
)

// Magic begins every gzip stream; no JSON document begins so.
var Magic = []byte{0x1f, 0x8b} // read-only

// Compress is data in a gzip stream that depends on data alone: the
// header names no file, no modification time and no OS (gzip's
// "unknown"), at the best compression, so a compile twice writes the
// same bytes with one Go toolchain.
func Compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	zw.Header = gzip.Header{OS: 255}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decompress is the document a file's bytes hold: data itself when
// plain, its decompression when gzip's (one stream: a second is no part
// of the document).
func Decompress(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, Magic) {
		return data, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	zr.Multistream(false)
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	return out, zr.Close()
}
