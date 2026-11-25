package validate

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// gzipCopy compiles testdata's account binding into a directory of the
// test's, writes it gzipped where CompiledFor keeps it, and returns the
// binding's path, the artifact's, and the model compiled.
func gzipCopy(t *testing.T) (string, string, *CompiledModel) {
	t.Helper()
	bp, plain := compiledCopy(t, "account", "account.qnt")
	cm, err := LoadCompiled(plain)
	if err != nil {
		t.Fatal(err)
	}
	cp := CompiledFor(bp)
	if !strings.HasSuffix(cp, CompiledExt) {
		t.Fatalf("CompiledFor(%s) = %s, want the gzipped convention %s", bp, cp, CompiledExt)
	}
	if err := cm.Write(cp); err != nil {
		t.Fatal(err)
	}
	return bp, cp, cm
}

// gunzipFile is the JSON document a gzipped artifact holds, by
// compress/gzip directly (not the loader under test), with its header.
func gunzipFile(t *testing.T, path string) ([]byte, gzip.Header) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("%s is no gzip stream: %v", path, err)
	}
	doc, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return doc, zr.Header
}

// A compiled model written gzipped (CompiledExt) holds the same document
// Write writes plain, byte for byte; LoadCompiled reads both, the two
// the same model; and the gzipped one judges runs as the plain one does.
func TestCompiledGzipRoundTrip(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	bp, cp, cm := gzipCopy(t)
	plain := filepath.Join(t.TempDir(), "account.model.json")
	if err := cm.Write(plain); err != nil {
		t.Fatal(err)
	}
	pb, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(pb, gzipMagic) {
		t.Fatalf("%s, named without .gz, was written gzipped", plain)
	}
	doc, _ := gunzipFile(t, cp)
	if !bytes.Equal(doc, pb) {
		t.Fatalf("the gzipped artifact holds %d bytes, the plain one is %d: not the same document", len(doc), len(pb))
	}
	if fi, err := os.Stat(cp); err != nil || fi.Size() >= int64(len(pb)) {
		t.Fatalf("the gzipped artifact (%v, %v) is not smaller than the plain one (%d bytes)", fi.Size(), err, len(pb))
	}
	gz, err := LoadCompiled(cp)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := LoadCompiled(plain)
	if err != nil {
		t.Fatal(err)
	}
	if d := gz.Differs(pl); len(d) > 0 {
		t.Fatalf("the gzipped and plain artifacts differ in %v", d)
	}
	if gz.digest == pl.digest {
		t.Errorf("the digest is of the file as kept: the gzipped and plain files share one (%s)", gz.digest)
	}
	ch, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		ok   bool
		ops  []any
	}{
		{"conforms", true, []any{"deposit", 50, 50, "withdraw", 20, 30}},
		{"wrong balance", false, []any{"deposit", 10, 10, "deposit", 5, 16}},
	} {
		rep, err := ch.Check(context.Background(), compiledSteps(c.ops...))
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK() != c.ok {
			t.Errorf("%s: OK %v by the gzipped artifact, want %v:\n%s", c.name, rep.OK(), c.ok, rep.Summary())
		}
	}
}

// Compiling twice writes the same gzipped bytes: the header carries no
// modification time, file name or OS, so compile -check and the digests
// in manifests do not depend on when or where it was compiled.
func TestCompiledGzipDeterministic(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	bp, first, _ := gzipCopy(t)
	cm, err := Compile(bp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// the binding's path is relative to the artifact's directory: the
	// second compile is written beside the first, to compare the bytes
	second := filepath.Join(filepath.Dir(first), "again"+CompiledExt)
	if err := cm.Write(second); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("two compiles of one binding wrote different gzipped bytes (%d, %d)", len(a), len(b))
	}
	_, h := gunzipFile(t, second)
	if !h.ModTime.IsZero() || h.Name != "" || h.Comment != "" || len(h.Extra) > 0 || h.OS != 255 {
		t.Errorf("the gzip header carries more than the data: mtime %v, name %q, comment %q, extra %d bytes, OS %d", h.ModTime, h.Name, h.Comment, len(h.Extra), h.OS)
	}
}

// A gzipped artifact gone stale is refused as a plain one is, naming the
// gzipped file in the fix; one whose container is broken is refused with
// the fix, never read as another document.
func TestCompiledGzipStaleRefused(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	t.Run("binding edited", func(t *testing.T) {
		t.Parallel()
		bp, cp, _ := gzipCopy(t)
		b, err := os.ReadFile(bp)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bp, append(b, []byte("\n# edited\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
		fix := "gotogether compile -binding " + bp + " -o " + cp
		if err == nil || !strings.Contains(err.Error(), "is stale: it was compiled from another binding") || !strings.Contains(err.Error(), fix) {
			t.Fatalf("a stale gzipped artifact: %v; want it refused as compiled from another binding, with the fix %q", err, fix)
		}
	})
	t.Run("source edited", func(t *testing.T) {
		t.Parallel()
		bp, cp, _ := gzipCopy(t)
		src := filepath.Join(filepath.Dir(bp), "account.qnt")
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(src, append(b, []byte("\n// edited\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "account.qnt changed since it was compiled") || !strings.Contains(err.Error(), "-o "+cp) {
			t.Fatalf("a gzipped artifact of a changed source: %v; want it refused, with the fix", err)
		}
	})
	t.Run("container truncated", func(t *testing.T) {
		t.Parallel()
		bp, cp, _ := gzipCopy(t)
		b, err := os.ReadFile(cp)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cp, b[:len(b)/2], 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "gzip container") || !strings.Contains(err.Error(), "compile it again") {
			t.Fatalf("a truncated gzipped artifact: %v; want it refused for its container, with the fix", err)
		}
	})
}

// An artifact written and read back is the same model as the compile it
// was written from, by Differs, gzipped or plain, though json.Marshal
// writes a RawMessage's <, > and & escaped and quint's output does not:
// compile -check once called such a model (twophase's) stale.
func TestCompiledWrittenIsNotStale(t *testing.T) {
	t.Parallel()
	cm := &CompiledModel{Format: CompiledFormat, Quint: "0.33.1", Binding: File{Path: "b.yaml", SHA256: "00"}, Module: "m",
		Typecheck: json.RawMessage(`{"types": {"7": "(int, int) => bool"}, "text": "x < y and y > z & w"}`),
		Eval:      &binding.EvalModule{Text: "module m_eval {}"},
		IR:        json.RawMessage(`{"modules": [{"name": "m", "op": "<"}]}`)}
	for _, name := range []string{"m.model.json", "m" + CompiledExt} {
		path := filepath.Join(t.TempDir(), name)
		if err := cm.Write(path); err != nil {
			t.Fatal(err)
		}
		got, err := LoadCompiled(path)
		if err != nil {
			t.Fatal(err)
		}
		if d := cm.Differs(got); len(d) > 0 {
			t.Errorf("%s: written and read back, it differs from its compile in %v", name, d)
		}
		if d := cm.Differs(&CompiledModel{Format: cm.Format, Binding: cm.Binding, Module: cm.Module, Eval: cm.Eval, IR: cm.IR,
			Typecheck: json.RawMessage(`{"types": {"7": "(int, int) => bool"}, "text": "x < y or y > z & w"}`)}); len(d) != 1 {
			t.Errorf("%s: another typecheck, differing in %v: want the typecheck alone", name, d)
		}
	}
}
