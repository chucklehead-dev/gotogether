package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/internal/overlay"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// nav is the navigator (P5): a model's states (-binding, from init or a
// recorded run's step -at k), or an exploration's run, by its choices
// (-run 'TestX#n:seed=s', as a failing run's report names it: the test
// binary navigates it, GOTOGETHER_NAV). One node per call, printed for a
// person or (-json) as one JSON object: the node, its path, its state,
// its branches and its disabled actions with why.
func nav(out io.Writer, args []string) (int, error) {
	fset := commandFlags("nav")
	bind := fset.String("binding", "", "the binding whose model to navigate (from init, or with -run a step log: the state after its first -at calls)")
	run := fset.String("run", "", "a recorded run's step log (with -binding), or an exploration's run as a failing run's report names it ('TestX#2:seed=17', run in its package)")
	at := fset.String("at", "", "where to open: with a step log, how many of the actor's calls to take (default all); with an exploration's run, a node's id (<schedule>@<k>, @<k> the run's own first k choices), start, or fail (the last choice before the failing step)")
	actor := fset.String("actor", "", "with a step log: the actor to navigate (default the only one)")
	follow := fset.String("follow", "", "a path from the node: a model's transitions as its branches' take names them, comma-separated (admit(\"a\"),release(\"a\"); #n the n-th branch); a run's options (1,0,2)")
	take := fset.String("take", "", "one more branch to take after -follow")
	pkg := fset.String("pkg", ".", "with an exploration's run: the test's package (go test runs in it)")
	save := fset.Bool("save", false, "with an exploration's run: save the node's run to the exploration's corpus (ExploreConfig.Corpus)")
	fold := fset.Bool("fold", false, "fold the node's linear stretch: a model's single branches taken until a node of several; a run's choices that are no decision (nominal where the base run's are) passed to its next")
	asJSON := fset.Bool("json", false, "print the node as one JSON object (the agents' form)")
	text := fset.Bool("text", false, "print the node as text even on a terminal, where gotogether-nav (the interactive navigator) otherwise opens it")
	quint := fset.String("quint", "quint", "quint binary")
	fset.Parse(args)
	if !*asJSON && !*text && isTerminal(out) {
		if code, ran, err := navTUI(args); ran {
			return code, err
		}
	}
	path := *follow
	if *take != "" {
		if path != "" {
			path += ","
		}
		path += *take
	}
	if *run != "" && isRunRef(*run) {
		if *bind != "" || *actor != "" {
			return 2, fmt.Errorf("nav -run %s: an exploration's run is the test's, judged by its binding: drop -binding and -actor", *run)
		}
		node, err := navRun(*run, *pkg, *at, path, *save, *fold)
		if err != nil {
			return 2, err
		}
		return printNode(out, node, *asJSON, runNodeText(node))
	}
	if *bind == "" {
		return 2, fmt.Errorf("nav: give -binding (a model), with -run a step log, or -run an exploration's run ('TestX#2:seed=17')")
	}
	c, err := validate.NewChecker(*bind, validate.Options{Quint: *quint})
	if err != nil {
		return 2, err
	}
	defer c.Close()
	start := validate.NavStart{Actor: *actor, At: -1}
	if *run != "" {
		f, err := input.Read(*run, input.Unknown)
		if err != nil {
			return 2, err
		}
		start.Steps = f.Steps
		if start.Steps == nil {
			start.Steps = []qtrace.Step{}
		}
		if *at != "" {
			if _, err := fmt.Sscan(*at, &start.At); err != nil {
				return 2, fmt.Errorf("nav -at %q: with a step log, how many calls to take", *at)
			}
		}
	} else if *at != "" || *actor != "" {
		return 2, fmt.Errorf("nav: -at and -actor open a recorded run: add -run steps.jsonl")
	}
	n, err := c.Nav(start)
	if err != nil {
		return 2, err
	}
	walk := n.Node
	if *fold {
		walk = n.Fold
	}
	node, err := walk(validate.SplitPath(path)...)
	if err != nil {
		return 2, err
	}
	return printNode(out, node, *asJSON, node.String())
}

// tuiModule is the interactive navigator's module, which `gotogether
// nav` on a terminal runs when its binary is on PATH (read-only).
const tuiModule = "github.com/chucklehead-dev/gotogether/qnav"

// navTUI runs gotogether-nav (the interactive navigator, a module of its
// own: Charm's libraries stay out of the core) with nav's arguments, on
// the terminal: ran false when it is not installed, after saying how to
// install it (the node is then printed as text).
func navTUI(args []string) (code int, ran bool, err error) {
	bin, err := exec.LookPath("gotogether-nav")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotogether nav: the interactive navigator is not installed (go install %s/cmd/gotogether-nav@latest); printing the node (-text says so without this line)\n", tuiModule)
		return 0, false, nil
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), true, nil
		}
		return 2, true, fmt.Errorf("gotogether nav: %s: %w", bin, err)
	}
	return 0, true, nil
}

// isTerminal reports whether out is a terminal (a character device).
func isTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// isRunRef reports whether s names an exploration's run (TestX#n:kind=v)
// rather than a file.
func isRunRef(s string) bool {
	if _, err := os.Stat(s); err == nil {
		return false
	}
	return reRunRef.MatchString(s)
}

