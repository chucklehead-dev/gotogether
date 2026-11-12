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
	Shard       shardSpec
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
	// Environment classifies the same inherited environment: semantic
	// variables by value (shared by every shard), the rest by name.
	Environment semanticEnvironment `json:"environment"`
}
type completionReceipt struct {
	Builds            []buildInvocation    `json:"builds"`
	Generated         []capturedInvocation `json:"generated"`
	Wrapper           fileIdentity         `json:"wrapper"`
	Version           int                  `json:"version"`
	Shard             shardSpec            `json:"shard"`
	Shared            *sharedIdentity      `json:"shared,omitempty"`
	SharedUnavailable string               `json:"shared_unavailable,omitempty"`
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

// startRecord is a campaign's settings. A shard of a split campaign records
// no max: its survivors are counted by the aggregate, against the package's
// max, once.
type startRecord struct {
	Version        int       `json:"version"`
	Root           string    `json:"root"`
	Package        string    `json:"package"`
	Jobs           int       `json:"jobs"`
	MemMB          int       `json:"mem_mb"`
	TimeoutSeconds int       `json:"timeout_seconds"`
	MaxSurvivors   *int      `json:"max_survivors,omitempty"`
	Shard          shardSpec `json:"shard"`
	InputDigest    string    `json:"input_digest"`
}

func campaignStart(c workerConfig) startRecord {
	s := startRecord{Version: journalVersion, Root: c.Root, Package: c.Package, Jobs: c.Jobs, MemMB: c.MemMB, TimeoutSeconds: 300, Shard: c.Shard, InputDigest: c.InputDigest}
	if c.Shard.Count == 1 {
		max := c.Max
		s.MaxSurvivors = &max
	}
	return s
}

// decodeStart reads recorded settings in the current strict shape only.
func decodeStart(raw []byte) (startRecord, error) {
	var s startRecord
	if err := decodeJSON(raw, &s); err != nil {
		return s, fmt.Errorf("campaign start: %w", err)
	}
	switch {
	case s.Version != journalVersion:
		return s, fmt.Errorf("unsupported journal version %d (current %d): rerun the campaign with the current helper", s.Version, journalVersion)
	case s.Shard.Count < 1 || s.Shard.Index < 0 || s.Shard.Index >= s.Shard.Count:
		return s, fmt.Errorf("invalid shard %s", s.Shard)
	case (s.MaxSurvivors == nil) != (s.Shard.Count > 1):
		return s, errors.New("campaign start: a max is recorded exactly when the campaign is unsharded")
	case s.MaxSurvivors != nil && *s.MaxSurvivors < -1:
		return s, errors.New("campaign start: invalid max")
	}
	return s, nil
}

func (s startRecord) gate() gatePolicy {
	if s.MaxSurvivors == nil {
		return gatePolicy{Max: -1, Shards: s.Shard.Count}
	}
	return gatePolicy{Max: *s.MaxSurvivors, Shards: s.Shard.Count}
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
	c.Environment = currentEnvironment()
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
	if len(os.Args) >= 2 && os.Args[1] == "aggregate" {
		return runAggregate(ctx, os.Args[2:])
	}
	cfg, err := parseCampaign(os.Args[1:])
	if err != nil {
		return err
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
	receipt := completionReceipt{Builds: result.Builds, Generated: result.Generated, Wrapper: result.Wrapper, Version: receiptVersion, Shard: cfg.Shard, AdmissionEligible: false, Limitations: []string{"completion evidence only; no reviewpolicy admission or survivor approval", "arbitrary runtime files and external services are not a closed input universe"}, InputDigest: cfg.InputDigest, InputsRechecked: before.Unavailable == "", Inputs: before, Producer: producer, WorkerExecution: execution, Qweave: result.Qweave, ExecutionTools: tools, Manifest: result.Manifest, Journal: journal, Complete: complete}
	receipt.Limitations = append(receipt.Limitations, before.Inputs.Limitations...)
	if before.Unavailable != "" {
		receipt.Limitations = append(receipt.Limitations, before.Unavailable)
	}
	receipt.Limitations = append(receipt.Limitations, identityBoundary...)
	if shared, err := sharedIdentityOf(receipt); err != nil {
		// The count gate is unchanged; such a shard cannot be aggregated.
		receipt.SharedUnavailable = err.Error()
	} else {
		receipt.Shared = &shared
	}
	if err := publishJSONContext(ctx, cfg.Journal+".receipt.json", receipt); err != nil {
		return fmt.Errorf("seal completion receipt: %w", err)
	}
	fmt.Fprintf(os.Stderr, "mutation completion receipt: %s.receipt.json (not admission eligible)\n", cfg.Journal)
	switch complete.Gate {
	case gateFailed:
		return fmt.Errorf("%d survivors exceed limit %d: review source mutations and missing behavioral controls", complete.Survivors, cfg.Max)
	case gateDeferred:
		fmt.Fprintf(os.Stderr, "shard %s complete: %d of its %d source mutants survived; no limit applied here (the aggregate applies the package's max once to every shard's sum)\n", cfg.Shard, complete.Survivors, complete.Total)
	}
	return nil
}

// identityBoundary says what a shared identity does and does not establish.
// identityBoundary is read-only after initialization.
var identityBoundary = []string{
	"shared identity: source files, Go discovery, candidate inventory, suite roster and binaries, generator, compiler and execution tools, and campaign configuration, by content, with the checkout root, GOROOT and GOMODCACHE path prefixes made relative and nothing else relaxed",
	"shared semantic environment: every classified semantic variable by value (unset distinct from empty) and the tools on PATH tests look up, by content; runner-only and unclassified variables are recorded by name per shard and not compared",
	"execution identity (input_digest: the exact inherited environment and absolute paths) is per runner and is not compared across shards",
}

const usage = "usage: mutatecore root [-shard i/n] core-package [max-survivors]\n       mutatecore aggregate core-package max-survivors shards evidence-dir"

// parseCampaign reads root, an optional -shard i/n and the package with its
// optional max. A shard of n > 1 takes no max: the aggregate applies it once.
func parseCampaign(args []string) (workerConfig, error) {
	cfg := workerConfig{Max: -1, Shard: shardSpec{0, 1}}
	if len(args) < 2 {
		return cfg, errors.New(usage)
	}
	cfg.Root, args = args[0], args[1:]
	if args[0] == "-shard" {
		if len(args) < 2 {
			return cfg, errors.New(usage)
		}
		var err error
		if cfg.Shard, err = parseShard(args[1]); err != nil {
			return cfg, err
		}
		args = args[2:]
	}
	if len(args) < 1 || len(args) > 2 || strings.HasPrefix(args[0], "-") {
		return cfg, errors.New(usage)
	}
	cfg.Package = args[0]
	if err := refuseRerun(cfg, os.Getenv("GITHUB_RUN_ATTEMPT"), os.Getenv("GITHUB_REF_NAME")); err != nil {
		return cfg, err
	}
	var err error
	cfg.Root, err = filepath.Abs(cfg.Root)
	if err != nil {
		return cfg, err
	}
	if len(args) == 2 {
		if cfg.Shard.Count > 1 {
			return cfg, fmt.Errorf("shard %s takes no max-survivors: the package's max is applied once, to every shard's survivors summed (mutatecore aggregate %s %s %d <evidence-dir>)", cfg.Shard, cfg.Package, args[1], cfg.Shard.Count)
		}
		cfg.Max, err = strconv.Atoi(args[1])
		if err != nil || cfg.Max < 0 {
			return cfg, errors.New("max-survivors must be nonnegative")
		}
	}
	return cfg, nil
}

// refuseRerun: a shard's artifact is named by its package and shard alone,
// so a re-run attempt of one shard job would meet its earlier attempt's
// evidence. The supported retry is a fresh dispatch of the package, whose
// shards are all one run's first attempt.
func refuseRerun(cfg workerConfig, attempt, ref string) error {
	if cfg.Shard.Count == 1 || attempt == "" || attempt == "1" {
		return nil
	}
	if ref == "" {
		ref = "<ref>"
	}
	return fmt.Errorf("shard %s of %s is attempt %s of its workflow run: a split campaign is not re-run in place (its shards would mix attempts). Dispatch a fresh run of the package instead: gh workflow run nightly.yml --ref %s -f jobs=core-mutants -f mutants=%s", cfg.Shard, cfg.Package, attempt, ref, cfg.Package)
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
