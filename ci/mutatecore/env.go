package main

import (
	"os"
	"os/exec"
	"slices"
	"strings"
)

// A campaign's inherited environment is classified. Semantic variables can
// change what a test checks (a mode, a seed, a backend, a deadline, locale or
// runtime behaviour, a fixture's child switch): every shard of a campaign must
// agree on each, set or unset, with the same value. Runner-only variables
// name a runner's own locations, credentials and bookkeeping: they differ
// between runners and are recorded by name only. Every other variable is
// unclassified, recorded by name only; TestEnvironmentReadsAreClassified fails
// if the module's code reads one. The exact environment, every value of every
// variable, stays in the per-runner execution identity (input_digest).

// semanticKeys is read-only after initialization.
var semanticKeys = []string{
	// Go's runtime, toolchain and build mode
	"CGO_ENABLED", "GO111MODULE", "GO386", "GOAMD64", "GOARCH", "GOARM", "GOARM64", "GODEBUG", "GOEXPERIMENT", "GOFIPS140",
	"GOFLAGS", "GOGC", "GOMAXPROCS", "GOMEMLIMIT", "GOOS", "GORACE", "GOROOT", "GOTOOLCHAIN", "GOTRACEBACK", "GOWORK",
	// time and locale
	"TZ", "LANG", "LANGUAGE", "LC_ALL", "LC_COLLATE", "LC_CTYPE", "LC_MESSAGES", "LC_NUMERIC", "LC_TIME",
	// the campaign's own settings, and tests that read CI
	"CI", "JOBS", "MEM_MB",
	// a run of the workflow: shards of one campaign are one run's one attempt
	"GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT",
	// native tool behaviour validate/model checks for
	"APALACHE_JAR", "CLASSPATH", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH", "JAVA_OPTS", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS",
	"JVM_ARGS", "JVM_GC_ARGS", "LD_LIBRARY_PATH", "LD_PRELOAD", "NODE_OPTIONS", "NODE_PATH", "_JAVA_OPTIONS",
	// fixture switches tests read in their child processes
	"CHOICE_REAL_QUINT", "CHOICE_WORK_LOG", "GUARD_MARK", "GUARD_MUTATE", "GUARD_REAL_QUINT", "HISTORY_REAL_QUINT", "INIT_FAIL",
	"INIT_LOG", "LRU_EVIDENCE_DIR", "MUTATECORE_CANCEL_CHILD", "NAV_NODE_CAPTURE", "NAV_RECORD_CAPTURE", "NAV_RESPONSE_FIXTURE",
	"PG_URL", "SETUP_FAIL", "SETUP_LOG", "GOCACHEPROG",
	// every toolkit and quint variable the module names (unset recorded as
	// unset, not absent); TestEnvironmentReadsAreClassified keeps the list whole
	"GOTOGETHER_ADMISSION_HELPER", "GOTOGETHER_ALL_FAULTS", "GOTOGETHER_CACHE", "GOTOGETHER_CHECK",
	"GOTOGETHER_COMMAND_IO_HELPER", "GOTOGETHER_CONNECT_HELPER", "GOTOGETHER_CONNECT_PREFLIGHT_MARKER",
	"GOTOGETHER_CORPUS", "GOTOGETHER_CPU_TIMES", "GOTOGETHER_DEMO_DIR", "GOTOGETHER_DISCOVERY_BENCH_BIN",
	"GOTOGETHER_DISCOVERY_BENCH_DIR", "GOTOGETHER_EVAL", "GOTOGETHER_EVAL_SAMPLE", "GOTOGETHER_EVIDENCE",
	"GOTOGETHER_HEGEL_SEED", "GOTOGETHER_HELLO_REPLAY", "GOTOGETHER_KEEP", "GOTOGETHER_MAIN",
	"GOTOGETHER_MUTANT", "GOTOGETHER_MUTANT_PACKAGE", "GOTOGETHER_MUTANT_TEST", "GOTOGETHER_NATIVE_BATCH",
	"GOTOGETHER_NATIVE_BATCH_ARTIFACTS", "GOTOGETHER_NATIVE_REUSE_RECEIPT", "GOTOGETHER_NATIVE_VERIFY",
	"GOTOGETHER_NAV", "GOTOGETHER_NAV_FOLD", "GOTOGETHER_NAV_FOLLOW", "GOTOGETHER_NAV_IN", "GOTOGETHER_NAV_OUT",
	"GOTOGETHER_NAV_RECORD", "GOTOGETHER_NAV_SAVE", "GOTOGETHER_OLD_RUNTIME_BACKEND",
	"GOTOGETHER_OLD_RUNTIME_CHILD", "GOTOGETHER_OLD_RUNTIME_DRIVER", "GOTOGETHER_PROGRESS",
	"GOTOGETHER_QEVAL_FUZZ", "GOTOGETHER_QEVAL_FUZZ_SEED", "GOTOGETHER_QEVAL_FUZZ_SHRINK",
	"GOTOGETHER_QEVAL_FUZZ_SIZE", "GOTOGETHER_QEVAL_MULTI_SIZE", "GOTOGETHER_REPORT_DIR", "GOTOGETHER_RUN",
	"GOTOGETHER_SALT", "GOTOGETHER_SCALE_STEPS", "GOTOGETHER_SCHEDULE", "GOTOGETHER_SEED", "GOTOGETHER_SEEDS",
	"GOTOGETHER_SEED_OFFSET", "GOTOGETHER_SESSION", "GOTOGETHER_SHADOW_LOG", "GOTOGETHER_STEPS_DIR",
	"GOTOGETHER_STRICT", "GOTOGETHER_TEST_BACKEND_PROCESS", "GOTOGETHER_TUTORIAL_HELP", "GOTOGETHER_UPDATE_API",
	"GOTOGETHER_UPDATE_GOLDEN", "GOTOGETHER_UPDATE_SCHEMA", "GOTOGETHER_VERBOSE", "GOTOGETHER_VERIFY",
	"QNEMESIS_DIAG", "QNEMESIS_ADDR", "QNEMESIS_CHILD", "QNEMESIS_DIR", "QNEMESIS_GRANDCHILD", "QNEMESIS_IMAGE", "QNEMESIS_INC",
	"QNEMESIS_LOG", "QNEMESIS_NAME", "QNEMESIS_TICK", "QUINT_BACKEND", "QUINT_BIN", "QUINT_HOME",
	"QUINT_MAX_SAMPLES", "QUINT_MAX_STEPS", "QUINT_SEED", "QUINT_VERBOSE",
}

