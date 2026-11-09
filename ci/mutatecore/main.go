// Command mutatecore runs a core package's mutants against all descendant tests.
package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type source struct {
	file    string
	ordinal int
}
type mutant struct {
	source
	descriptor string
	id         int
}
type suite struct {
	pkg, dir, binary string
	mutants          []mutant
}
type target struct{ suite, id int }
type candidate struct {
	mutant
	targets []target
}

const coreModule = "github.com/chucklehead-dev/gotogether"

// inventoryLine is read-only after initialization.
var inventoryLine = regexp.MustCompile(`^qmut: mutant ([0-9]+) ([^ ]+):([0-9]+) (.+)$`)

func inventory(output string) ([]mutant, error) {
	var result []mutant
	counts := map[string]int{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "qmut:") {
			continue
		}
		m := inventoryLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("malformed inventory: %q", line)
		}
		id, err := strconv.Atoi(m[1])
		if err != nil || id != len(result) {
			return nil, fmt.Errorf("noncontiguous mutant ID in %q", line)
		}
		file := m[2]
		result = append(result, mutant{source{file, counts[file]}, strings.TrimPrefix(line, "qmut: mutant "+m[1]+" "), id})
		counts[file]++
	}
	return result, scanner.Err()
}

// admit requires every unmutated baseline before running any mutant. Generated
// registration order within a file is stable across binaries, unlike ID bases.
// Full descriptors and per-file counts detect inconsistent source inventories.
func admit(suites []suite, baseline func(suite) error) ([]candidate, error) {
	bySource := map[source]*candidate{}
	fileCounts := map[string]int{}
	for i, s := range suites {
		if err := baseline(s); err != nil {
			return nil, fmt.Errorf("%s: unmutated baseline: %w", s.pkg, err)
		}
		counts := map[string]int{}
		for _, m := range s.mutants {
			counts[m.file]++
			c := bySource[m.source]
			if c == nil {
				c = &candidate{mutant: m}
				bySource[m.source] = c
			} else if c.descriptor != m.descriptor {
				return nil, fmt.Errorf("%s: inconsistent mutant at %s ordinal %d", s.pkg, m.file, m.ordinal)
			}
			c.targets = append(c.targets, target{i, m.id})
		}
		for file, n := range counts {
			if old, ok := fileCounts[file]; ok && old != n {
				return nil, fmt.Errorf("%s: inconsistent inventory length for %s", s.pkg, file)
			}
			fileCounts[file] = n
		}
	}
	result := make([]candidate, 0, len(bySource))
	for _, c := range bySource {
		preferOwner(c, suites)
		result = append(result, *c)
	}
	slices.SortFunc(result, func(a, b candidate) int {
		if order := cmp.Compare(a.file, b.file); order != 0 {
			return order
		}
		return cmp.Compare(a.ordinal, b.ordinal)
	})
	if len(result) == 0 {
		return nil, errors.New("no mutants woven into any test suite")
	}
	return result, nil
}

// The source-owning package's tests often kill a mutation without running
// broader consumers. Move that linked suite first, leaving other suites in
// their admitted order. A passing owner still needs every remaining suite.
func preferOwner(c *candidate, suites []suite) {
	owner := coreModule + "/" + path.Dir(c.file)
	for i, t := range c.targets {
		if suites[t.suite].pkg == owner {
			copy(c.targets[1:i+1], c.targets[:i])
			c.targets[0] = t
			return
		}
	}
}

func survives(c candidate, run func(target) (bool, error)) (bool, error) {
	for _, t := range c.targets {
		passed, err := run(t)
		if err != nil {
			return false, err
		}
		if !passed {
			return false, nil
		}
	}
	return true, nil
}

type goPackage struct {
	ImportPath, Dir           string
	TestGoFiles, XTestGoFiles []string
}

func testSuites(r io.Reader, work string) ([]suite, error) {
	dec := json.NewDecoder(r)
	var suites []suite
	for {
		var p goPackage
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
			continue
		}
		if p.ImportPath == "" || p.Dir == "" {
			return nil, errors.New("incomplete go list package")
		}
		suites = append(suites, suite{pkg: p.ImportPath, dir: p.Dir, binary: filepath.Join(work, strconv.Itoa(len(suites)), "test")})
	}
	if len(suites) == 0 {
		return nil, errors.New("no test-bearing packages")
	}
	return suites, nil
}

// Every owned tool runs in its own process group: canceling a build must stop
// qweave and compiler descendants as well as its outer go process.
func groupCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = time.Second
	return c
}

