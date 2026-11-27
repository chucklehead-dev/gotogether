package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/mbtmodel"
)

// doctor lists the module's compiled models for connect by spec and main
// module, as it lists bindings': a spec a connect test names with none
// (and the compile that writes it), current, stale once its spec changes
// (a skew); and an artifact no test names as connect would read it: an
// orphan whose spec is gone, a plain one an earlier gotogether kept, one
// where connect does not look. No quint runs: the check is by hashes.
func TestDoctorListsConnectModels(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bank, instance := mustRead(t, "../../connect/testdata/bank.qnt"), mustRead(t, "../../connect/testdata/instance.qnt")
	bankGz, instanceGz := mustRead(t, "../../connect/testdata/bank.mbt.json.gz"), mustRead(t, "../../connect/testdata/instance.design.mbt.json.gz")
	write("svc/instance.qnt", instance)
	write("svc/bank.qnt", bank)
	write("svc/bank.mbt.json.gz", bankGz)
	write("svc/drive_test.go", []byte(`package svc

import "github.com/chucklehead-dev/gotogether/connect"

var _ = connect.Config{Spec: "instance.qnt", Main: "design", MaxSamples: 3}
var _ = []connect.Config{{Main: "x", Spec: "missing.qnt"}, {Spec: "bank.qnt"}}
`))
	write("gone/bank.mbt.json.gz", bankGz)
	write("old/bank.qnt", bank)
	write("old/bank.mbt.json", gunzipped(t, bankGz))
	write("moved/bank.qnt", bank)
	write("moved/other.mbt.json.gz", bankGz)
	write("testdata/bank.mbt.json.gz", bankGz)
	var out bytes.Buffer
	if _, err := doctor(&out, []string{"-C", dir}); err != nil {
		t.Fatal(err)
	}
	inst := filepath.Join(dir, "svc", "instance.qnt")
	for _, want := range []string{
		"compiled models for connect",
		"spec " + inst + " -main design", "none (its connect tests need quint: gotogether compile -spec " + inst + " -main design",
		"spec " + filepath.Join(dir, "svc", "bank.qnt") + " ", "current (" + filepath.Join(dir, "svc", "bank.mbt.json.gz"),
		"orphan", "its spec " + filepath.Join(dir, "gone", "bank.qnt") + " is not here",
		"plain", filepath.Join(dir, "old", "bank.mbt.json") + " is the plain form an earlier gotogether kept, which connect no longer finds (it reads bank.mbt.json.gz)",
		"misplaced", "at " + filepath.Join(dir, "moved", "bank.mbt.json.gz") + ", not " + filepath.Join(dir, "moved", "other.mbt.json.gz"),
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "missing.qnt") || strings.Contains(out.String(), filepath.Join(dir, "testdata")) {
		t.Errorf("a spec that is not there, or testdata's artifact, listed:\n%s", out.String())
	}
	// the instance's model compiled: current; its spec edited: stale, a skew
	write("svc/instance.design.mbt.json.gz", instanceGz)
	ps := mbtPieces(dir, nil)
	i := slices.IndexFunc(ps, func(p piece) bool { return p.name == "spec "+inst+" -main design" })
	if i < 0 || ps[i].version != "current" || ps[i].skew != "" || len(ps) != 5 {
		t.Fatalf("a current compiled model, listed once with the four others: %+v", ps)
	}
	write("svc/instance.qnt", append(instance, "\n// edited\n"...))
	out.Reset()
	code, err := doctor(&out, []string{"-C", dir, "-strict"})
	if err != nil || code != 1 || !strings.Contains(out.String(), "is stale: instance.qnt changed since it was compiled") || !strings.Contains(out.String(), "gotogether compile -spec "+inst+" -main design") {
		t.Errorf("a stale compiled model: exit %d, %v:\n%s", code, err, out.String())
	}
	// named with -compiled, connect's artifact is checked as connect's
	out.Reset()
	if _, err := doctor(&out, []string{"-C", t.TempDir(), "-compiled", filepath.Join(dir, "svc", "bank.mbt.json.gz")}); err != nil || !strings.Contains(out.String(), "current ("+filepath.Join(dir, "svc", "bank.mbt.json.gz")) {
		t.Errorf("-compiled with connect's artifact: %v:\n%s", err, out.String())
	}
}

func gunzipped(t *testing.T, gz []byte) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.mbt.json.gz")
	if err := os.WriteFile(path, gz, 0o644); err != nil {
		t.Fatal(err)
	}
	cm, err := mbtmodel.Peek(path)
	if err != nil {
		t.Fatal(err)
	}
	plain := strings.TrimSuffix(path, ".gz")
	if err := cm.Write(plain); err != nil {
		t.Fatal(err)
	}
	return mustRead(t, plain)
}
