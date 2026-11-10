package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type workerConfig struct {
	Go          string
	Capture     string
	Root        string
	Package     string
	Max         int
	Jobs        int
	MemMB       int
	Work        string
	Journal     string
	Result      string
	Discovery   []byte
	InputDigest string
}
type fileIdentity struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type buildInvocation struct {
	Directory  string   `json:"directory"`
	Arguments  []string `json:"arguments"`
	PathPrefix string   `json:"path_prefix,omitempty"`
}

type workerResult struct {
	Builds    []buildInvocation    `json:"builds"`
	Generated []capturedInvocation `json:"generated"`
	Wrapper   fileIdentity         `json:"wrapper"`
	Manifest  producerManifest     `json:"manifest"`
	Journal   string               `json:"journal"`
	Qweave    fileIdentity         `json:"qweave"`
	Complete  completeRecord       `json:"complete"`
}
type provenanceContext struct {
	DiscoveryArguments       []string      `json:"discovery_arguments"`
	Inputs                   inputManifest `json:"inputs"`
	Unavailable              string        `json:"unavailable,omitempty"`
	Discovery                []byte        `json:"discovery"`
	RuntimeEnvironmentDigest string        `json:"runtime_environment_digest"`
}
type completionReceipt struct {
	Builds            []buildInvocation    `json:"builds"`
	Generated         []capturedInvocation `json:"generated"`
	Wrapper           fileIdentity         `json:"wrapper"`
	Version           int                  `json:"version"`
	AdmissionEligible bool                 `json:"admission_eligible"`
	Limitations       []string             `json:"limitations"`
	InputDigest       string               `json:"input_digest"`
	InputsRechecked   bool                 `json:"inputs_rechecked"`
	Inputs            provenanceContext    `json:"inputs"`
	Producer          fileIdentity         `json:"producer"`
	WorkerExecution   executionResult      `json:"worker_execution"`
	Qweave            fileIdentity         `json:"qweave"`
	ExecutionTools    []fileIdentity       `json:"execution_tools"`
	Manifest          producerManifest     `json:"manifest"`
	Journal           fileIdentity         `json:"journal"`
	Complete          completeRecord       `json:"complete"`
}

func identifyFile(path string) (fileIdentity, error) {
	sum, size, err := digestFile(path)
	return fileIdentity{Path: path, SHA256: sum, Size: size}, err
}
func valueDigest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func readJSONFile(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	scan := json.NewDecoder(bytes.NewReader(raw))
	if _, err := jsonValue(scan); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%s: trailing worker data", path)
	}
	return nil
}
func campaignStart(c workerConfig) map[string]any {
	return map[string]any{"version": 1, "root": c.Root, "package": c.Package, "jobs": c.Jobs, "mem_mb": c.MemMB, "timeout_seconds": 300, "max_survivors": c.Max, "input_digest": c.InputDigest}
}
func discover(ctx context.Context, root, pkg string) ([]byte, error) {
	c := groupCommand(ctx, "go", discoveryArguments(pkg)...)
	c.Dir = root
	c.Stderr = os.Stderr
	b, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("list packages: %w", err)
	}
	return b, nil
}
func collectContext(ctx context.Context, root, pkg string) (provenanceContext, error) {
	var c provenanceContext
	discovery, err := discover(ctx, root, pkg)
	if err != nil {
		return c, err
	}
	c.DiscoveryArguments = discoveryArguments(pkg)
	c.Discovery = discovery
	c.Inputs, err = collectInputs(ctx, root, pkg)
	if err != nil {
		// Provenance unsupported is not a new mutation gate. Preserve execution,
		// and disclose the unavailable boundary without granting policy eligibility.
		c.Unavailable = err.Error()
	}
	// Tests may read arbitrary environment, not only go env. Keep values private
	// while binding the exact inherited environment to this operational receipt.
	env := append([]string(nil), os.Environ()...)
	// Sorting makes this independent of the process environment's enumeration.
	slices.Sort(env)
	c.RuntimeEnvironmentDigest, _ = valueDigest(env)
	return c, nil
}

