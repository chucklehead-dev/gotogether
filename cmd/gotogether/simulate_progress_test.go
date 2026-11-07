package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/validate"
)

func TestSimulateLiveDiagnosticsKeepJSONIsolated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake verifier and signal")
	}
	dir := t.TempDir()
	quint := filepath.Join(dir, "quint")
	for path, text := range map[string]string{
		quint:                             "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.33.1; exit 0; fi\necho 'live native diagnostic' >&2\necho '[violation] unfinished output'\nexec sleep 60\n",
		filepath.Join(dir, "source.qnt"):  "module source { var x: int\naction init = x' = 0\naction step = x' = x\nval safe = true\n}\n",
		filepath.Join(dir, "source.yaml"): "spec: source.qnt\nmodule: source\ninvariants: [safe]\nsimulate: {holds: [safe]}\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAnalysisCommandProcess$", "--", "simulate", "-json", "-verify", "8", "-binding", filepath.Join(dir, "source.yaml"), "-quint", quint)
	cmd.Env = append(os.Environ(), "GO_WANT_MODEL_ANALYSIS_MAIN=1", "GOTOGETHER_REPORT_DIR=", "HOME="+dir, "XDG_CACHE_HOME="+dir)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		w.Close()
		t.Fatal(err)
	}
	w.Close()
	live := make(chan struct{}, 1)
	stderr := make(chan string, 1)
	go func() {
		var b strings.Builder
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			b.WriteString(line + "\n")
			if strings.Contains(line, "[violation] unfinished output") {
				live <- struct{}{}
			}
		}
		stderr <- b.String()
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		}
	})
	select {
	case <-live:
	case err := <-done:
		finished = true
		t.Fatalf("command returned before live output: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("verifier diagnostics buffered")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		finished = true
		if err == nil {
			t.Fatal("canceled CLI succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canceled CLI did not exit")
	}
	var doc validate.Document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout was not one JSON payload: %v\n%s", err, stdout.Bytes())
	}
	if doc.Verdict != "error" || doc.Exit != 2 || doc.Error == "" || len(doc.Checks) != 0 {
		t.Fatalf("partial output became model evidence: %s", stdout.Bytes())
	}
	text := <-stderr
	for _, want := range []string{"START prepare", "START invariant safe", "live native diagnostic", "END invariant safe status=canceled elapsed=", "END binding status=canceled elapsed="} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in stderr:\n%s", want, text)
		}
	}
}
