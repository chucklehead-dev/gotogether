package grist_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
)

// sizing is a binary of its own (<app>-sizing): Main runs the one on
// PATH with sizing's arguments, passing its output and exit status on,
// and without one says how to make it. The app's binary links no
// sizing (TestSizingStaysOutOfTheBinary, in examples/counters).
func TestMainSizingRunsItsBinary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	var out bytes.Buffer
	err := grist.MainArgs(context.Background(), apiApp(), []string{"sizing", "-rate", "40/s"}, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "no names-sizing beside this binary or on PATH") || !strings.Contains(err.Error(), "gristload.Main") {
		t.Fatalf("without names-sizing: %v, want the hint naming gristload.Main", err)
	}
	script := "#!/bin/sh\necho \"sizing got $*\"\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "names-sizing"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = grist.MainArgs(context.Background(), apiApp(), []string{"sizing", "-rate", "40/s", "-burst", "3x30s"}, &out, &out)
	var status grist.ExitStatus
	if !errors.As(err, &status) || status != 3 || out.String() != "sizing got -rate 40/s -burst 3x30s\n" {
		t.Fatalf("with names-sizing: %v, output %q; want exit status 3 and its arguments echoed", err, out.String())
	}
}
