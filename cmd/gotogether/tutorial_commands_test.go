package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Only native -h runs in children: tutorial arguments are inspected, never
// executed. This keeps the guard independent of models, files and installed tools.
func TestTutorialHelpProcess(t *testing.T) {
	raw := os.Getenv("GOTOGETHER_TUTORIAL_HELP")
	if raw == "" {
		return
	}
	var command string
	if err := json.Unmarshal([]byte(raw), &command); err != nil {
		t.Fatal(err)
	}
	// Preserve the exact shell command token, including whitespace. Splitting
	// it could execute a different command before the appended help switch.
	os.Args = []string{"gotogether", command}
	os.Args = append(os.Args, "-h")
	main()
}

type tutorialFlag struct{ takesValue bool }
type tutorialHelp map[string]tutorialFlag

func nativeTutorialHelp(t *testing.T, command string) (tutorialHelp, error) {
	t.Helper()
	raw, _ := json.Marshal(command)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestTutorialHelpProcess$")
	cmd.Env = append(os.Environ(), "GOTOGETHER_TUTORIAL_HELP="+string(raw))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s -h: %v: %s", command, err, bytes.TrimSpace(out))
	}
	flags := tutorialHelp{}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "  -") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 1 || len(fields) > 2 {
			return nil, fmt.Errorf("unrecognized help flag line %q", line)
		}
		flags[strings.TrimPrefix(fields[0], "-")] = tutorialFlag{takesValue: len(fields) == 2}
	}
	// -h/-help are implemented by flag.Parse and aren't registered flags.
	flags["h"] = tutorialFlag{}
	flags["help"] = tutorialFlag{}
	return flags, nil
}

func checkTutorialArgs(args []string, flags tutorialHelp) error {
	positional := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return nil
		} // e.g. shrink's external driver owns the rest
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			positional = true
			continue
		}
		if positional {
			return fmt.Errorf("flag %s follows a positional argument; Go stops parsing flags there", arg)
		}
		name, _, assigned := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		f, ok := flags[name]
		if !ok {
			return fmt.Errorf("unknown flag %s", arg)
		}
		if f.takesValue && !assigned {
			i++
			if i == len(args) {
				return fmt.Errorf("flag %s needs a value", arg)
			}
			// A value beginning '-' is legal: it must not become another option.
		}
	}
	return nil
}

// shellWords handles the shell's quoting and separators used by these tutorials.
// Substitutions remain opaque words: this is inspection, not expansion. The
// unsupported compound syntax is rejected rather than silently dropping a CLI.
type tutorialShellToken struct {
	text     string
	operator bool
}

func tutorialShellWords(line string) ([]tutorialShellToken, error) {
	var words []tutorialShellToken
	var word strings.Builder
	started := false
	plain := true
	flush := func() {
		if started {
			words = append(words, tutorialShellToken{text: word.String()})
			word.Reset()
			started = false
			plain = true
		}
	}
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && quote != '\'' {
			plain = false
			if i+1 == len(line) {
				return nil, fmt.Errorf("unfinished shell continuation")
			}
			if quote == '"' && !strings.ContainsRune("$`\\\"", rune(line[i+1])) {
				word.WriteByte(c)
				started = true
				continue
			}
			i++
			word.WriteByte(line[i])
			started = true
			continue
		}
		if c == quote && quote != 0 {
			quote = 0
			continue
		}
		if c == '\'' || c == '"' {
			if quote == 0 {
				quote = c
				plain = false
				started = true
				continue
			}
		}
		if c == '$' && i+1 < len(line) && line[i+1] == '(' && quote != '\'' {
			end, err := tutorialSubstitutionEnd(line, i+2)
			if err != nil {
				return nil, err
			}
			nested, err := tutorialInvocations(line[i+2 : end])
			if err != nil {
				return nil, fmt.Errorf("command substitution: %w", err)
			}
			if len(nested) > 0 {
				return nil, fmt.Errorf("gotogether in command substitution is outside this guard's scope")
			}
			plain = false
			word.WriteString(line[i : end+1])
			started = true
			i = end
			continue
		}
		if c == '`' && quote != '\'' {
			return nil, fmt.Errorf("backtick expansion is outside this guard's scope")
		}
		if quote == 0 {
			if c == '#' && !started {
				break
			}
			if c == ' ' || c == '\t' {
				flush()
				continue
			}
			if strings.ContainsRune("|&;><()", rune(c)) || ((c == '{' || c == '}') && !started && (i+1 == len(line) || strings.ContainsRune(" \t;", rune(line[i+1])))) {
				fd := ""
				if (c == '>' || c == '<') && started && plain && tutorialFD.MatchString(word.String()) {
					fd = word.String()
					word.Reset()
					started = false
				}
				flush()
				op := fd + string(c)
				if i+1 < len(line) && (line[i+1] == c || ((c == '>' || c == '<') && line[i+1] == '&')) {
					op += string(line[i+1])
					i++
				}
				words = append(words, tutorialShellToken{text: op, operator: true})
				continue
			}
		}
		word.WriteByte(c)
		started = true
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed shell quote")
	}
	flush()
	return words, nil
}