func run(ctx context.Context) error {
	if len(os.Args) >= 5 && os.Args[1] == "--capture-go" {
		return captureGo(ctx, os.Args[2:])
	}
	if len(os.Args) == 3 && os.Args[1] == "--mutation-worker" {
		var cfg workerConfig
		if err := readJSONFile(os.Args[2], &cfg); err != nil {
			return err
		}
		result, err := runWorker(ctx, cfg)
		if err != nil {
			return err
		}
		// This only attests this worker's completed Close. Its parent observes its
		// actual exit before publishing the externally checked completion receipt.
		return publishJSONContext(ctx, cfg.Result, result)
	}
	if len(os.Args) < 3 || len(os.Args) > 4 {
		return errors.New("usage: mutatecore root core-package [max-survivors]")
	}
	cfg := workerConfig{Root: os.Args[1], Package: os.Args[2], Max: -1}
	var err error
	cfg.Root, err = filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}
	if len(os.Args) == 4 {
		cfg.Max, err = strconv.Atoi(os.Args[3])
		if err != nil || cfg.Max < 0 {
			return errors.New("max-survivors must be nonnegative")
		}
	}
	cfg.Jobs, err = positiveEnv("JOBS", 1)
	if err != nil {
		return err
	}
	cfg.MemMB, err = positiveEnv("MEM_MB", 3072)
	if err != nil {
		return err
	}
	before, err := collectContext(ctx, cfg.Root, cfg.Package)
	if err != nil {
		return err
	}
	if before.Unavailable != "" {
		fmt.Fprintf(os.Stderr, "mutation provenance unavailable (count gate unchanged): %s\n", before.Unavailable)
	}
	cfg.Go, err = exec.LookPath("go")
	if err != nil {
		return err
	}
	cfg.Discovery = before.Discovery
	cfg.InputDigest, err = valueDigest(before)
	if err != nil {
		return err
	}
	cfg.Work, err = os.MkdirTemp("", "mutate-core-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cfg.Work)
	cfg.Journal = os.Getenv("GOTOGETHER_MUTATION_LOG")
	if cfg.Journal == "" {
		dir := filepath.Join(cfg.Root, ".gotogether", "mutations")
		if err := durableMkdirAll(dir, 0700); err != nil {
			return err
		}
		owned, err := os.MkdirTemp(dir, strings.ReplaceAll(cfg.Package, "/", "-")+"-")
		if err != nil {
			return err
		}
		if err := syncDirectoryAncestry(owned, syncDirectory); err != nil {
			return err
		}
		cfg.Journal = filepath.Join(owned, "journal.jsonl")
	} else if !filepath.IsAbs(cfg.Journal) {
		cfg.Journal = filepath.Join(cfg.Root, cfg.Journal)
	}
	if err := durableMkdirAll(filepath.Dir(cfg.Journal), 0700); err != nil {
		return err
	}
	if _, err := os.Lstat(cfg.Journal); err == nil {
		return fmt.Errorf("campaign journal already exists: %s", cfg.Journal)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := publishJSON(cfg.Journal+".inputs.json", before); err != nil {
		return err
	}
	cfg.Capture = cfg.Journal + ".build-inputs"
	cfg.Result = filepath.Join(cfg.Work, "worker-result.json")
	config := filepath.Join(cfg.Work, "worker-config.json")
	if err := publishJSON(config, cfg); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	producer, err := identifyFile(exe)
	if err != nil {
		return err
	}
	var tools []fileIdentity
	for _, name := range []string{"go", "sh", "timeout"} {
		p, err := exec.LookPath(name)
		if err != nil {
			return err
		}
		identity, err := identifyFile(p)
		if err != nil {
			return err
		}
		tools = append(tools, identity)
	}
	child := exec.CommandContext(ctx, exe, "--mutation-worker", config)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	child.Cancel = func() error { return syscall.Kill(-child.Process.Pid, syscall.SIGTERM) }
	child.WaitDelay = 5 * time.Second
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	execution, err := runExecution(ctx, child, false)
	if err != nil {
		return fmt.Errorf("mutation worker: %w", err)
	}
	if !execution.passed() {
		return fmt.Errorf("mutation worker incomplete: %s", execution)
	}
	var result workerResult
	if err := readJSONFile(cfg.Result, &result); err != nil {
		return fmt.Errorf("worker completion: %w", err)
	}
	if result.Journal != cfg.Journal || result.Manifest.InputDigest != cfg.InputDigest {
		return errors.New("worker result does not match owned campaign")
	}
	if err := verifyOwnedBuild(ctx, cfg, result, producer, tools); err != nil {
		return err
	}
	after, err := collectContext(ctx, cfg.Root, cfg.Package)
	if err != nil {
		return err
	}
	afterDigest, err := valueDigest(after)
	if err != nil {
		return err
	}
	if afterDigest != cfg.InputDigest {
		return errors.New("source/build inputs or discovery changed during mutation execution: completion not sealed")
	}
	complete, err := validateJournal(result.Journal, result.Manifest)
	if err != nil {
		return fmt.Errorf("validate completed journal: %w", err)
	}
	if complete != result.Complete {
		return errors.New("worker result and journal complete disagree")
	}
	journal, err := identifyFile(cfg.Journal)
	if err != nil {
		return err
	}
	receipt := completionReceipt{Builds: result.Builds, Generated: result.Generated, Wrapper: result.Wrapper, Version: 1, AdmissionEligible: false, Limitations: []string{"completion evidence only; no reviewpolicy admission or survivor approval", "arbitrary runtime files and external services are not a closed input universe"}, InputDigest: cfg.InputDigest, InputsRechecked: before.Unavailable == "", Inputs: before, Producer: producer, WorkerExecution: execution, Qweave: result.Qweave, ExecutionTools: tools, Manifest: result.Manifest, Journal: journal, Complete: complete}
	receipt.Limitations = append(receipt.Limitations, before.Inputs.Limitations...)
	if before.Unavailable != "" {
		receipt.Limitations = append(receipt.Limitations, before.Unavailable)
	}
	if err := publishJSONContext(ctx, cfg.Journal+".receipt.json", receipt); err != nil {
		return fmt.Errorf("seal completion receipt: %w", err)
	}
	fmt.Fprintf(os.Stderr, "mutation completion receipt: %s.receipt.json (not admission eligible)\n", cfg.Journal)
	if !complete.GatePassed {
		return fmt.Errorf("%d survivors exceed limit %d: review source mutations and missing behavioral controls", complete.Survivors, cfg.Max)
	}
	return nil
}

func verifyOwnedBuild(ctx context.Context, cfg workerConfig, result workerResult, producer fileIdentity, tools []fileIdentity) error {
	if !reflect.DeepEqual(result.Builds, expectedBuilds(cfg)) {
		return errors.New("worker build commands differ from owned configuration")
	}
	expectedStart, _ := json.Marshal(campaignStart(cfg))
	if !reflect.DeepEqual(expectedStart, []byte(result.Manifest.Start)) {
		return errors.New("worker start settings changed")
	}
	expected, err := testSuites(strings.NewReader(string(cfg.Discovery)), cfg.Work)
	if err != nil {
		return err
	}
	if len(expected) != len(result.Manifest.Suites) || len(expected) != len(result.Manifest.Binaries) {
		return errors.New("worker suite roster differs from independent discovery")
	}
	for i, s := range expected {
		got := result.Manifest.Suites[i]
		if got.Index != i || got.Package != s.pkg || got.Directory != s.dir || got.Binary != s.binary {
			return errors.New("worker suite identity differs from independent discovery")
		}
		actual, err := identifyFile(s.binary)
		if err != nil {
			return err
		}
		want := result.Manifest.Binaries[i]
		if want.Suite != i || want.SHA256 != actual.SHA256 || want.Size != actual.Size {
			return errors.New("worker suite binary changed")
		}
		execution, err := execute(ctx, s, "list", cfg.MemMB*1024, inventoryMode)
		if err != nil {
			return err
		}
		if !execution.passed() {
			return errors.New("independent inventory execution failed")
		}
		inv, err := inventory(execution.Output)
		if err != nil {
			return err
		}
		s.mutants = inv
		if !reflect.DeepEqual(suiteMetadata(i, s), got) {
			return errors.New("worker inventory differs from independently executed binary")
		}
	}
	if result.Qweave.Path != filepath.Join(cfg.Work, "qweave") {
		return errors.New("worker generator path differs from owned build")
	}
	captures, err := captureManifest(cfg.Capture)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(captures, result.Generated) {
		return errors.New("generated build input manifest changed")
	}
	if result.Wrapper.Path != filepath.Join(cfg.Work, "go-wrapper", "go") {
		return errors.New("owned Go wrapper path changed")
	}
	all := append([]fileIdentity{producer, result.Qweave, result.Wrapper}, tools...)
	for _, want := range all {
		actual, err := identifyFile(want.Path)
		if err != nil {
			return err
		}
		if actual != want {
			return fmt.Errorf("build/execution tool changed: %s", want.Path)
		}
	}
	return nil
}

func expectedBuilds(cfg workerConfig) []buildInvocation {
	qweave := filepath.Join(cfg.Work, "qweave")
	builds := []buildInvocation{{Directory: filepath.Join(cfg.Root, "qweave"), Arguments: []string{cfg.Go, "build", "-o", qweave, "./cmd/qweave"}}}
	suites, err := testSuites(strings.NewReader(string(cfg.Discovery)), cfg.Work)
	if err != nil {
		return nil
	}
	for _, s := range suites {
		builds = append(builds, buildInvocation{Directory: cfg.Root, Arguments: []string{qweave, "-C", cfg.Root, "-mutate", "-coverpkg", coreModule + "/" + cfg.Package, "go", "test", "-c", "-o", s.binary, s.pkg}, PathPrefix: filepath.Join(cfg.Work, "go-wrapper")})
	}
	return builds
}

func discoveryArguments(pkg string) []string {
	return []string{"list", "-json=ImportPath,Dir,TestGoFiles,XTestGoFiles", "./" + pkg + "/..."}
}
