package canon

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// The tests read quint's outputs for testdata/m.qnt and its variants,
// kept compressed beside it (testdata/out/): written by quint 0.33.1 with
// GOTOGETHER_UPDATE_CANON=1 go test ./internal/qoracle/canon/, and read
// with no quint installed. Each variant is the model written as m.qnt in a
// directory of its own, the directory recorded as /ROOT.

// shiftPrefix is a module that consumes ids before every other: the
// shifted variant's outputs differ from the model's in every id, and in
// nothing else once the module is stripped.
const shiftPrefix = "module zzshift { pure val s1 = 1 pure val s2 = Set(1, 2) } "

// variants are the model's: the model, shifted, a definition renamed (upd,
// which nothing references), and one definition's body changed (f's
// operator).
var variants = map[string]func(string) string{
	"base":    func(s string) string { return s },
	"shifted": func(s string) string { return shiftPrefix + s },
	"renamed": func(s string) string { return strings.Replace(s, "pure def upd(", "pure def upq(", 1) },
	"changed": func(s string) string { return strings.Replace(s, "val m = n + N", "val m = n - N", 1) },
}

var outputs = []string{"typecheck", "compile", "parse", "map"}

func TestUpdateOutputs(t *testing.T) {
	if os.Getenv("GOTOGETHER_UPDATE_CANON") != "1" {
		t.Skip("set GOTOGETHER_UPDATE_CANON=1 to rewrite testdata/out with quint")
	}
	if _, err := exec.LookPath("quint"); err != nil {
		t.Fatal("quint is not installed")
	}
	src, err := os.ReadFile("testdata/m.qnt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("testdata/out", 0o755); err != nil {
		t.Fatal(err)
	}
	for name, f := range variants {
		dir := t.TempDir()
		qnt := filepath.Join(dir, "m.qnt")
		if err := os.WriteFile(qnt, []byte(f(string(src))), 0o644); err != nil {
			t.Fatal(err)
		}
		runs := [][]string{
			{"typecheck", "--out", filepath.Join(dir, "typecheck.json"), qnt},
			{"compile", "--target", "json", "--main", "m", "--init", "init", "--step", "step", "--out", filepath.Join(dir, "compile-errors.json"), qnt},
			{"parse", "--out", filepath.Join(dir, "parse.json"), "--source-map", filepath.Join(dir, "map.json"), qnt},
		}
		for _, args := range runs {
			cmd := quintcmd.Command(context.Background(), "", dir, args...)
			var stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = io.Discard, &stderr
			if args[0] == "compile" {
				// the IR is stdout's: --out writes the modules before
				// flattening. A file, not a pipe: node exits before a pipe
				// drains (validate/eval.go does the same)
				out, err := os.Create(filepath.Join(dir, "compile.json"))
				if err != nil {
					t.Fatal(err)
				}
				defer out.Close()
				cmd.Stdout = out
			}
			if err := cmd.Run(); err != nil {
				t.Fatalf("quint %s: %v\n%s", args[0], err, stderr.Bytes())
			}
		}
		for _, o := range outputs {
			data, err := os.ReadFile(filepath.Join(dir, o+".json"))
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.ReplaceAll(data, []byte(dir), []byte("/ROOT"))
			var b bytes.Buffer
			w, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
			w.Write(data)
			w.Close()
			if err := os.WriteFile(filepath.Join("testdata/out", name+"."+o+".json.gz"), b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// output is a variant's output (typecheck, compile, parse or map).
func output(t *testing.T, variant, o string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata/out", variant+"."+o+".json.gz"))
	if err != nil {
		t.Fatalf("%v (GOTOGETHER_UPDATE_CANON=1 writes it)", err)
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
