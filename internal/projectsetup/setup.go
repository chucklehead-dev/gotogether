// Package projectsetup runs a new project's dependency and generation steps,
// preserving its files and describing how to continue if a step fails.
package projectsetup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// Plan is an ordered sequence of Go argument vectors, without the executable.
// Verification is suggested after setup, never executed by Run.
type Plan struct {
	Label string
	Dir   string
	Steps [][]string
}

// Run executes setup outside the surrounding Go workspace. Failure retains all
// project files and the original subprocess error, with a concrete continuation.
func (p Plan) Run(output io.Writer) error {
	for i, args := range p.Steps {
		cmd := exec.Command("go", args...)
		cmd.Dir = p.Dir
		cmd.Stdout, cmd.Stderr = output, output
		cmd.Env = withoutWorkspace()
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: setup incomplete; project files retained in %s: go %s: %w\nresume: %s", p.Label, p.Dir, strings.Join(args, " "), err, p.Resume(i))
		}
	}
	return nil
}

func withoutWorkspace() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, v := range env {
		if !strings.HasPrefix(v, "GOWORK=") {
			out = append(out, v)
		}
	}
	return append(out, "GOWORK=off")
}

// Resume renders a POSIX shell continuation from the specified setup stage,
// followed by verification. It does not rerun initialization or rewrite files.
func (p Plan) Resume(from int) string {
	if from < 0 {
		from = 0
	}
	if from > len(p.Steps) {
		from = len(p.Steps)
	}
	commands := []string{"cd -- " + shellQuote(absolute(p.Dir))}
	for _, args := range p.Steps[from:] {
		commands = append(commands, goCommand(args))
	}
	commands = append(commands, goCommand([]string{"test", "-short", "-v", "./..."}))
	return "(" + strings.Join(commands, " && ") + ")"
}

// Verify renders only the verification command for a successful setup.
func Verify(dir string) string { return (Plan{Dir: dir}).Resume(0) }

func absolute(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func goCommand(args []string) string {
	words := []string{"GOWORK=off", "go"}
	for _, arg := range args {
		words = append(words, shellQuote(arg))
	}
	return strings.Join(words, " ")
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:@=+-", r))
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// CheckGo checks executable availability before automatic setup writes files.
func CheckGo() error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("Go is required for automatic setup: %w", err)
	}
	return nil
}

// CheckModule validates main-module paths, including local paths without a
// domain. go and toolchain are Go directive names rather than module paths.
func CheckModule(path string) error {
	if path == "go" || path == "toolchain" {
		return fmt.Errorf("module path %q is reserved by Go", path)
	}
	if err := module.CheckImportPath(path); err != nil {
		return fmt.Errorf("invalid module path %q: %w", path, err)
	}
	if _, _, ok := module.SplitPathVersion(path); !ok {
		return fmt.Errorf("invalid module path %q: invalid major-version suffix (use /v2 or later; gopkg.in paths require .vN)", path)
	}
	return nil
}

// ModulePath quotes a path as a go.mod token, including replacement paths.
func ModulePath(path string) string { return modfile.AutoQuote(path) }

// CheckDestination permits an absent or readable empty directory. Other
// filesystem errors remain errors rather than permission to start writing.
func CheckDestination(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		if _, linkErr := os.Lstat(dir); linkErr == nil {
			return fmt.Errorf("destination %s exists but cannot be read: %w", dir, err)
		} else if !os.IsNotExist(linkErr) {
			return fmt.Errorf("destination %s: %w", dir, linkErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("destination %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s exists and is not empty: give another -dir", dir)
	}
	return nil
}
