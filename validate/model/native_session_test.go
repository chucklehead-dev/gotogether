package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

func sessionBindings(t *testing.T, source *GraphResult) (string, string) {
	t.Helper()
	first := batchBinding(t, source, "{holds: [a, b], verifySteps: 2}")
	content := "spec: source.qnt\nmodule: source\ninvariants: [a,b]\nsimulate: {holds: [a,b], verifySteps: 2}\n"
	if err := os.WriteFile(first, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(filepath.Dir(first), "second.yaml")
	if err := os.WriteFile(second, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return first, second
}
func testIdentity(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:])
}

func stableTestTool(context.Context, string) (string, bool) {
	return testIdentity("explicit-test-tool-identity"), true
}

func TestSimulateManyExactProofAndIndependentReports(t *testing.T) {
	source := batchFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "proof"), nil)
	first, second := sessionBindings(t, source)
	var reports []*Report
	for report, err := range simulateMany(t.Context(), []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 8}, stableTestTool) {
		if err != nil {
			t.Fatal(err)
		}
		reports = append(reports, report)
		proof := report.Invariants[0].NativeProof
		if proof == nil || proof.Reused != (len(reports) == 2) || proof.ProducerBinding != first || proof.Steps != 2 {
			t.Fatalf("proof provenance: %+v", proof)
		}
		if len(reports) == 1 {
			proof.Invariants[0] = "caller mutation"
			proof.RequestSHA256 = "caller mutation"
		}
	}
	if len(reports) != 2 || len(batchInvocations(t, source)) != 1 {
		t.Fatalf("reports=%d calls=%v", len(reports), batchInvocations(t, source))
	}
	p := reports[1].Invariants[0].NativeProof
	if p.Invariants[0] != "a" || p.RequestSHA256 == "caller mutation" {
		t.Fatal("cached proof aliased consumer report")
	}
	config := sha256.Sum256([]byte(nativeApalacheConfig))
	if p.ConfigurationSHA256 != hex.EncodeToString(config[:]) {
		t.Fatal("configuration identity lost")
	}
	if !strings.Contains(reports[1].Text, "reused exact bounded native proof") {
		t.Fatal("text hid shared proof origin")
	}
	doc := reports[1].Doc()
	if !strings.Contains(doc.Checks[0].Detail, "reused exact bounded native proof") {
		t.Fatal("document detail hid proof origin")
	}
	doc.Checks[0].NativeProof.Invariants[0] = "document mutation"
	if p.Invariants[0] != "a" || reports[1].Invariants[1].NativeProof.Invariants[0] != "a" {
		t.Fatal("projection or group members share mutable provenance")
	}
	if reports[0].Binding != first || reports[1].Binding != second {
		t.Fatal("binding identities collapsed")
	}
}

