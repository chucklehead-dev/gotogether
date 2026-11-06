package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/validate"
)

type discoveryBenchmarkCase struct {
	name, dir string
	files     []string
	bytes     int64
}

// Use recorded artifacts rather than expanding synthetic JSON token arrays.
// GOTOGETHER_DISCOVERY_BENCH_DIR optionally adds a real captured artifact tree;
// it is copied, never changed. Every case includes current healthy evidence so
// an otherwise unrelated directory still exercises the complete suite command.
func discoveryBenchmarkCases(b *testing.B) []discoveryBenchmarkCase {
	b.Helper()
	prepare := func(name string, sources map[string]string) discoveryBenchmarkCase {
		b.Helper()
		c := discoveryBenchmarkCase{name: name, dir: b.TempDir()}
		for dest, source := range sources {
			data, err := os.ReadFile(source)
			if err != nil {
				b.Fatal(err)
			}
			path := filepath.Join(c.dir, dest)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				b.Fatal(err)
			}
			c.files = append(c.files, path)
			c.bytes += int64(len(data))
		}
		data := []byte(`{"format":"` + formats.Evidence.Name() + `","role":"faithful","test":"DiscoveryBenchmark","runs":1,"conform":1}`)
		path := filepath.Join(c.dir, "discovery-benchmark.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			b.Fatal(err)
		}
		c.files = append(c.files, path)
		c.bytes += int64(len(data))
		slices.Sort(c.files)
		return c
	}
	root := filepath.Join("..", "..")
	cases := []discoveryBenchmarkCase{prepare("tracked-mix", map[string]string{
		"corpus/election-after-heal.json": filepath.Join(root, "examples/raftkv/testdata/corpus/f8cf7e5f7ea2.json"),
		"corpus/raft-history.json":        filepath.Join(root, "examples/raftkv/testdata/corpus/32814032ea82.json"),
		"traces/rust.itf.json":            filepath.Join(root, "itf/testdata/bignumber.rust.itf.json"),
		"traces/typescript.itf.json":      filepath.Join(root, "itf/testdata/bignumber.typescript.itf.json"),
	})}
	if source := os.Getenv("GOTOGETHER_DISCOVERY_BENCH_DIR"); source != "" {
		files := map[string]string{}
		if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
				return nil
			}
			rel, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			files[filepath.Join("capture", rel)] = path
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		if len(files) == 0 {
			b.Fatal("GOTOGETHER_DISCOVERY_BENCH_DIR has no regular JSON files")
		}
		cases = append(cases, prepare("captured-tree", files))
	}
	return cases
}

// BenchmarkEvidenceDiscoveryCLI includes process startup, directory walking,
// format discovery, reader validation and suite rendering. Outputs go to the
// subprocess's default null streams; parent allocations do not represent the
// child process, so this reports time and logical input size, not child B/op.
// GOTOGETHER_DISCOVERY_BENCH_BIN selects a prebuilt CLI for alternating controls;
// absent it, the current CLI is built outside timing.
func BenchmarkEvidenceDiscoveryCLI(b *testing.B) {
	cli := os.Getenv("GOTOGETHER_DISCOVERY_BENCH_BIN")
	if cli == "" {
		cli = filepath.Join(b.TempDir(), "gotogether")
		if out, err := exec.CommandContext(b.Context(), "go", "build", "-o", cli, ".").CombinedOutput(); err != nil {
			b.Fatalf("build CLI: %v\n%s", err, out)
		}
	}
	env := make([]string, 0, len(os.Environ()))
	for _, variable := range os.Environ() {
		if strings.HasPrefix(variable, "GOTOGETHER_SESSION=") || strings.HasPrefix(variable, "GOTOGETHER_REPORT_DIR=") || strings.HasPrefix(variable, "GOTOGETHER_MAIN=") {
			continue
		}
		env = append(env, variable)
	}
	for _, c := range discoveryBenchmarkCases(b) {
		b.Run(c.name, func(b *testing.B) {
			invoke := func() *exec.Cmd {
				cmd := exec.CommandContext(b.Context(), cli, "evidence", "-history", c.dir)
				cmd.Env = env
				return cmd
			}
			// Confirm the captured fixture is accepted before collecting timings.
			if out, err := invoke().CombinedOutput(); err != nil {
				b.Fatalf("fixture refused: %v\n%s", err, out)
			}
			for b.Loop() {
				if err := invoke().Run(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(c.files)), "files/op")
			b.ReportMetric(float64(c.bytes), "input-B/op")
		})
	}
}

// BenchmarkEvidenceDiscoveryLeaves profiles the two standalone family queries.
// This isolates scanner CPU/allocations; the CLI benchmark above is the
// end-to-end decision control and may consolidate these queries into one pass.
func BenchmarkEvidenceDiscoveryLeaves(b *testing.B) {
	for _, c := range discoveryBenchmarkCases(b) {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				found := 0
				for _, path := range c.files {
					evidence, err := formats.Evidence.IsFile(path)
					if err != nil {
						b.Fatal(err)
					}
					manifest, err := validate.IsManifest(path)
					if err != nil {
						b.Fatal(err)
					}
					if evidence || manifest {
						found++
					}
				}
				if found == 0 {
					b.Fatal("healthy evidence was not discovered")
				}
			}
			b.ReportMetric(float64(len(c.files)), "files/op")
			b.ReportMetric(float64(c.bytes), "input-B/op")
		})
	}
}
