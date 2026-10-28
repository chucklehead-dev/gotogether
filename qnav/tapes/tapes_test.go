// Package tapes holds gotogether-nav's VHS scenarios (*.tape), which
// render the README's GIFs and screenshots (render.sh) and, as text, are
// checked here against their goldens: each tape drives the binary in a
// real terminal (ttyd and a headless browser, through vhs).
package tapes

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tools are what a tape needs: vhs drives ttyd in a browser, ffmpeg
// encodes its frames, quint judges the nodes.
var tools = []string{"vhs", "ttyd", "ffmpeg", "quint"}

// TestTapes runs every tape with its outputs replaced by one text file,
// and compares its screens with testdata/<tape>.golden.txt
// (GOTOGETHER_UPDATE_GOLDEN=1 rewrites them).
func TestTapes(t *testing.T) {
	if testing.Short() {
		t.Skip("each tape runs the navigator in a terminal for seconds")
	}
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH (the module's README says how to install the tape tools)", tool)
		}
	}
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "gotogether-nav"), "../cmd/gotogether-nav")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building gotogether-nav: %v\n%s", err, out)
	}
	tapes, _ := filepath.Glob("*.tape")
	if len(tapes) == 0 {
		t.Fatal("no tapes")
	}
	for _, tape := range tapes {
		name := strings.TrimSuffix(tape, ".tape")
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			txt := filepath.Join(dir, name+".txt")
			src, err := os.ReadFile(tape)
			if err != nil {
				t.Fatal(err)
			}
			script := reOutput.ReplaceAllString(string(src), "")
			script = `Output "` + txt + "\"\n" + script
			run := filepath.Join(dir, tape)
			if err := os.WriteFile(run, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("vhs", run)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if os.Geteuid() == 0 {
				cmd.Env = append(cmd.Env, "VHS_NO_SANDBOX=true") // the browser's sandbox refuses root
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("vhs %s: %v\n%s", tape, err, out)
			}
			b, err := os.ReadFile(txt)
			if err != nil {
				t.Fatal(err)
			}
			got := screens(b)
			golden := filepath.Join("testdata", name+".golden.txt")
			if os.Getenv("GOTOGETHER_UPDATE_GOLDEN") != "" {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, _ := os.ReadFile(golden)
			if got != string(want) {
				t.Errorf("%s's screens changed (GOTOGETHER_UPDATE_GOLDEN=1 rewrites %s, if they should):\n%s", tape, golden, got)
			}
		})
	}
}

// reOutput is a tape's outputs and screenshots, which the test replaces
// with its text file (read-only).
var reOutput = regexp.MustCompile(`(?m)^(Output|Screenshot) .*$`)

// screens are the navigator's distinct screens in a tape's text output,
// in order: its frames (one a command), without the shell's, without
// those of a node being fetched (their timing is the machine's), each
// shown once while it lasts.
func screens(b []byte) string {
	sep := regexp.MustCompile(`(?m)^─{80}$\n?`)
	var out []string
	for _, f := range sep.Split(string(b), -1) {
		var ls []string
		for l := range strings.SplitSeq(f, "\n") {
			ls = append(ls, strings.TrimRight(l, " "))
		}
		f = strings.TrimRight(strings.Join(ls, "\n"), "\n")
		if !strings.HasPrefix(strings.TrimLeft(f, "\n"), "gotogether-nav  ") || strings.Contains(f, "working…") || strings.Contains(f, "loading…") {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == f {
			continue
		}
		out = append(out, f)
	}
	return string(bytes.Join(func() [][]byte {
		var bs [][]byte
		for _, s := range out {
			bs = append(bs, []byte(s))
		}
		return bs
	}(), []byte("\n\n========\n\n"))) + "\n"
}