func TestSimulateManyContextChangesMiss(t *testing.T) {
	for _, change := range []string{"constant", "defs", "module", "import", "import-body", "new-source", "property-body", "init-step", "group-order", "group-member", "bound", "tool"} {
		t.Run(change, func(t *testing.T) {
			source := batchFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "proof"), nil)
			first, second := sessionBindings(t, source)
			tool := "one"
			identity := func(context.Context, string) (string, bool) { return testIdentity(tool), true }
			if change == "import-body" {
				spec, _ := os.ReadFile(source.checker.Binding.Spec)
				if err := os.WriteFile(source.checker.Binding.Spec, []byte(strings.Replace(string(spec), "module source {", "module source { import imported.* from \"./imported\"", 1)), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(first), "imported.qnt"), []byte("module imported { pure val n = 1 }\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			sourceSHA := ""
			count := 0
			for report, err := range simulateMany(t.Context(), []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 8}, identity) {
				if err != nil {
					t.Fatal(err)
				}
				count++
				if report.Invariants[0].NativeProof == nil || report.Invariants[0].NativeProof.Reused {
					t.Fatalf("changed context reused: %+v", report.Invariants[0])
				}
				current := report.Invariants[0].NativeProof.SourceSHA256
				if count == 1 {
					sourceSHA = current
				} else {
					changedSource := change != "tool" && change != "group-order" && change != "group-member" && change != "bound"
					if (current != sourceSHA) != changedSource {
						t.Fatalf("source context digest lost precision for %s", change)
					}
				}
				if count != 1 {
					continue
				}
				raw, err := os.ReadFile(second)
				if err != nil {
					t.Fatal(err)
				}
				text := string(raw)
				switch change {
				case "constant":
					text += "constants: {N: '1'}\n"
				case "defs":
					text += "defs: 'pure val authored = true'\n"
				case "module":
					text = strings.Replace(text, "module: source", "module: alternate", 1)
				case "group-order":
					text = strings.Replace(text, "invariants: [a,b]", "invariants: [b,a]", 1)
				case "group-member":
					text = strings.ReplaceAll(text, "[a,b]", "[a,unclassified]")
				case "bound":
					text = strings.Replace(text, "verifySteps: 2", "verifySteps: 3", 1)
				case "property-body", "init-step":
					original, err := os.ReadFile(source.checker.Binding.Spec)
					if err != nil {
						t.Fatal(err)
					}
					from, to := "val a = true", "val a = false"
					if change == "init-step" {
						from, to = "action step = x' = x", "action step = x' = x + 1"
					}
					if err := os.WriteFile(source.checker.Binding.Spec, []byte(strings.Replace(string(original), from, to, 1)), 0600); err != nil {
						t.Fatal(err)
					}
				case "tool":
					tool = "two"
				case "import":
					path := filepath.Join(filepath.Dir(first), "imported.qnt")
					if err := os.WriteFile(path, []byte("module imported { pure val n = 2 }\n"), 0600); err != nil {
						t.Fatal(err)
					}
					spec, err := os.ReadFile(source.checker.Binding.Spec)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(source.checker.Binding.Spec, []byte(strings.Replace(string(spec), "module source {", "module source { import imported.* from \"./imported\"", 1)), 0600); err != nil {
						t.Fatal(err)
					}
				case "import-body":
					if err := os.WriteFile(filepath.Join(filepath.Dir(first), "imported.qnt"), []byte("module imported { pure val n = 2 }\n"), 0600); err != nil {
						t.Fatal(err)
					}
				case "new-source":
					if err := os.WriteFile(filepath.Join(filepath.Dir(first), "new.qnt"), []byte("module new { pure val n = 1 }\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(second, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if count != 2 || len(batchInvocations(t, source)) != 2 {
				t.Fatalf("count=%d calls=%v", count, batchInvocations(t, source))
			}
		})
	}
}

func TestSimulateManyStopsAndDoesNotPersist(t *testing.T) {
	source := batchFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "proof"), nil)
	first, second := sessionBindings(t, source)
	sequence := simulateMany(t.Context(), []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2}, stableTestTool)
	for range sequence {
		break
	}
	for _, err := range sequence {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(batchInvocations(t, source)) != 2 {
		t.Fatal("iterator lifetime leaked proof storage")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	n := 0
	for report, err := range simulateMany(ctx, []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2}, stableTestTool) {
		n++
		if n == 1 {
			if err != nil {
				t.Fatal(err)
			}
			cancel()
		} else if report != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled hit returned report: %+v %v", report, err)
		}
	}
	if n != 2 || len(batchInvocations(t, source)) != 3 {
		t.Fatal("canceled hit dispatched native work")
	}
	// Unsupported custom tools continue normal verification and never reuse.
	n = 0
	for report, err := range SimulateMany(t.Context(), []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2}) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		if report.Invariants[0].NativeProof != nil {
			t.Fatal("wrapper acquired trusted tool provenance")
		}
	}
	if n != 2 || len(batchInvocations(t, source)) != 5 {
		t.Fatal("custom wrapper unexpectedly reused")
	}
	for report, err := range SimulateMany(t.Context(), []string{first, second}, SimulateOptions{Out: "one.qnt"}) {
		if report != nil || err == nil {
			t.Fatal("shared graph output admitted")
		}
	}
}

