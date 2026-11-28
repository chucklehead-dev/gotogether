package quintcmd

import (
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
	"strings"
	"sync/atomic"
	"time"
)

// ProbeTimeoutEnv names the variable that bounds a `quint --version`
// probe (a Go duration; DefaultProbeTimeout unset).
const ProbeTimeoutEnv = "GOTOGETHER_QUINT_PROBE_TIMEOUT"

// DefaultProbeTimeout bounds a probe when ProbeTimeoutEnv is unset. Node
// answers in a second on an idle machine; with Veil's proofs on every
// core it once took more than ten (DESIGN §116).
const DefaultProbeTimeout = 30 * time.Second

// probeTimeout is the probe's bound, from ProbeTimeoutEnv.
func probeTimeout() (time.Duration, error) {
	v := os.Getenv(ProbeTimeoutEnv)
	if v == "" {
		return DefaultProbeTimeout, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s=%q: not a duration (30s, 2m): set a positive one, or unset it for %s", ProbeTimeoutEnv, v, DefaultProbeTimeout)
	}
	return d, nil
}

// A ProbeTimeoutError is a `quint --version` that did not answer within
// its timeout: a quint that is there but slow (a loaded machine), never
// a missing one (NotFound).
type ProbeTimeoutError struct {
	Binary  string
	Timeout time.Duration
}

func (e *ProbeTimeoutError) Error() string {
	set := "unset, the default"
	if v := os.Getenv(ProbeTimeoutEnv); v != "" {
		set = ProbeTimeoutEnv + "=" + v
	}
	return fmt.Sprintf("%s --version did not answer within %s (%s): a loaded machine; raise it (%s=2m), or run fewer quint processes at once", e.Binary, e.Timeout, set, ProbeTimeoutEnv)
}

// NotFound reports whether err is a quint that is not there (not on
// PATH, no such file), as against one that failed or timed out.
func NotFound(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

// probe runs c's binary with --version, in c's directory and environment,
// within the probe timeout. The caller holds an admission slot, so the
// timeout counts the probe alone, never the wait for a slot. A timeout is
// a *ProbeTimeoutError; c's own context's end is its error.
func (c *Cmd) probe() ([]byte, error) {
	timeout, err := probeTimeout()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	probe := exec.CommandContext(ctx, c.Path, "--version")
	probe.Path, probe.Err = c.Path, c.Err // preserve exec's original path resolution
	if len(c.Args) > 0 {
		probe.Args[0] = c.Args[0]
	}
	probe.Dir, probe.Env, probe.WaitDelay = c.Dir, c.raw().Environ(), WaitDelay
	group(probe)
	out, err := probe.Output()
	if err := c.ctx.Err(); err != nil {
		return out, err
	}
	if ctx.Err() != nil {
		name := c.Path
		if len(c.Args) > 0 {
			name = c.Args[0]
		}
		return out, &ProbeTimeoutError{Binary: name, Timeout: timeout}
	}
	return out, err
}

// Cache only the reviewed stock CLI: its version comes from version.js and is
// independent of the model directory. Unknown wrappers and future CLI builds
// are probed each time; their delegated programs/files cannot be inferred.
const stockCLIHash = "4e8e20625162401e2934d8c7e8789a53c057fb578a8f0f47d2883a5e44b4d009"
const stockVersionHash = "a516d978a79f871d14f768282671683855fe9bc138f5f59406c4c522cb3b91d9"

// Node v22.23.4 Linux/amd64, from the admitted npm node@22 toolchain. This is
// eligibility for an optimization, not a runtime requirement for other Nodes.
const stockNodeHash = "94efd062ed066c1a0b332e18249859c905e920eeada5df55d24c99b919629a90"

type nodeIdentity struct {
	fingerprint string
	reviewed    bool
}

// nodeIdentities avoids rehashing the 120MiB interpreter when its identity is
// unchanged. One last entry bounds memory. Process-wide.
var nodeIdentities atomic.Value // nodeIdentity

// versions remembers the last successful stock identity, never failed/cancelled
// owners. One entry bounds memory even when callers vary their environment.
// Process-wide.
var versions atomic.Value // string

func versionQuery(c *exec.Cmd) bool { return len(c.Args) == 2 && c.Args[1] == "--version" }

func (c *Cmd) checkVersion() error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	key := stockIdentity(c.raw())
	if key != "" {
		if saved := versions.Load(); saved != nil && saved.(string) == key {
			return nil
		}
	}
	// A probe owns one slot, releases it, then the workload independently gets
	// one slot. Never recurse through Command or hold a cache lock while waiting.
	release, err := acquire(c.ctx)
	if err != nil {
		return err
	}
	defer release()
	key = stockIdentity(c.raw())
	if key != "" {
		if saved := versions.Load(); saved != nil && saved.(string) == key {
			return nil
		}
	}
	out, err := c.probe()
	if ctxErr := c.ctx.Err(); ctxErr != nil {
		return fmt.Errorf("quint admission: version probe: %w", ctxErr)
	}
	if err != nil {
		if _, timedOut := errors.AsType[*ProbeTimeoutError](err); timedOut {
			return fmt.Errorf("quint admission: %w", err)
		}
		if NotFound(err) {
			return fmt.Errorf("quint admission: %s --version: %w; install Quint with npm install -g @informalsystems/quint@%s", c.Path, err, PinnedVersion)
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return fmt.Errorf("quint admission: %s --version: %w: %s", c.Path, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return fmt.Errorf("quint admission: %s --version: %w", c.Path, err)
	}
	version := strings.TrimSpace(string(out))
	if !Supported(version) {
		return fmt.Errorf("quint admission: unsupported Quint version %q from %s; install stable Quint %s or later (npm install -g @informalsystems/quint@%s and run ci/install-quint.sh for the paired Rust evaluator)", version, c.Path, MinimumVersion, PinnedVersion)
	}
	if key != "" && version == PinnedVersion && key == stockIdentity(c.raw()) {
		versions.Store(key)
	}
	return nil
}

func envValue(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v
		}
	}
	return ""
}

