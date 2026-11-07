package connect

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func oldRuntime(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake CLI")
	}
	bin := filepath.Join(t.TempDir(), "quint")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.33.0; exit 0; fi\necho executed >> \"$0.work\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestUnsupportedRuntimeBeforeTraceEffects(t *testing.T) {
	for _, backend := range []string{"typescript", "rust"} {
		for _, existing := range []bool{false, true} {
			t.Run(backend+map[bool]string{false: "/new", true: "/existing"}[existing], func(t *testing.T) {
				bin := oldRuntime(t)
				dir := filepath.Join(t.TempDir(), "traces")
				stale := filepath.Join(dir, "run_99.itf.json")
				if existing {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(stale, []byte("retained evidence"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				files, _, err := GenerateRun(t.Context(), Config{Quint: bin, Backend: backend, Spec: "testdata/terminal_samples.qnt", Dir: dir, Seed: "42", MaxSamples: 5, MaxSteps: 5})
				if err == nil || !strings.Contains(err.Error(), "unsupported Quint version \"0.33.0\"") || len(files) != 0 {
					t.Fatalf("unsupported runtime: files=%v error=%v", files, err)
				}
				if _, err := os.Stat(bin + ".work"); !os.IsNotExist(err) {
					t.Fatalf("unsupported CLI generated traces: %v", err)
				}
				if existing {
					data, err := os.ReadFile(stale)
					if err != nil || string(data) != "retained evidence" {
						t.Fatalf("existing evidence changed: %q %v", data, err)
					}
				} else if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("unsupported runtime created trace directory: %v", err)
				}
			})
		}
	}
}

func TestUnsupportedRuntimeChild(t *testing.T) {
	bin := os.Getenv("GOTOGETHER_OLD_RUNTIME_CHILD")
	if bin == "" {
		t.Skip("subprocess control")
	}
	Run(t, Config{Quint: bin, Backend: os.Getenv("GOTOGETHER_OLD_RUNTIME_BACKEND"), Spec: "testdata/terminal_samples.qnt", Dir: t.TempDir(), Seed: "42", MaxSamples: 5, MaxSteps: 5}, func(testing.TB) Driver {
		if err := os.WriteFile(os.Getenv("GOTOGETHER_OLD_RUNTIME_DRIVER"), []byte("called"), 0600); err != nil {
			t.Fatal(err)
		}
		return Switch{"init": Skip, "step": Skip}
	})
}

func TestUnsupportedRuntimeNeverReachesDriver(t *testing.T) {
	for _, backend := range []string{"typescript", "rust"} {
		t.Run(backend, func(t *testing.T) {
			bin := oldRuntime(t)
			marker := filepath.Join(t.TempDir(), "driver")
			cmd := exec.Command(os.Args[0], "-test.run=^TestUnsupportedRuntimeChild$", "-test.v", "-test.count=1")
			cmd.Env = append(os.Environ(), "GOTOGETHER_OLD_RUNTIME_CHILD="+bin, "GOTOGETHER_OLD_RUNTIME_BACKEND="+backend, "GOTOGETHER_OLD_RUNTIME_DRIVER="+marker)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "unsupported Quint version \"0.33.0\"") {
				t.Fatalf("missing rejected-runtime failure: %v\n%s", err, out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("driver executed for unsupported runtime: %v", err)
			}
		})
	}
}

func TestTraceCountOverridesRefusedBeforeEffects(t *testing.T) {
	for _, flag := range []string{"--max-samples", "--n-traces", "--maxSamples", "--nTraces"} {
		for _, equals := range []bool{false, true} {
			t.Run(flag+map[bool]string{false: "/separate", true: "/equals"}[equals], func(t *testing.T) {
				bin := oldRuntime(t)
				if err := os.WriteFile(bin, []byte("#!/bin/sh\necho called > \"$0.invoked\"\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
				args := []string{flag, "1"}
				if equals {
					args = []string{flag + "=1"}
				}
				dir := filepath.Join(t.TempDir(), "traces")
				_, _, err := GenerateRun(t.Context(), Config{Quint: bin, Spec: "testdata/terminal_samples.qnt", Dir: dir, Args: args, MaxSamples: 5})
				if err == nil || !strings.Contains(err.Error(), "use Config.MaxSamples instead") {
					t.Fatalf("coverage override: %v", err)
				}
				for _, path := range []string{dir, bin + ".invoked"} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("invalid override caused effects at %s: %v", path, err)
					}
				}
			})
		}
	}
}

func TestIncompleteRunReturnsPartialFilesAndError(t *testing.T) {
	bin := oldRuntime(t)
	script := `#!/bin/sh
if [ "$1" = --version ]; then echo 0.33.1; exit 0; fi
while [ "$#" -gt 0 ]; do
 if [ "$1" = --out-itf ]; then shift; out=$1; fi
 shift
done
out=$(printf '%s' "$out" | sed 's/{seq}/0/')
printf '{}\n' > "$out"
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, samples := range []int{1, 2} {
		files, _, err := GenerateRun(t.Context(), Config{Quint: bin, Spec: "testdata/terminal_samples.qnt", Dir: t.TempDir(), MaxSamples: samples})
		if len(files) != 1 {
			t.Fatalf("lost actual trace file: %v error=%v", files, err)
		}
		if samples == 1 {
			if err != nil {
				t.Fatalf("complete control failed: %v", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "generated 1 trace files for 2 requested samples") {
			t.Fatalf("short batch silently accepted: %v", err)
		}
		if _, err := os.Stat(files[0]); err != nil {
			t.Fatalf("partial evidence file lost: %v", err)
		}
	}
}