func command(ctx context.Context, dir string, args ...string) error {
	c := groupCommand(ctx, args[0], args[1:]...)
	c.Dir = dir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

type runMode uint8

const (
	inventoryMode runMode = iota
	baselineMode
	mutantMode
)

// executionResult describes the owned shell/timeout process, not why a test
// failed. Encoded timeout/signal statuses can also be explicit test exits.
type executionResult struct {
	Output       string `json:"-"`
	Disposition  string `json:"disposition"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	Signal       string `json:"signal,omitempty"`
	SignalNumber int    `json:"signal_number,omitempty"`
}

func exited(code int) executionResult {
	r := executionResult{Disposition: "exited", ExitCode: &code}
	switch {
	case code == 124:
		r.Disposition = "timeout_status"
	case code >= 125 && code <= 127:
		r.Disposition = "setup_status"
	case code > 128 && code <= 192:
		r.Disposition = "signal_status"
	}
	return r
}

func (r executionResult) String() string {
	if r.ExitCode != nil {
		return fmt.Sprintf("%s (exit %d)", r.Disposition, *r.ExitCode)
	}
	if r.Signal != "" {
		return r.Disposition + " (" + r.Signal + ")"
	}
	return r.Disposition
}

func (r executionResult) passed() bool {
	return r.Disposition == "exited" && r.ExitCode != nil && *r.ExitCode == 0
}

// validate prevents missing or contradictory execution evidence from becoming a
// resolved kill when an executor is injected into campaign orchestration.
func (r executionResult) validate() error {
	if r.ExitCode != nil && r.Signal == "" && r.SignalNumber == 0 {
		expected := exited(*r.ExitCode)
		if *r.ExitCode >= 0 && *r.ExitCode <= 255 && r.Disposition == expected.Disposition && r.Disposition != "setup_status" {
			return nil
		}
	}
	if r.Disposition == "signaled" && r.ExitCode == nil && r.SignalNumber > 0 && r.SignalNumber <= 64 && r.Signal == syscall.Signal(r.SignalNumber).String() {
		return nil
	}
	return fmt.Errorf("unresolved or contradictory execution evidence: %s", r)
}

// Positional arguments carry paths literally. timeout retains the existing
// 300-second limit; nonzero test statuses retain the operational kill policy.
func execute(ctx context.Context, s suite, id string, mem int, mode runMode) (executionResult, error) {
	return runExecution(ctx, executionCommand(ctx, s, id, mem, mode, "300"), mode != mutantMode)
}

// The explicit limit is private to command construction so controls can exercise
// the real deadline quickly; production execution always supplies 300 seconds.
func executionCommand(ctx context.Context, s suite, id string, mem int, mode runMode, limit string) *exec.Cmd {
	// TERM can be ignored indefinitely. KILL enforces the same deadline without
	// adding a grace period. timeout may report exit 124 or kill itself too;
	// neither status uniquely attributes the cause of an operational kill.
	c := groupCommand(ctx, "sh", "-c", `ulimit -v "$1" || exit 125; shift; exec timeout --signal=KILL "$@"`, "mutatecore", strconv.Itoa(mem), limit, s.binary)
	c.Dir = s.dir
	c.Env = append(os.Environ(), "GOTOGETHER_MUTANT="+id)
	if mode == inventoryMode {
		c.Args = append(c.Args, "-test.run", "^$")
	} else {
		c.Args = append(c.Args, "-test.short", "-test.failfast")
	}
	return c
}

func runExecution(ctx context.Context, c *exec.Cmd, capture bool) (executionResult, error) {
	var output []byte
	var err error
	if capture {
		output, err = c.CombinedOutput()
	} else {
		err = c.Run()
	}
	r := executionResult{Output: string(output)}
	if ctx.Err() != nil {
		r.Disposition = "canceled"
		return r, context.Cause(ctx)
	}
	if err == nil {
		r = exited(0)
	} else if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			r.Disposition, r.Signal, r.SignalNumber = "signaled", status.Signal().String(), int(status.Signal())
		} else {
			r = exited(exit.ExitCode())
		}
		r.Output = string(output)
		if r.Disposition == "setup_status" {
			return r, err
		}
		return r, nil
	} else {
		r.Disposition = "execution_error"
		return r, err
	}
	r.Output = string(output)
	return r, nil
}

func baseline(ctx context.Context, s suite, mem int) (executionResult, error) {
	result, err := execute(ctx, s, strconv.Itoa(len(s.mutants)), mem, baselineMode)
	if err != nil {
		return result, fmt.Errorf("%w\n%s", err, result.Output)
	}
	if !result.passed() {
		return result, fmt.Errorf("baseline execution %s:\n%s", result, result.Output)
	}
	return result, nil
}

func positiveEnv(name string, fallback int) (int, error) {
	s := os.Getenv(name)
	if s == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return n, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "mutate-core:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) (runErr error) {
	if len(os.Args) < 3 || len(os.Args) > 4 {
		return errors.New("usage: mutatecore root core-package [max-survivors]")
	}
	root, pkg := os.Args[1], os.Args[2]
	max := -1
	if len(os.Args) == 4 {
		var err error
		max, err = strconv.Atoi(os.Args[3])
		if err != nil || max < 0 {
			return errors.New("max-survivors must be nonnegative")
		}
	}
	jobs, err := positiveEnv("JOBS", 1)
	if err != nil {
		return err
	}
	memMB, err := positiveEnv("MEM_MB", 3072)
	if err != nil {
		return err
	}
	j, err := openJournal(root, pkg, os.Getenv("GOTOGETHER_MUTATION_LOG"))
	if err != nil {
		return fmt.Errorf("open campaign journal: %w", err)
	}
	fmt.Fprintf(os.Stderr, "mutation campaign records: %s\n", j.path)
	executionComplete := false
	defer func() {
		if runErr != nil && !executionComplete {
			if err := j.append("aborted", map[string]any{"error": runErr.Error()}); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
		if err := j.file.Close(); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	if err := j.append("start", map[string]any{"version": 1, "root": root, "package": pkg, "jobs": jobs, "mem_mb": memMB, "timeout_seconds": 300, "max_survivors": max}); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "mutate-core-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	list := groupCommand(ctx, "go", "list", "-json", "./"+pkg+"/...")
	list.Dir = root
	list.Stderr = os.Stderr
	output, err := list.Output()
	if err != nil {
		return fmt.Errorf("list packages: %w", err)
	}
	suites, err := testSuites(strings.NewReader(string(output)), work)
	if err != nil {
		return err
	}
	for i := range suites {
		s := &suites[i]
		if err := os.MkdirAll(filepath.Dir(s.binary), 0700); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "building mutation suite %s\n", s.pkg)
		err := command(ctx, root, "go", "run", "-C", filepath.Join(root, "qweave"), "./cmd/qweave", "-C", root, "-mutate", "-coverpkg", coreModule+"/"+pkg, "go", "test", "-c", "-o", s.binary, s.pkg)
		if err != nil {
			return fmt.Errorf("%s: compile: %w", s.pkg, err)
		}
		result, err := execute(ctx, *s, "list", memMB*1024, inventoryMode)
		if err != nil {
			return fmt.Errorf("%s: inventory: %w", s.pkg, err)
		}
		if !result.passed() {
			return fmt.Errorf("%s: inventory execution %s: %s", s.pkg, result.Disposition, result.Output)
		}
		s.mutants, err = inventory(result.Output)
		if err != nil {
			return fmt.Errorf("%s: %w", s.pkg, err)
		}
		if err := j.append("suite", suiteMetadata(i, *s)); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "registered %d mutations in %s\n", len(s.mutants), s.pkg)
	}
	candidates, err := admit(suites, func(s suite) error {
		started := time.Now()
		fmt.Fprintf(os.Stderr, "checking unmutated baseline %s\n", s.pkg)
		result, baselineErr := baseline(ctx, s, memMB*1024)
		record := map[string]any{"package": s.pkg, "passed": baselineErr == nil, "execution": result}
		if baselineErr != nil {
			record["error"] = baselineErr.Error()
		}
		if err := j.append("baseline", record); err != nil {
			return err
		}
		if baselineErr != nil {
			return baselineErr
		}
		fmt.Fprintf(os.Stderr, "baseline passed %s (%s)\n", s.pkg, time.Since(started).Round(time.Millisecond))
		return nil
	})
	if err != nil {
		return err
	}
	sources := make([]sourceRecord, len(candidates))
	for i, c := range candidates {
		sources[i] = sourceMetadata(c)
	}
	if err := j.append("admitted", map[string]any{"sources": sources, "suites": len(suites)}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "admitted %d source mutants across %d test suites\n", len(candidates), len(suites))
	started := time.Now()
	results, err := runCampaign(ctx, candidates, jobs, max, func(ctx context.Context, t target) (executionResult, error) {
		result, err := execute(ctx, suites[t.suite], strconv.Itoa(t.id), memMB*1024, mutantMode)
		if err != nil {
			return result, fmt.Errorf("%s local mutant %d: %w", suites[t.suite].pkg, t.id, err)
		}
		return result, nil
	}, j, func(completed int) {
		if completed%25 == 0 || completed == len(candidates) {
			fmt.Fprintf(os.Stderr, "completed %d/%d source mutants (%s)\n", completed, len(candidates), time.Since(started).Round(time.Second))
		}
	})
	if err != nil {
		return fmt.Errorf("mutant execution: %w", err)
	}
	executionComplete = true
	survivors := 0
	for i, c := range candidates {
		if results[i] {
			survivors++
			fmt.Printf("survived: %s [file ordinal %d]\n", c.descriptor, c.ordinal)
		}
	}
	fmt.Printf("%s: %d of %d mutants survived %d test suites\n", pkg, survivors, len(candidates), len(suites))
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if max >= 0 && survivors > max {
		return fmt.Errorf("%d survivors exceed limit %d: review source mutations and missing behavioral controls", survivors, max)
	}
	return nil
}
