package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Every command's -h says what it is for and gives an example; a removed
// flag (usage "gone: ...") is not listed. Each flag set in the source is
// one of commands, so none is left with Go's bare "Usage of x:".
func TestCommandHelp(t *testing.T) {
	var src []byte
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "help.go" { // help.go makes commandFlags' own
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src = append(src, b...)
	}
	if bare := regexp.MustCompile(`flag\.NewFlagSet\(`).FindAll(src, -1); len(bare) > 0 {
		t.Errorf("%d flag sets made without commandFlags: their -h has no purpose or example", len(bare))
	}
	for _, m := range regexp.MustCompile(`commandFlags\("([^"]+)"\)`).FindAllSubmatch(src, -1) {
		name := string(m[1])
		fset := commandFlags(name)
		fset.Bool("old", false, "gone: use -new")
		var b bytes.Buffer
		fset.SetOutput(&b)
		fset.Usage()
		c := commands[name]
		if !strings.Contains(b.String(), c.purpose) || !strings.Contains(b.String(), c.example) || strings.Contains(b.String(), "-old") {
			t.Errorf("%s -h:\n%s", name, b.String())
		}
	}
}

// A flag's zero default goes unsaid, and so does a default its usage
// states: "(default 0)" once followed "(default 200)".
func TestHelpDefaults(t *testing.T) {
	fset := commandFlags("validate")
	fset.Int("budget", 0, "orders to try (default 200)")
	fset.Int("n", 0, "a count")
	fset.Duration("d", 0, "a duration")
	fset.String("s", "", "a string")
	fset.Bool("b", false, "a switch")
	fset.Int("k", 3, "a count with a default")
	fset.Duration("e", 0, "a stated default (default 5s)")
	var b bytes.Buffer
	fset.SetOutput(&b)
	fset.Usage()
	out := b.String()
	for _, bad := range []string{"(default 0)", "(default 0s)", "(default false)", "(default )", "(default 200) (default"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q in:\n%s", bad, out)
		}
	}
	for _, want := range []string{"a count with a default (default 3)", "orders to try (default 200)\n", "(default 5s)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// An unknown command says so, with the nearest command when it is a
// misspelling: the usage alone did not say what was wrong. Every command
// main runs is in commandNames, so none is missing from the suggestions.
func TestUnknownCommand(t *testing.T) {
	for name, want := range map[string]string{
		"valdate":    `unknown command "valdate" (did you mean validate?); gotogether help lists the commands`,
		"Lint":       `unknown command "Lint" (did you mean lint?)`,
		"frobnicate": `unknown command "frobnicate"; gotogether help lists the commands`,
	} {
		if got := unknownCommand(name); !strings.HasPrefix(got, want) {
			t.Errorf("unknownCommand(%q) = %q, want %q", name, got, want)
		}
	}
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	sw := string(src)
	sw = sw[strings.Index(sw, "switch os.Args[1] {"):]
	sw = sw[:strings.Index(sw, "default:")]
	for _, m := range regexp.MustCompile(`case "([a-z]+)"`).FindAllStringSubmatch(sw, -1) {
		if !slices.Contains(commandNames, m[1]) {
			t.Errorf("command %s is not in commandNames", m[1])
		}
	}
}

// The command line exits 2 on an unknown command, saying which.
func TestUnknownCommandExits(t *testing.T) {
	if os.Getenv("GOTOGETHER_MAIN") == "1" {
		os.Args = []string{"gotogether", "valdate"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestUnknownCommandExits$")
	cmd.Env = append(os.Environ(), "GOTOGETHER_MAIN=1")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 || !strings.Contains(string(out), `unknown command "valdate" (did you mean validate?)`) {
		t.Errorf("exit %v:\n%s", err, out)
	}
}

// gotogether help is one line per command, the question it answers,
// and its example, every command main runs among them, the flags left to
// each command's -h: validate's 30 flags once filled eight lines of it,
// and simulate's line hid -graph.
func TestUsageOneLinePerCommand(t *testing.T) {
	var b bytes.Buffer
	usageTo(&b)
	usage := b.String()
	for _, name := range commandNames {
		if name == "help" {
			continue
		}
		if !regexp.MustCompile(`(?m)^  ` + name + ` +\S`).MatchString(usage) {
			t.Errorf("help has no line for %s:\n%s", name, usage)
		}
	}
	for _, name := range helpOrder {
		if c := commands[name]; c.short == "" || !strings.Contains(usage, c.short) {
			t.Errorf("%s's line is not its short help %q", name, c.short)
		}
	}
	if !strings.Contains(usage, "-graph") || !strings.Contains(usage, "gotogether simulate -binding stock.binding.yaml -graph") {
		t.Errorf("help does not show simulate -graph:\n%s", usage)
	}
	if strings.Contains(usage, "-linearize-budget") || strings.Count(usage, "\n") > 40 {
		t.Errorf("help lists flags (or runs past 40 lines):\n%s", usage)
	}
}

// A command run without what it needs says what, with its example: the
// whole usage printed alone did not.
func TestCommandNeeds(t *testing.T) {
	if os.Getenv("GOTOGETHER_MAIN") == "1" {
		os.Args = []string{"gotogether", "validate"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCommandNeeds$")
	cmd.Env = append(os.Environ(), "GOTOGETHER_MAIN=1")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	want := "gotogether: validate needs a binding and the FILEs to check (step logs, OTLP/JSON, ITF): gotogether validate -binding stock.binding.yaml runs/*.jsonl (gotogether validate -h lists its flags)\n"
	if !errors.As(err, &ee) || ee.ExitCode() != 2 || string(out) != want {
		t.Errorf("exit %v:\n%s\nwant:\n%s", err, out, want)
	}
}

// scaffold -h shows both scaffolds: the binding's and -system's test.
func TestScaffoldHelpExamples(t *testing.T) {
	fset := commandFlags("scaffold")
	var b bytes.Buffer
	fset.SetOutput(&b)
	fset.Usage()
	for _, want := range []string{"  gotogether scaffold -spec model/stock.qnt", "  gotogether scaffold -system -binding stock.binding.yaml"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("want %q in:\n%s", want, b.String())
		}
	}
}

// The top-level usage names grist, the framework whose model is written
// for the app: a newcomer starting a service found it nowhere in the CLI.
func TestUsageNamesGrist(t *testing.T) {
	var b bytes.Buffer
	usageTo(&b)
	if !strings.Contains(b.String(), "grist/cmd/grist@latest new <name>") {
		t.Errorf("the usage does not say how to start a grist app:\n%s", b.String())
	}
}
