package connect

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func clearNumericEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"QUINT_MAX_SAMPLES", "QUINT_MAX_STEPS", "QUINT_VERBOSE"} {
		t.Setenv(name, "")
	}
}

func TestConfigFromEnvNumericPreflight(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		env  map[string]string
		want Config
		err  string
	}{
		{"empty defaults", Config{}, nil, Config{}, ""},
		{"zero defaults", Config{}, map[string]string{"QUINT_MAX_SAMPLES": "0", "QUINT_MAX_STEPS": "0", "QUINT_VERBOSE": "0"}, Config{}, ""},
		{"environment values", Config{}, map[string]string{"QUINT_MAX_SAMPLES": "7", "QUINT_MAX_STEPS": "12", "QUINT_VERBOSE": "2"}, Config{MaxSamples: 7, MaxSteps: 12, Verbose: 2}, ""},
		{"explicit precedence", Config{MaxSamples: 3, MaxSteps: 4, Verbose: 1}, map[string]string{"QUINT_MAX_SAMPLES": "many", "QUINT_MAX_STEPS": "bad", "QUINT_VERBOSE": "loud"}, Config{MaxSamples: 3, MaxSteps: 4, Verbose: 1}, ""},
		{"sample typo", Config{}, map[string]string{"QUINT_MAX_SAMPLES": "many"}, Config{}, `QUINT_MAX_SAMPLES="many": want a nonnegative integer`},
		{"sample overflow", Config{}, map[string]string{"QUINT_MAX_SAMPLES": strings.Repeat("9", 100)}, Config{}, "QUINT_MAX_SAMPLES="},
		{"negative sample", Config{}, map[string]string{"QUINT_MAX_SAMPLES": "-1"}, Config{}, `QUINT_MAX_SAMPLES="-1": want a nonnegative integer`},
		{"step typo", Config{}, map[string]string{"QUINT_MAX_STEPS": "ten"}, Config{}, `QUINT_MAX_STEPS="ten": want a nonnegative integer`},
		{"negative step", Config{}, map[string]string{"QUINT_MAX_STEPS": "-1"}, Config{}, `QUINT_MAX_STEPS="-1": want a nonnegative integer`},
		{"verbosity typo", Config{}, map[string]string{"QUINT_VERBOSE": "loud"}, Config{}, `QUINT_VERBOSE="loud": want 0 (quiet), 1 (steps), or 2 (state)`},
		{"verbosity range", Config{}, map[string]string{"QUINT_VERBOSE": "3"}, Config{}, `QUINT_VERBOSE="3": want 0 (quiet), 1 (steps), or 2 (state)`},
		{"explicit negative samples", Config{MaxSamples: -1}, map[string]string{"QUINT_MAX_SAMPLES": "7"}, Config{}, "Config.MaxSamples=-1: want a nonnegative integer"},
		{"explicit negative steps", Config{MaxSteps: -1}, nil, Config{}, "Config.MaxSteps=-1: want a nonnegative integer"},
		{"explicit negative verbosity", Config{Verbose: -1}, nil, Config{}, "Config.Verbose=-1: want 0 (quiet), 1 (steps), or 2 (state)"},
		{"explicit verbosity range", Config{Verbose: 3}, nil, Config{}, "Config.Verbose=3: want 0 (quiet), 1 (steps), or 2 (state)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearNumericEnv(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			got, err := tc.cfg.FromEnv()
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("FromEnv error %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || got.MaxSamples != tc.want.MaxSamples || got.MaxSteps != tc.want.MaxSteps || got.Verbose != tc.want.Verbose {
				t.Fatalf("FromEnv = samples %d, steps %d, verbose %d, %v; want %d, %d, %d", got.MaxSamples, got.MaxSteps, got.Verbose, err, tc.want.MaxSamples, tc.want.MaxSteps, tc.want.Verbose)
			}
		})
	}
}

// Configuration failure must precede both trace cleanup and subprocess work.
// A successful control proves the fake command's marker can actually be reached.
func TestNumericPreflightBeforeTraceEffects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake Quint command is a POSIX shell script")
	}
	clearNumericEnv(t)
	root := t.TempDir()
	marker := filepath.Join(root, "invoked")
	t.Setenv("GOTOGETHER_CONNECT_PREFLIGHT_MARKER", marker)
	quint := filepath.Join(root, "quint")
	if err := os.WriteFile(quint, []byte(`#!/bin/sh
printf called > "$GOTOGETHER_CONNECT_PREFLIGHT_MARKER"
while [ "$#" -gt 0 ]; do
  if [ "$1" = --out-itf ]; then
    shift
    out=$1
  fi
  shift
done
out=$(printf '%s' "$out" | sed 's/{seq}/0/')
printf '{}\n' > "$out"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		cfg  Config
		env  map[string]string
		want string
	}{
		{"malformed environment", Config{}, map[string]string{"QUINT_MAX_SAMPLES": "many"}, "QUINT_MAX_SAMPLES"},
		{"explicit budget", Config{MaxSamples: -1}, nil, "Config.MaxSamples"},
		{"explicit verbosity", Config{Verbose: 3}, nil, "Config.Verbose"},
	} {
		for _, existing := range []bool{false, true} {
			t.Run(tc.name+"/existing="+strconv.FormatBool(existing), func(t *testing.T) {
				clearNumericEnv(t)
				for name, value := range tc.env {
					t.Setenv(name, value)
				}
				dir := filepath.Join(t.TempDir(), "traces")
				stale := filepath.Join(dir, "run_99.itf.json")
				if existing {
					if err := os.Mkdir(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(stale, []byte("retained trace"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				cfg := tc.cfg
				cfg.Spec, cfg.Quint, cfg.Dir, cfg.Seed = "testdata/bank.qnt", quint, dir, "1"
				_, _, err := GenerateRun(t.Context(), cfg)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("GenerateRun error %v, want %s preflight", err, tc.want)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("invalid configuration launched Quint: %v", err)
				}
				if existing {
					if data, err := os.ReadFile(stale); err != nil || string(data) != "retained trace" {
						t.Fatalf("preflight changed an earlier trace: %q, %v", data, err)
					}
				} else if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("preflight created output directory: %v", err)
				}
			})
		}
	}
	files, cfg, err := GenerateRun(t.Context(), Config{Spec: "testdata/bank.qnt", Quint: quint, Dir: filepath.Join(root, "valid"), MaxSamples: 1, MaxSteps: 1, Seed: "1"})
	if err != nil || len(files) != 1 || cfg.MaxSamples != 1 {
		t.Fatalf("valid control: files %v, samples %d, error %v", files, cfg.MaxSamples, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("valid control did not invoke fake Quint: %v", err)
	}
}