func tutorialSubstitutionEnd(s string, start int) (int, error) {
	depth, quote := 1, byte(0)
	for i := start; i < len(s); i++ {
		c := s[i]
		if c == '\\' && quote != '\'' {
			i++
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("unclosed command substitution")
}

var tutorialFD = regexp.MustCompile(`^[0-9]+$`)
var tutorialRedirection = regexp.MustCompile(`^[0-9]*(>|>>|<|>&|<&)$`)

var tutorialAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func tutorialInvocations(line string) ([][]string, error) {
	words, err := tutorialShellWords(line)
	if err != nil {
		return nil, err
	}
	var out [][]string
	var simple []string
	check := func() error {
		args := simple
		for len(args) > 0 {
			if tutorialAssignment.MatchString(args[0]) || args[0] == "time" || args[0] == "env" {
				args = args[1:]
				continue
			}
			break
		}
		if len(args) >= 3 && args[0] == "go" && args[1] == "tool" && args[2] == "gotogether" {
			args = args[2:]
		}
		if len(args) > 0 && args[0] == "gotogether" {
			if len(args) < 2 {
				return fmt.Errorf("gotogether needs a documented command")
			}
			out = append(out, append([]string(nil), args[1:]...))
		} else if len(args) > 0 && args[0] != "cd" {
			for _, arg := range args {
				if arg == "gotogether" {
					return fmt.Errorf("unsupported gotogether wrapper in %q", line)
				}
			}
		}
		simple = nil
		return nil
	}
	for i := 0; i < len(words); i++ {
		token := words[i]
		word := token.text
		if !token.operator {
			simple = append(simple, word)
			continue
		}
		if tutorialRedirection.MatchString(word) {
			i++
			if i == len(words) || words[i].operator {
				return nil, fmt.Errorf("missing redirection target")
			}
			continue
		}
		switch word {
		case "|", "||", "&&", ";", "&", "(", ")", "{", "}":
			if err := check(); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported shell operator %q", word)
		}
	}
	if err := check(); err != nil {
		return nil, err
	}
	return out, nil
}

// Console output can itself recommend gotogether commands. Inspect only '$'
// input lines there, and all lines in sh/bash fences. Other fences/prose are
// deliberately excluded: this checks syntax freshness, not expected output.
func tutorialCommands(markdown string) (map[int][][]string, error) {
	out := map[int][][]string{}
	language := ""
	lines := strings.Split(markdown, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "```") {
			if language == "" {
				language = strings.TrimPrefix(line, "```")
			} else {
				language = ""
			}
			continue
		}
		if language != "sh" && language != "bash" && language != "console" {
			continue
		}
		if language == "console" && !strings.HasPrefix(line, "$ ") {
			continue
		}
		line = strings.TrimPrefix(line, "$ ")
		start := i + 1
		for (len(line)-len(strings.TrimRight(line, "\\")))%2 == 1 {
			if i+1 == len(lines) {
				return nil, fmt.Errorf("line %d: unfinished continuation", start)
			}
			i++
			line = strings.TrimSuffix(line, "\\") + " " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "> "))
		}
		commands, err := tutorialInvocations(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", start, err)
		}
		if len(commands) > 0 {
			out[start] = commands
		}
	}
	return out, nil
}

func TestTutorialCommandsAreCurrent(t *testing.T) {
	paths, err := filepath.Glob("../../docs/tutorials/*.md")
	if err != nil || len(paths) == 0 {
		t.Fatalf("tutorials: %v, %v", paths, err)
	}
	cache := map[string]tutorialHelp{}
	count := 0
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		commands, err := tutorialCommands(string(text))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		lines := make([]int, 0, len(commands))
		for line := range commands {
			lines = append(lines, line)
		}
		slices.Sort(lines)
		for _, line := range lines {
			invocations := commands[line]
			for _, invocation := range invocations {
				command, args := invocation[0], invocation[1:]
				flags, ok := cache[command]
				if !ok {
					flags, err = nativeTutorialHelp(t, command)
					if err != nil {
						t.Errorf("%s:%d: %v", path, line, err)
						continue
					}
					cache[command] = flags
				}
				count++
				if err := checkTutorialArgs(args, flags); err != nil {
					t.Errorf("%s:%d: gotogether %s: %v", path, line, strings.Join(invocation, " "), err)
				}
			}
		}
	}
	if count == 0 {
		t.Fatal("no tutorial gotogether commands checked")
	}
	t.Logf("checked %d invocations across %d tutorial pages using %d native command help pages", count, len(paths), len(cache))
}