func TestNativeSessionRejectsPublicationDriftAndMalformedProof(t *testing.T) {
	for _, kind := range []string{"tool-drift", "source-drift", "malformed", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			script := nativeFakeScript(nativeProofJSON, "", 0, "published")
			if kind == "malformed" {
				script = nativeFakeScript(`{"stage":"verifying","status":"unknown","errors":[]}`, "", 0, "published")
			}
			source := batchFakeSource(t, script, nil)
			first, _ := sessionBindings(t, source)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var sink bytes.Buffer
			progress := &sessionMutationWriter{write: func() {
				switch kind {
				case "source-drift":
					_ = os.WriteFile(source.checker.Binding.Spec, []byte("module source { pure val changed = true }\n"), 0600)
				case "cancel":
					cancel()
				}
			}, target: &sink}
			calls := 0
			identity := func(context.Context, string) (string, bool) {
				calls++
				if kind == "tool-drift" && calls > 1 {
					return testIdentity("changed"), true
				}
				return testIdentity("original"), true
			}
			n := 0
			for report, err := range simulateMany(ctx, []string{first, first}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2, Progress: progress}, identity) {
				n++
				if report != nil || err == nil {
					t.Fatalf("invalid proof published: %+v %v", report, err)
				}
			}
			if n != 1 || len(batchInvocations(t, source)) != 1 {
				t.Fatalf("failed proof continued: n%d calls%v", n, batchInvocations(t, source))
			}
		})
	}
}

type sessionMutationWriter struct {
	match  string
	write  func()
	target *bytes.Buffer
}

func (w *sessionMutationWriter) Write(b []byte) (int, error) {
	match := w.match
	if match == "" {
		match = "published"
	}
	if strings.Contains(string(b), match) {
		w.write()
	}
	return w.target.Write(b)
}

func TestSimulateManyObserverProductsAndOwnGoals(t *testing.T) {
	skip.Quint(t)
	actual, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	script := `if [ "$1" = verify ]; then
 printf '%s\n' "$*" >> "$0.invocations"
 cp "$2" "$0.invariant-input"
` + nativeFakeScript(nativeProofJSON, "", 0, "proof") + "\nfi\nexec " + quote(actual) + " \"$@\"\n"
	source := comparisonFakeSource(t, script)
	first, second := sessionBindings(t, source)
	for i, path := range []string{first, second} {
		raw, _ := os.ReadFile(path)
		goal := fmt.Sprintf("goal%d", i)
		text := strings.Replace(string(raw), "verifySteps: 2}", "verifySteps: 2, possible: [{name: "+goal+", when: 'x == 0'}]}", 1)
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for report, err := range simulateMany(t.Context(), []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2}, stableTestTool) {
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Possibles) != 1 || !report.Possibles[0].Found || report.Possibles[0].Possible.Name != fmt.Sprintf("goal%d", n) {
			t.Fatalf("binding goals lost: %+v", report.Possibles)
		}
		n++
	}
	if n != 2 || len(batchInvocations(t, source)) != 1 {
		t.Fatalf("observer-only differences prevented reuse: n%d calls%v", n, batchInvocations(t, source))
	}
	input, _ := os.ReadFile(source.checker.Options.Quint + ".invariant-input")
	if strings.Contains(string(input), "__gtg_possible_") {
		t.Fatal("owned observer declarations reached invariant input")
	}
	raw, _ := os.ReadFile(second)
	_ = os.WriteFile(second, []byte(strings.Replace(string(raw), "x == 0", "missingPredicate", 1)), 0600)
	outcomes := 0
	for report, err := range simulateMany(t.Context(), []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2}, stableTestTool) {
		outcomes++
		if report != nil && report.Binding == first {
			continue
		}
		if report != nil || err == nil || !strings.Contains(err.Error(), "observer precheck") {
			t.Fatalf("invalid observer silently skipped: %+v %v", report, err)
		}
	}
	if outcomes != 2 || len(batchInvocations(t, source)) != 2 {
		t.Fatal("invalid observer was not checked independently before proof reuse")
	}

}

func TestNativeManagedToolIdentity(t *testing.T) {
	if os.Getenv("GOTOGETHER_NATIVE_VERIFY") != "1" {
		t.Skip("opt in to installed managed tool closure inspection; no SMT")
	}
	identity, ok := resolveNativeToolIdentity(t.Context(), "quint")
	if !ok || len(identity) != 64 {
		t.Fatal("installed managed native tool closure is not eligible")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "release"), []byte("JAVA_VERSION=21\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "java"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, ok := resolveNativeToolIdentity(t.Context(), "quint"); ok {
		t.Fatal("Java script wrapper admitted as a closed native tool installation")
	}
}

