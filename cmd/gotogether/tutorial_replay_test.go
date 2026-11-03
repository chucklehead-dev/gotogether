package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// This is intentionally one walkthrough, not a shell interpreter: the checked
// console blocks run only hello's sampled and complete-graph commands. Setup,
// later mutations and native counterexample replay remain separate coverage.
type tutorialReplay struct {
	args   []string
	output string
}

func allowedHelloReplayArgs(args []string) bool {
	return slices.Equal(args, []string{"simulate", "-binding", "hello.binding.yaml"}) ||
		slices.Equal(args, []string{"simulate", "-graph", "-binding", "hello.binding.yaml"})
}

func helloReplayArgs(command string) ([]string, error) {
	tokens, err := tutorialShellWords(command)
	if err != nil {
		return nil, err
	}
	var words []string
	for _, token := range tokens {
		if token.operator || strings.ContainsAny(token.text, "$`") {
			return nil, fmt.Errorf("hello replay does not execute shell operators or expansions")
		}
		words = append(words, token.text)
	}
	if len(words) == 0 || words[0] != "gotogether" {
		return nil, fmt.Errorf("hello replay needs a gotogether command")
	}
	args := words[1:]
	if !allowedHelloReplayArgs(args) {
		return nil, fmt.Errorf("command is outside the two checked hello invocations: %q", command)
	}
	return args, nil
}

func readHelloReplay(markdown, name string) (tutorialReplay, error) {
	marker := "<!-- tutorial-check: " + name + " -->"
	if strings.Count(markdown, marker) != 1 {
		return tutorialReplay{}, fmt.Errorf("need exactly one %s marker", name)
	}
	_, after, _ := strings.Cut(markdown, marker)
	lines := strings.Split(strings.TrimLeft(after, "\n"), "\n")
	if len(lines) == 0 || lines[0] != "```console" {
		return tutorialReplay{}, fmt.Errorf("%s marker must immediately precede a console fence", name)
	}
	var commands, output []string
	closed := false
	for _, line := range lines[1:] {
		if line == "```" {
			closed = true
			break
		}
		if strings.HasPrefix(line, "$ ") {
			if len(output) > 0 {
				return tutorialReplay{}, fmt.Errorf("%s must contain one command's complete output", name)
			}
			commands = append(commands, strings.TrimPrefix(line, "$ "))
		} else {
			output = append(output, line)
		}
	}
	if !closed || len(commands) == 0 || len(output) == 0 {
		return tutorialReplay{}, fmt.Errorf("%s needs a closed console block with a command and output", name)
	}
	if name == "hello-sampled" {
		if len(commands) != 2 || commands[0] != "cd examples/hello" {
			return tutorialReplay{}, fmt.Errorf("hello-sampled must first change from the checkout root to examples/hello")
		}
		commands = commands[1:]
	}
	if len(commands) != 1 {
		return tutorialReplay{}, fmt.Errorf("%s may only change to examples/hello and run one invocation", name)
	}
	args, err := helloReplayArgs(commands[0])
	if err != nil {
		return tutorialReplay{}, fmt.Errorf("%s: %w", name, err)
	}
	graph := slices.Contains(args, "-graph")
	if graph != (name == "hello-graph") {
		return tutorialReplay{}, fmt.Errorf("%s has the wrong sampled/graph command", name)
	}
	quoted := strings.Join(output, "\n") + "\n"
	if strings.Contains(quoted, "[...]") {
		return tutorialReplay{}, fmt.Errorf("%s must quote complete output, without truncation markers", name)
	}
	return tutorialReplay{args: args, output: quoted}, nil
}

// The graph's elapsed wall time is the only volatile field in these blocks.
// State/transition counts, proof scope, sampling bounds and witnesses stay exact.
var helloGraphElapsed = regexp.MustCompile(`(?m)^(  graph   complete: [0-9]+ states(?: \([0-9]+ initial\))?, [0-9]+ transitions, depth [0-9]+), [0-9]+(?:\.[0-9]+)?s$`)

func checkHelloReplayOutput(expected, actual string, runErr error) error {
	if runErr != nil {
		return fmt.Errorf("native CLI failed, regardless of its printed output: %w", runErr)
	}
	normalize := func(s string) string { return helloGraphElapsed.ReplaceAllString(s, "${1}, <elapsed>s") }
	if normalize(expected) != normalize(actual) {
		return fmt.Errorf("tutorial output drifted\n--- quoted ---\n%s--- actual ---\n%s", expected, actual)
	}
	return nil
}