// semanticPrefixes is read-only after initialization. GOTOGETHER_ covers
// every toolkit variable a test may read (CHECK, STRICT, EVAL, EVAL_SAMPLE,
// SEEDS, RUN, ALL_FAULTS, ...), present or not, except those runnerOnlyKeys
// names.
var semanticPrefixes = []string{"GOTOGETHER_", "QUINT_", "QNEMESIS_", "GO_WANT_", "FIXTURE_", "ONBOARDING_"}

// runnerOnlyKeys is read-only after initialization.
var runnerOnlyKeys = []string{
	"PATH", "HOME", "TMPDIR", "USER", "LOGNAME", "PWD", "OLDPWD", "SHLVL", "SHELL", "HOSTNAME", "TERM", "_",
	"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "XDG_STATE_HOME",
	"GOPATH", "GOCACHE", "GOMODCACHE", "GOENV", "GOTMPDIR", "GOPROXY", "GOSUMDB", "GONOSUMDB", "GONOPROXY", "GOPRIVATE", "GOINSECURE",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR",
	"INVOCATION_ID", "JOURNAL_STREAM", "ImageOS", "ImageVersion", "AGENT_TOOLSDIRECTORY",
	// the journal path is each shard's own
	"GOTOGETHER_MUTATION_LOG",
}

// runnerOnlyPrefixes is read-only after initialization.
var runnerOnlyPrefixes = []string{"RUNNER_", "GITHUB_", "ACTIONS_", "SSH_"}

// pathTools are external programs tests look up on PATH, skipping or
// changing what they check by their presence. Each is identified by content.
// pathTools is read-only after initialization.
var pathTools = []string{"quint", "node", "java", "apalache-mc", "docker", "kind", "kubectl", "kwok", "kwokctl", "maelstrom", "ttyd", "ffmpeg", "vhs"}

type envClass int

const (
	unclassified envClass = iota
	semantic
	runnerOnly
)

// classify: exact keys first (GITHUB_RUN_ID is semantic though GITHUB_ is a
// runner prefix; GOTOGETHER_MUTATION_LOG runner-only though GOTOGETHER_ is
// semantic), then prefixes.
func classify(name string) envClass {
	switch {
	case slices.Contains(semanticKeys, name):
		return semantic
	case slices.Contains(runnerOnlyKeys, name):
		return runnerOnly
	}
	for _, p := range semanticPrefixes {
		if strings.HasPrefix(name, p) {
			return semantic
		}
	}
	for _, p := range runnerOnlyPrefixes {
		if strings.HasPrefix(name, p) {
			return runnerOnly
		}
	}
	return unclassified
}

// envSetting distinguishes an unset variable from one set empty.
type envSetting struct {
	Name  string `json:"name"`
	Set   bool   `json:"set"`
	Value string `json:"value"`
}

// semanticEnvironment lists every semantic key (set or not) and every present
// variable of a semantic prefix, by name; and the names of the present
// runner-only and unclassified variables.
type semanticEnvironment struct {
	Semantic     []envSetting `json:"semantic"`
	Tools        []pathTool   `json:"path_tools"`
	RunnerOnly   []string     `json:"runner_only"`
	Unclassified []string     `json:"unclassified"`
}
type pathTool struct {
	Name   string `json:"name"`
	Found  bool   `json:"found"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
	// Unavailable is why a tool found on PATH has no content digest (an
	// execute-only binary, a target that vanished): such a tool has no
	// identity, and its shard cannot be aggregated (sharedIdentityOf).
	Unavailable string `json:"unavailable,omitempty"`
}

func classifyEnvironment(environ []string, lookPath func(string) (string, error)) semanticEnvironment {
	var e semanticEnvironment
	present := map[string]string{}
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		present[k] = v
	}
	names := slices.Clone(semanticKeys)
	for k := range present {
		switch classify(k) {
		case semantic:
			if !slices.Contains(names, k) {
				names = append(names, k)
			}
		case runnerOnly:
			e.RunnerOnly = append(e.RunnerOnly, k)
		default:
			e.Unclassified = append(e.Unclassified, k)
		}
	}
	slices.Sort(names)
	for _, k := range names {
		v, ok := present[k]
		e.Semantic = append(e.Semantic, envSetting{Name: k, Set: ok, Value: v})
	}
	slices.Sort(e.RunnerOnly)
	slices.Sort(e.Unclassified)
	for _, name := range pathTools {
		t := pathTool{Name: name}
		if p, err := lookPath(name); err == nil {
			// a tool found but unreadable has no identity: no digest, and
			// the reason, so that two shards failing alike never compare
			// equal on their error text
			if sum, size, err := digestFile(p); err == nil {
				t.Found, t.SHA256, t.Size = true, sum, size
			} else {
				t.Found, t.Unavailable = true, err.Error()
			}
		}
		e.Tools = append(e.Tools, t)
	}
	return e
}

func currentEnvironment() semanticEnvironment {
	return classifyEnvironment(os.Environ(), exec.LookPath)
}