func stockIdentity(c *exec.Cmd) string {
	if c.Err != nil {
		return ""
	}
	env := c.Environ()
	for _, name := range []string{"NODE_OPTIONS", "NODE_PATH", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH"} {
		if envValue(env, name) != "" {
			return ""
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := c.Dir
	if dir == "" {
		dir = cwd
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	binary := c.Path
	if !filepath.IsAbs(binary) {
		binary = filepath.Join(dir, binary)
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return ""
	}
	if filepath.Base(binary) != "cli.js" || filepath.Base(filepath.Dir(binary)) != "src" || filepath.Base(filepath.Dir(filepath.Dir(binary))) != "dist" {
		return ""
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(binary)))
	cli, err := os.ReadFile(binary)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(cli)
	if hex.EncodeToString(digest[:]) != stockCLIHash {
		return ""
	}
	version, err := os.ReadFile(filepath.Join(filepath.Dir(binary), "version.js"))
	if err != nil {
		return ""
	}
	digest = sha256.Sum256(version)
	if hex.EncodeToString(digest[:]) != stockVersionHash {
		return ""
	}
	metadata, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Name, Version string
		Bin           map[string]string
	}
	if json.Unmarshal(metadata, &pkg) != nil || pkg.Name != "@informalsystems/quint" || pkg.Version != PinnedVersion || pkg.Bin["quint"] != "dist/src/cli.js" {
		return ""
	}
	// /usr/bin/env node resolves PATH in the child's directory, not the parent
	// process's PATH. A different interpreter invalidates the admission memo.
	var node string
	var info os.FileInfo
	for _, entry := range filepath.SplitList(envValue(env, "PATH")) {
		if !filepath.IsAbs(entry) {
			entry = filepath.Join(dir, entry)
		}
		candidate := filepath.Join(entry, "node")
		stat, e := os.Stat(candidate)
		if e == nil && !stat.IsDir() && stat.Mode()&0111 != 0 {
			node, info = candidate, stat
			break
		}
	}
	if node == "" {
		return ""
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil || !reviewedNode(node, info) {
		return ""
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "%q\x00%q\x00%d\x00%d\x00%v\x00", binary, node, info.Size(), info.ModTime().UnixNano(), info.Sys())
	hash.Write(metadata)
	hash.Write(version)
	hash.Write(cli)
	if len(c.Args) > 0 {
		fmt.Fprintf(hash, "\x00%q", c.Args[0])
	}
	for _, value := range env { // PWD is irrelevant only for the reviewed stock CLI.
		if !strings.HasPrefix(value, "PWD=") {
			fmt.Fprintf(hash, "\x00%q", value)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Native proxies can also dispatch by cwd (for example mise/asdf shims).
// Executable format alone therefore cannot prove Node's version behavior.
func reviewedNode(path string, before os.FileInfo) bool {
	fingerprint := fmt.Sprintf("%q/%d/%d/%v", path, before.Size(), before.ModTime().UnixNano(), before.Sys())
	if value := nodeIdentities.Load(); value != nil {
		entry := value.(nodeIdentity)
		if entry.fingerprint == fingerprint {
			return entry.reviewed
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(before, actual) {
		return false
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return false
	}
	after, err := f.Stat()
	if err != nil || actual.Size() != after.Size() || !actual.ModTime().Equal(after.ModTime()) || fmt.Sprint(actual.Sys()) != fmt.Sprint(after.Sys()) {
		return false // do not attach a digest to an identity that changed while read
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(after, current) || after.Size() != current.Size() || !after.ModTime().Equal(current.ModTime()) {
		return false
	}
	reviewed := hex.EncodeToString(hash.Sum(nil)) == stockNodeHash
	nodeIdentities.Store(nodeIdentity{fmt.Sprintf("%q/%d/%d/%v", path, current.Size(), current.ModTime().UnixNano(), current.Sys()), reviewed})
	return reviewed
}