var reRunRef = regexp.MustCompile(`#\d+:(seed|schedule|corpus|choices)=`) // read-only

func printNode(out io.Writer, node any, asJSON bool, text string) (int, error) {
	if !asJSON {
		fmt.Fprint(out, text)
		return 0, nil
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(node); err != nil {
		return 2, err
	}
	return 0, nil
}

// navRun has the test binary of ref's test navigate it: go test in pkg
// with GOTOGETHER_RUN naming the run and GOTOGETHER_NAV the node, built
// with the overlay of the mutant GOTOGETHER_MUTANT names.
func navRun(ref, pkg, at, follow string, save, fold bool) (*validate.RunNode, error) {
	i := strings.LastIndex(ref[:strings.LastIndex(ref, ":")], "#")
	test := ref[:i]
	dir, err := os.MkdirTemp("", "gotogether-nav-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	outFile := filepath.Join(dir, "node.json")
	if at == "" {
		at = "start"
	}
	pattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(test), "/", "$/^") + "$"
	args := []string{"test", "-count=1"}
	// a mutant's run (GOTOGETHER_MUTANT, as its navigate line sets it):
	// its test binary built with its overlay, if it has one
	if o, err := overlay.Of(pkg, overlay.Mutant(), dir); err != nil {
		return nil, fmt.Errorf("nav -run %s: %w", ref, err)
	} else if o != "" {
		args = append(args, "-overlay", o)
	}
	cmd := exec.Command("go", append(args, "-run", pattern, ".")...)
	cmd.Dir = pkg
	cmd.Env = append(os.Environ(), "GOTOGETHER_RUN="+ref, "GOTOGETHER_NAV="+at, "GOTOGETHER_NAV_FOLLOW="+follow, "GOTOGETHER_NAV_OUT="+outFile)
	if save {
		cmd.Env = append(cmd.Env, "GOTOGETHER_NAV_SAVE=1")
	}
	if fold {
		cmd.Env = append(cmd.Env, "GOTOGETHER_NAV_FOLD=1")
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	runErr := cmd.Run()
	b, err := os.ReadFile(outFile)
	if err != nil {
		hint := ""
		if overlay.Mutant() == "" {
			// a mutant's run in the system as it is: its TestMutant skips
			if ms, _ := filepath.Glob(filepath.Join(pkg, filepath.FromSlash(overlay.Dir), "*")); len(ms) > 0 {
				hint = fmt.Sprintf("; a mutant's run navigates in its mutant, %s=<name> as the run's navigate line sets it (this package's: %s)", overlay.Env, strings.Join(names(ms), ", "))
			}
		}
		return nil, fmt.Errorf("nav -run %s: the test wrote no node (go test -run '%s' in %s: %v%s)\n%s", ref, pattern, pkg, runErr, hint, buf.String())
	}
	var n validate.RunNode
	if err := json.Unmarshal(b, &n); err != nil {
		return nil, fmt.Errorf("nav -run %s: %w", ref, err)
	}
	if err := validate.CheckNavProtocol(n.Protocol); err != nil {
		return nil, fmt.Errorf("nav -run %s: %w", ref, err)
	}
	return &n, nil
}

// names are paths' base names.
func names(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

// runNodeText is a run node as a person reads it.
func runNodeText(n *validate.RunNode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "node %s of %s: %s\n", n.ID, n.Run, n.Verdict)
	if n.Folded > 0 {
		fmt.Fprintf(&b, "  folded %d choices that were no decision\n", n.Folded)
	}
	if n.Elapsed != "" {
		fmt.Fprintf(&b, "  at %s, after %d choices\n", n.Elapsed, n.At)
	}
	fmt.Fprintf(&b, "  steps so far (%d):\n", len(n.Steps))
	for _, s := range n.Steps {
		fmt.Fprintf(&b, "    %s\n", s)
	}
	if len(n.Tasks) > 0 {
		fmt.Fprintf(&b, "  tasks:\n")
		for _, t := range n.Tasks {
			fmt.Fprintf(&b, "    %s\n", t)
		}
	}
	for _, a := range n.Model {
		if a.Error != "" {
			fmt.Fprintf(&b, "  model (%s): %s\n", a.Actor, a.Error)
			continue
		}
		var vs []string
		for _, v := range a.State {
			vs = append(vs, v.Name+" = "+v.Value)
		}
		fmt.Fprintf(&b, "  model (%s): %s\n", a.Actor, strings.Join(vs, ", "))
	}
	if c := n.Choice; c != nil {
		fmt.Fprintf(&b, "  next choice: %s, %d options (the run took %d):\n", c.Kind, c.N, c.Taken)
		for _, br := range n.Branches {
			fmt.Fprintf(&b, "    %d %s -> %s\n", br.Option, br.Name, br.Child)
		}
	}
	if n.Saved != "" {
		fmt.Fprintf(&b, "  saved to the corpus: %s\n", n.Saved)
	}
	for _, note := range n.Notes {
		fmt.Fprintf(&b, "  note: %s\n", note)
	}
	return b.String()
}