func TestSimulateManySkipAndClassificationRemainIndependent(t *testing.T) {
	source := batchFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "proof"), nil)
	first, second := sessionBindings(t, source)
	text := "spec: source.qnt\nmodule: source\ninvariants: [a,b]\nsimulate: {holds: [a,b], verifySkip: intentional}\n"
	if err := os.WriteFile(second, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	count := 0
	for report, err := range simulateMany(t.Context(), []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2}, stableTestTool) {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if count == 2 && (report.Skipped != "intentional" || len(report.Invariants) != 0) {
			t.Fatalf("proof overrode skip: %+v", report)
		}
	}
	if count != 2 || len(batchInvocations(t, source)) != 1 {
		t.Fatal("skipped binding dispatched proof work")
	}
	text = "spec: source.qnt\nmodule: source\ninvariants: [a,b]\nsimulate: {holds: [b], allows: [a]}\n"
	if err := os.WriteFile(second, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	count = 0
	for report, err := range simulateMany(t.Context(), []string{first, second, first}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2}, stableTestTool) {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if count == 2 {
			if report.Code != 1 || report.Invariants[0].NativeProof != nil || report.Invariants[1].NativeProof != nil {
				t.Fatalf("proof changed classifications: %+v", report)
			}
		}
	}
	if count != 2 || len(batchInvocations(t, source)) != 4 {
		t.Fatalf("nonzero report continued or reused incompatible group: %d %v", count, batchInvocations(t, source))
	}
}

func TestSimulateManyConcurrentIterationsAreIsolated(t *testing.T) {
	source := batchFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "proof"), nil)
	first, second := sessionBindings(t, source)
	sequence := simulateMany(t.Context(), []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2}, stableTestTool)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			n := 0
			for report, err := range sequence {
				if err != nil {
					t.Error(err)
					return
				}
				if report.Invariants[0].NativeProof.Reused != (n == 1) {
					t.Error("proof lifetime crossed iterator calls")
				}
				n++
			}
			if n != 2 {
				t.Error("incomplete iteration")
			}
		})
	}
	wg.Wait()
	if len(batchInvocations(t, source)) != 2 {
		t.Fatal("iterations shared proof storage")
	}
}

func TestNativeSessionNeverStoresNonpositiveResults(t *testing.T) {
	for _, kind := range []string{"counterexample", "unknown", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			script := nativeFakeScript(nativeCexJSON, nativeCexITF, 1, "published")
			if kind == "unknown" {
				script = nativeFakeScript(`{"stage":"verifying","status":"unknown","errors":[]}`, "", 0, "published")
			}
			if kind == "canceled" {
				script = nativeFakeScript(nativeProofJSON, "", 0, "published")
			}
			source := batchFakeSource(t, script, map[string]bool{"a": true, "b": true})
			first, _ := sessionBindings(t, source)
			session := newNativeProofSession(stableTestTool)
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), nativeProofSessionKey{}, session))
			defer cancel()
			var output bytes.Buffer
			progress := &sessionMutationWriter{target: &output, write: func() {
				if kind == "canceled" {
					cancel()
				}
			}}
			report, err := simulate(ctx, first, nil, nil, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2, Progress: progress})
			if kind == "counterexample" {
				if err != nil || report == nil || report.Code != 1 {
					t.Fatalf("counterexample lost: %+v %v", report, err)
				}
			} else if report != nil || err == nil {
				t.Fatalf("error became proof: %+v %v", report, err)
			}
			if len(session.proofs) != 0 {
				t.Fatalf("%s seeded positive proof store", kind)
			}
		})
	}
}

func TestNativeSessionCanceledHitDoesNotPublish(t *testing.T) {
	source := batchFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "proof"), nil)
	first, second := sessionBindings(t, source)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var output bytes.Buffer
	progress := &sessionMutationWriter{match: "reuses exact positive native proof", write: cancel, target: &output}
	n := 0
	for report, err := range simulateMany(ctx, []string{first, second}, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2, Progress: progress}, stableTestTool) {
		n++
		if n == 1 {
			if report == nil || err != nil {
				t.Fatalf("first proof failed: %+v %v", report, err)
			}
		} else if report != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled hit published: %+v %v", report, err)
		}
	}
	if n != 2 || len(batchInvocations(t, source)) != 1 {
		t.Fatal("cache-hit cancellation did not stop at the existing proof")
	}
}