func TestTutorialCommandGuardControls(t *testing.T) {
	flags, err := nativeTutorialHelp(t, "simulate")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-graph", "-binding", "a.yaml", "-depth=3"}, {"--binding=-starts-with-dash", "-const", "REQUESTS=1.to(12)"}, {"-binding", "-literal-value", "-graph"}, {"-binding", "-graph"}} {
		if err := checkTutorialArgs(args, flags); err != nil {
			t.Errorf("valid %q: %v", args, err)
		}
	}
	for _, args := range [][]string{{"-grap"}, {"-binding"}, {"a.yaml", "-graph"}, {"-graph", "false", "-depth", "3"}} {
		if err := checkTutorialArgs(args, flags); err == nil {
			t.Errorf("accepted stale/misplaced flags %q", args)
		}
	}
	validateFlags, err := nativeTutorialHelp(t, "validate")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkTutorialArgs([]string{"-noitf"}, validateFlags); err == nil {
		t.Error("accepted hidden removed flag")
	}
	for _, command := range []string{"simlate", "init should-never-be-created", "skills install", "simulate -h"} {
		if _, err := nativeTutorialHelp(t, command); err == nil {
			t.Errorf("accepted unknown command %q", command)
		}
	}
	if err := checkTutorialArgs([]string{"-binding", "a.yaml", "--", "driver", "-driver-owned"}, flags); err != nil {
		t.Fatal(err)
	}
}

func TestTutorialShellControls(t *testing.T) {
	line := `echo before && time env PATH="$(go env GOROOT)/bin:/usr/bin" go tool gotogether validate -binding 'with spaces.yaml' data.jsonl | grep -v '^ -fake'; gotogether simulate -const 'A=1.to(12)' -graph`
	got, err := tutorialInvocations(line)
	want := [][]string{{"validate", "-binding", "with spaces.yaml", "data.jsonl"}, {"simulate", "-const", "A=1.to(12)", "-graph"}}
	b, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if err != nil || !bytes.Equal(b, w) {
		t.Fatalf("invocations = %s, %v; want %s", b, err, w)
	}
	for _, executable := range []string{`gotoge\ther`, `'goto''gether'`} {
		commands, err := tutorialCommands("```sh\n" + executable + " simulate -bogus\n```\n")
		if err != nil || len(commands[2]) != 1 || commands[2][0][1] != "-bogus" {
			t.Errorf("escaped executable %q was skipped: %v, %v", executable, commands, err)
		}
	}
	for _, value := range []string{`'2'`, `2 `, `\2`} {
		invocations, err := tutorialInvocations("gotogether simulate -binding " + value + ">out -bogus")
		if err != nil || len(invocations) != 1 || len(invocations[0]) != 4 || invocations[0][2] != "2" {
			t.Fatalf("numeric redirection prefix %q: %v, %v", value, invocations, err)
		}
	}
	for _, value := range []string{"|", "&&", ">", "2>"} {
		got, err := tutorialInvocations("gotogether simulate -binding '" + value + "' -graph 2>&1 > out.txt")
		if err != nil || len(got) != 1 || len(got[0]) != 4 || got[0][2] != value || got[0][3] != "-graph" {
			t.Fatalf("quoted operator %q: %v, %v", value, got, err)
		}
	}
	for _, line := range []string{`gotogether simulate -binding 'broken`, `sudo gotogether simulate -graph`, `echo $(gotogether simulate -graph)`, "echo \"`gotogether simlate -bad`\""} {
		if _, err := tutorialInvocations(line); err == nil {
			t.Errorf("accepted unsupported syntax %q", line)
		}
	}
	md := "```console\n$ gotogether simulate -graph \\\n> -binding x.yaml\ngotogether removed-command -old\n```\n```go\ngotogether removed-command\n```\n```sh\n$ gotogether simulate -graph\ngotogether simulate -graph\n```\n"
	commands, err := tutorialCommands(md)
	if err != nil || len(commands) != 3 || len(commands[2]) != 1 {
		t.Fatalf("prompt/continuation selection = %v, %v", commands, err)
	}
}

func TestCommandHelpFlagTypes(t *testing.T) {
	fs := commandFlags("validate")
	fs.String("source", "", "source `FILE` to inspect")
	fs.Int("count", 0, "number to inspect")
	fs.Duration("wait", 0, "time to wait")
	fs.Bool("enabled", false, "enable inspection")
	var paths repeated
	fs.Var(&paths, "path", "path to inspect (repeatable)")
	var out bytes.Buffer
	fs.SetOutput(&out)
	fs.Usage()
	for _, line := range []string{"  -source FILE\n", "  -count int\n", "  -wait duration\n", "  -enabled\n", "  -path value\n", "source FILE to inspect"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("help missing %q:\n%s", line, out.String())
		}
	}
	if strings.Contains(out.String(), "`FILE`") {
		t.Error("help kept value-name quoting instead of standard flag usage")
	}
}
