package input

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// The fingerprint identifies the original byte image, not reconstructed steps
// or a later read of its path. Whitespace and torn bytes are evidence too.
func TestReadCapturesExactBytes(t *testing.T) {
	t.Parallel()
	requests := []byte(`{"protocol":"gotogether.requests/1","recorder":"ui"}
{"event":"request","req":"1","action":"deposit","time":"2026-10-27T09:00:00Z","args":{"amount":5}}
{"event":"response","req":"1","time":"2026-10-27T09:00:01Z","code":200,"args":{"balance":5}}
`)
	for _, c := range []struct {
		name   string
		data   []byte
		format Format
	}{
		{"steps", []byte(StepLogExample + "\n"), StepLog},
		{"torn-steps", []byte(StepLogExample[:len(StepLogExample)/2]), StepLog},
		{"requests", requests, Requests},
		{"otlp", []byte("{\"resourceSpans\":[]}\n"), OTLP},
		{"itf", []byte(`{"vars":["x"],"states":[{"x":{"#bigint":"5"}}]}`), ITF},
		{"empty", []byte(" \n"), Unknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "capture")
			if err := os.WriteFile(path, c.data, 0644); err != nil {
				t.Fatal(err)
			}
			in, err := Read(path, Unknown)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(c.data)
			want := hex.EncodeToString(digest[:])
			if in.Format != c.format || in.SHA256() != want {
				t.Fatalf("format=%q hash=%q want %q/%q", in.Format, in.SHA256(), c.format, want)
			}
			if err := os.WriteFile(path, []byte("changed after reading"), 0644); err != nil {
				t.Fatal(err)
			}
			if in.SHA256() != want {
				t.Fatal("fingerprint reread mutable input path")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if in.SHA256() != want {
				t.Fatal("fingerprint depends on continued file availability")
			}
		})
	}
	if (&File{Path: "not-read"}).SHA256() != "" {
		t.Fatal("manually assembled File claims a captured fingerprint")
	}
}

// Type inference may require a new typed read, but that read still hashes the
// same bytes it decodes. It must not decode first and hash the path afterward.
func TestReadITFCapturesTypedBytes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "large.itf.json")
	data := []byte(`{"vars":["x"],"states":[{"x":{"s":1,"e":18,"c":[46116,86018427387904]}}]}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, Unknown); !errors.As(err, new(*itf.AmbiguousError)) {
		t.Fatalf("untyped read should be ambiguous: %v", err)
	}
	typed, err := ReadITF(path, &itf.Schema{Vars: map[string]*itf.Type{"x": {Kind: itf.Int}}})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	want := hex.EncodeToString(digest[:])
	if typed.Format != ITF || typed.SHA256() != want || typed.ITF.States[0].Vars["x"].Kind != itf.Int {
		t.Fatalf("typed snapshot: %+v %q", typed, typed.SHA256())
	}
	if err := os.WriteFile(path, []byte(`{"vars":["x"],"states":[{"x":0}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if typed.SHA256() != want || typed.ITF.States[0].Vars["x"].Kind != itf.Int {
		t.Fatal("typed capture changed with its path")
	}
}
