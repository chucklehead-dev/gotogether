package connect

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copySpec copies testdata's spec and, named as given, its compiled
// model (testdata/<stem>.mbt.json.gz) into a directory of the test's,
// returning the spec's path there; edit changes the spec's copy.
func copySpec(t *testing.T, spec, artifact string, edit bool) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", spec))
	if err != nil {
		t.Fatal(err)
	}
	if edit {
		src = append(src, "\n// changed\n"...)
	}
	if err := os.WriteFile(filepath.Join(dir, spec), src, 0o644); err != nil {
		t.Fatal(err)
	}
	if artifact != "" {
		cm, _, err := LoadMBT(CompiledFor(filepath.Join("testdata", spec), ""))
		if err != nil {
			t.Fatal(err)
		}
		if err := cm.Write(filepath.Join(dir, artifact)); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, spec)
}

// A spec's compiled model is kept gzipped by convention (CompiledExt):
// the checked-in one is a gzip stream holding the document Write writes
// plain, byte for byte, with a header naming no file, time or OS, so the
// same model is the same bytes; LoadMBT reads both forms as one model,
// and refuses a broken container with the fix.
func TestCompiledMBTGzip(t *testing.T) {
	t.Parallel()
	gz := CompiledFor("testdata/bank.qnt", "")
	if gz != filepath.Join("testdata", "bank"+CompiledExt) || CompiledFor("m/bank.qnt", "design") != filepath.Join("m", "bank.design.mbt.json.gz") {
		t.Fatalf("CompiledFor names %s: want the gzipped convention %s", gz, CompiledExt)
	}
	kept, err := os.ReadFile(gz)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(kept))
	if err != nil {
		t.Fatalf("%s is no gzip stream: %v", gz, err)
	}
	doc, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if h := zr.Header; !h.ModTime.IsZero() || h.Name != "" || h.Comment != "" || len(h.Extra) > 0 || h.OS != 255 {
		t.Errorf("%s's gzip header carries more than the data: mtime %v, name %q, comment %q, OS %d", gz, h.ModTime, h.Name, h.Comment, h.OS)
	}
	cm, _, err := LoadMBT(gz)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plain := filepath.Join(dir, "bank.mbt.json")
	if err := cm.Write(plain); err != nil {
		t.Fatal(err)
	}
	pb, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc, pb) {
		t.Fatalf("%s holds %d bytes, written plain it is %d: not the same document", gz, len(doc), len(pb))
	}
	if len(kept) >= len(pb) {
		t.Errorf("the gzipped artifact (%d bytes) is not smaller than the plain one (%d)", len(kept), len(pb))
	}
	again := filepath.Join(dir, "again"+CompiledExt)
	if err := cm.Write(again); err != nil {
		t.Fatal(err)
	}
	if ab, _ := os.ReadFile(again); !bytes.Equal(ab, kept) {
		t.Errorf("the model written gzipped again is %d bytes unlike the kept %d: the same model must be the same bytes", len(ab), len(kept))
	}
	pl, _, err := LoadMBT(plain)
	if err != nil {
		t.Fatal(err)
	}
	if d := cm.Differs(pl); len(d) > 0 {
		t.Errorf("the gzipped and plain artifacts differ in %v", d)
	}
	broken := filepath.Join(dir, "broken"+CompiledExt)
	if err := os.WriteFile(broken, kept[:len(kept)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadMBT(broken); err == nil || !strings.Contains(err.Error(), "gzip container") || !strings.Contains(err.Error(), "compile it again (gotogether compile -spec") {
		t.Errorf("a truncated gzipped artifact: %v; want it refused for its container, with the fix", err)
	}
}

// An artifact kept plain (an earlier gotogether's .mbt.json) is not found
// by the convention, and every run that would have used it says so with
// the fix: GOTOGETHER_COMPILED=1 and a run without quint.
func TestCompiledMBTPlainNotFound(t *testing.T) {
	spec := copySpec(t, "bank.qnt", "bank.mbt.json", false)
	plain := filepath.Join(filepath.Dir(spec), "bank.mbt.json")
	noQuint(t)
	_, _, err := GenerateRun(t.Context(), Config{Spec: spec, Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), plain+" is the plain form an earlier gotogether kept there") || !strings.Contains(err.Error(), "gotogether compile -spec "+spec) {
		t.Fatalf("=1 with a plain artifact only: %v; want the plain one named, with the compile", err)
	}
	t.Setenv(compiledEnv, "")
	_, err = Config{Spec: spec, Dir: t.TempDir()}.resolve(t.Context())
	if err == nil || !strings.Contains(err.Error(), plain+" is a plain compiled model an earlier gotogether kept") {
		t.Fatalf("unset, no quint, a plain artifact only: %v; want the plain one named", err)
	}
}

// A logTB records a test's log lines.
type logTB struct {
	testing.TB
	logs []string
}

func (l *logTB) Helper() {}
func (l *logTB) Logf(f string, args ...any) {
	l.logs = append(l.logs, strings.TrimSpace(fmt.Sprintf(f, args...)))
}

// fakeQuint puts an executable named quint first on PATH: connect's
// choice of path sees quint installed, though nothing runs it.
func fakeQuint(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "quint"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("QUINT_BIN", "")
}

// Where quint is installed and GOTOGETHER_COMPILED is unset, quint runs
// whatever the artifact beside the spec is; a stale one (or a plain one an
// earlier gotogether kept) is named in the log, once a process, with the
// compile that refreshes it, and a current one says nothing.
func TestCompiledStaleLogged(t *testing.T) {
	fakeQuint(t)
	t.Setenv(compiledEnv, "")
	stale := copySpec(t, "bank.qnt", "bank"+CompiledExt, true)
	abs, _ := filepath.Abs(stale)
	cm, ignored, err := compiledMode(Config{Spec: stale, Quint: "quint"}, abs)
	if err != nil || cm != nil {
		t.Fatalf("quint installed, unset: compiled %v, %v; want quint's path", cm != nil, err)
	}
	if ignored == nil || !strings.Contains(ignored.why, "is stale: bank.qnt changed since it was compiled") || !strings.Contains(ignored.why, "gotogether compile -spec "+abs) {
		t.Fatalf("a stale artifact passed over: %+v; want why, with the compile", ignored)
	}
	rec := &logTB{TB: t}
	RequireTraces(rec, Config{Spec: stale})
	RequireTraces(rec, Config{Spec: stale})
	if len(rec.logs) != 1 || !strings.Contains(rec.logs[0], "passing over the spec's compiled model") || !strings.Contains(rec.logs[0], "is stale") {
		t.Fatalf("logged %q: want the stale artifact named once", rec.logs)
	}
	plain := copySpec(t, "bank.qnt", "bank.mbt.json", false)
	rec = &logTB{TB: t}
	RequireTraces(rec, Config{Spec: plain})
	if len(rec.logs) != 1 || !strings.Contains(rec.logs[0], "is a plain compiled model an earlier gotogether kept") {
		t.Fatalf("logged %q: want the plain artifact named", rec.logs)
	}
	current := copySpec(t, "bank.qnt", "bank"+CompiledExt, false)
	rec = &logTB{TB: t}
	RequireTraces(rec, Config{Spec: current})
	if len(rec.logs) != 0 {
		t.Fatalf("a current artifact logged %q", rec.logs)
	}
	t.Setenv(compiledEnv, "0")
	if _, ignored, _ := compiledMode(Config{Spec: stale, Quint: "quint"}, abs); ignored != nil {
		t.Errorf(`"0" chose quint explicitly, yet the artifact was checked: %+v`, ignored)
	}
}