// Only the two allowlisted native CLI argument lists can reach main; no shell
// or general documentation command dispatcher is involved, even in the child.
func TestTutorialReplayProcess(t *testing.T) {
	raw := os.Getenv("GOTOGETHER_HELLO_REPLAY")
	if raw == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	if !allowedHelloReplayArgs(args) {
		t.Fatal("hello replay child received arguments outside its scope")
	}
	os.Args = append([]string{"gotogether"}, args...)
	main()
}

func TestHelloTutorialReplay(t *testing.T) {
	skip.Quint(t)
	document, err := os.ReadFile("../../docs/tutorials/prove-it.md")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "tutorial workspace")
	dir := filepath.Join(root, "examples", "hello")
	for _, file := range []string{"hello.binding.yaml", "model/hello.qnt"} {
		data, err := os.ReadFile(filepath.Join("../../examples/hello", file))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	scratch := filepath.Join(root, "tmp")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hello-sampled", "hello-graph"} {
		t.Run(name, func(t *testing.T) {
			replay, err := readHelloReplay(string(document), name)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(replay.args)
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestTutorialReplayProcess$")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOGETHER_HELLO_REPLAY="+string(raw), "GOTOGETHER_CACHE="+filepath.Join(root, "cache"), "TMPDIR="+scratch)
			actual, runErr := cmd.CombinedOutput()
			if err := checkHelloReplayOutput(replay.output, string(actual), runErr); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHelloTutorialReplayControls(t *testing.T) {
	expected := "  graph   complete: 8 states, 25 transitions, depth 3, 0.0s\n  holds   exactlyOnce: no reachable state violates it (the whole graph, 8 states)\n  possible retry: action sequence executed by apply(id = 1), replay(id = 1)\n"
	if err := checkHelloReplayOutput(expected, strings.Replace(expected, "0.0s", "42.7s", 1), nil); err != nil {
		t.Fatal(err)
	}
	for _, actual := range []string{
		strings.Replace(expected, "8 states", "9 states", 1),
		strings.Replace(expected, "25 transitions", "24 transitions", 1),
		strings.Replace(expected, "depth 3", "depth 2", 1),
		strings.Replace(expected, "complete:", "bounded:", 1),
		strings.Replace(expected, "the whole graph", "a sampled graph", 1),
		strings.Replace(expected, "apply(id = 1)", "apply(id = 2)", 1),
		strings.Split(expected, "\n")[0] + "\n",
		expected + "extra output\n",
	} {
		if err := checkHelloReplayOutput(expected, actual, nil); err == nil {
			t.Errorf("accepted changed or incomplete output %q", actual)
		}
	}
	if err := checkHelloReplayOutput(expected, expected, errors.New("exit status 1")); err == nil {
		t.Error("accepted complete-looking output from a failed native CLI")
	}
	for _, command := range []string{
		"gotogether simulate -binding ../../outside.yaml",
		"gotogether simulate -binding hello.binding.yaml; touch escaped",
		"gotogether simulate -binding $(echo hello.binding.yaml)",
		"gotogether init anything",
	} {
		if _, err := helloReplayArgs(command); err == nil {
			t.Errorf("accepted command outside the walkthrough %q", command)
		}
	}
	markdown := "<!-- tutorial-check: hello-graph -->\n\n```console\n$ gotogether simulate -graph -binding hello.binding.yaml\n" + expected + "```\n"
	if _, err := readHelloReplay(markdown, "hello-graph"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		markdown + markdown,
		strings.Replace(markdown, "  holds", "[...]\n  holds", 1),
		strings.Replace(markdown, "```console", "```sh", 1),
		strings.Replace(markdown, " -graph", "", 1),
	} {
		if _, err := readHelloReplay(bad, "hello-graph"); err == nil {
			t.Error("accepted malformed, duplicate or truncated replay block")
		}
	}
	sampled := "<!-- tutorial-check: hello-sampled -->\n\n```console\n$ cd examples/hello\n$ gotogether simulate -binding hello.binding.yaml\nsampled output\n```\n"
	if _, err := readHelloReplay(sampled, "hello-sampled"); err != nil {
		t.Fatal(err)
	}
	if _, err := readHelloReplay(strings.Replace(sampled, "$ cd examples/hello\n", "", 1), "hello-sampled"); err == nil {
		t.Error("accepted sampled command without its working-directory setup")
	}
}
