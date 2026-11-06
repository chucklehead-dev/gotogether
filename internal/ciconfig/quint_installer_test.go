package ciconfig

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const quintFixtureBinary = "#!/bin/sh\n[ \"$1\" = --help ] || exit 7\nprintf 'Quint simulator\\n'\n"

// Exercise the real installer without npm, Node, GitHub or a host evaluator.
// Only acquisition is stubbed; tar, checksum verification and publication are real.
func TestQuintInstallerPublishesVerifiedEvaluator(t *testing.T) {
	f := newQuintInstallerFixture(t)
	if out, err := f.run(nil); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	f.wantCache([]byte(quintFixtureBinary))
	f.wantCleanStage()
	fetch, err := os.ReadFile(f.fetchLog)
	if err != nil || !strings.Contains(string(fetch), "--connect-timeout\n15\n--max-time\n60\n--retry\n2\n--retry-max-time\n180") {
		t.Fatalf("download lacks finite connect/transfer/retry limits: %v\n%s", err, fetch)
	}
}

func TestQuintInstallerReusesOnlyVerifiedCache(t *testing.T) {
	f := newQuintInstallerFixture(t)
	f.cache([]byte(quintFixtureBinary))
	if out, err := f.run([]string{"FAKE_FETCH_EXIT=28"}); err != nil || !strings.Contains(out, "verified cache") {
		t.Fatalf("verified cache was not reused: %v\n%s", err, out)
	}
	if _, err := os.Stat(f.fetchLog); !os.IsNotExist(err) {
		t.Fatalf("cache reuse contacted the download service: %v", err)
	}
}

// An executable bit and successful startup do not make cached bytes valid.
// A verified release must replace an invalid entry, not merely leave it intact.
func TestQuintInstallerRepairsCorruptCache(t *testing.T) {
	f := newQuintInstallerFixture(t)
	f.cache([]byte("#!/bin/sh\nprintf 'corrupt cached executable\n'\n"))
	if out, err := f.run(nil); err != nil || !strings.Contains(out, "verified release") {
		t.Fatalf("corrupt cache was not repaired: %v\n%s", err, out)
	}
	if _, err := os.Stat(f.fetchLog); err != nil {
		t.Fatalf("corrupt cache was accepted without acquiring a verified release: %v", err)
	}
	f.wantCache([]byte(quintFixtureBinary))
	f.wantCleanStage()
}

func TestQuintInstallerRejectsCacheThatCannotStart(t *testing.T) {
	f := newQuintInstallerFixture(t)
	bad := []byte("#!/bin/sh\nexit 9\n")
	f.makeArchive(bad)
	f.pins(bad)
	f.cache(bad)
	if out, err := f.run(nil); err == nil {
		t.Fatalf("an executable with valid bytes but failed startup was reused:\n%s", out)
	}
	f.wantCache(bad)
	f.wantCleanStage()
}

func TestQuintInstallerFailuresPreserveCache(t *testing.T) {
	for _, failure := range []string{"download", "archive checksum", "extraction", "binary checksum", "startup"} {
		t.Run(failure, func(t *testing.T) {
			f := newQuintInstallerFixture(t)
			old := []byte("#!/bin/sh\nprintf 'old executable\\n'\n")
			f.cache(old)
			var env []string
			switch failure {
			case "download":
				// Even a complete archive followed by a failed transfer must
				// not be extracted or published as a cache hit next time.
				env = []string{"FAKE_FETCH_EXIT=28"}
			case "archive checksum":
				f.write(f.archive, []byte("not the admitted archive"), 0o600)
			case "extraction":
				f.write(f.archive, []byte("an admitted checksum is not a valid tar archive"), 0o600)
				f.pins([]byte(quintFixtureBinary))
			case "binary checksum":
				f.makeArchive([]byte("#!/bin/sh\nprintf 'wrong evaluator\\n'\n"))
				f.pins([]byte(quintFixtureBinary))
			case "startup":
				bad := []byte("#!/bin/sh\nexit 9\n")
				f.makeArchive(bad)
				f.pins(bad)
			}
			if out, err := f.run(env); err == nil {
				t.Fatalf("%s passed:\n%s", failure, out)
			}
			f.wantCache(old)
			f.wantCleanStage()
		})
	}
}

func TestQuintInstallerFailureDoesNotPublishExecutable(t *testing.T) {
	f := newQuintInstallerFixture(t)
	f.makeArchive([]byte("#!/bin/sh\nprintf 'unadmitted executable\\n'\n"))
	f.pins([]byte(quintFixtureBinary))
	if out, err := f.run(nil); err == nil {
		t.Fatalf("wrong binary passed:\n%s", out)
	}
	if _, err := os.Stat(f.binary); !os.IsNotExist(err) {
		t.Fatalf("failed verification published an executable: %v", err)
	}
	f.wantCleanStage()
}

func TestQuintInstallerRejectsMismatchedPair(t *testing.T) {
	for _, env := range [][]string{{"FAKE_CLI_VERSION=0.33.0"}, {"FAKE_EVALUATOR_VERSION=v0.7.0"}} {
		f := newQuintInstallerFixture(t)
		if out, err := f.run(env); err == nil {
			t.Fatalf("unadmitted pair passed: %v\n%s", env, out)
		}
		if _, err := os.Stat(f.fetchLog); !os.IsNotExist(err) {
			t.Fatalf("unadmitted pair reached acquisition: %v", err)
		}
	}
}

type quintInstallerFixture struct {
	t                               *testing.T
	dir, bin, script, archive       string
	cacheDir, binary, fetchLog, npm string
}

func newQuintInstallerFixture(t *testing.T) *quintInstallerFixture {
	t.Helper()
	f := &quintInstallerFixture{t: t, dir: t.TempDir()}
	f.bin = filepath.Join(f.dir, "tools")
	f.script = filepath.Join(f.dir, "install-quint.sh")
	f.archive = filepath.Join(f.dir, "release.tar.gz")
	f.fetchLog = filepath.Join(f.dir, "fetch.log")
	f.npm = filepath.Join(f.dir, "npm root's files")
	f.cacheDir = filepath.Join(f.dir, "cache", "rust-evaluator-v0.7.1")
	f.binary = filepath.Join(f.cacheDir, "quint_evaluator")
	if err := os.MkdirAll(f.bin, 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../ci/install-quint.sh")
	if err != nil {
		t.Fatal(err)
	}
	f.write(f.script, script, 0o700)
	f.tool("npm", `if [ "$1" = root ]; then printf '%s\n' "$FAKE_NPM_ROOT"; fi`)
	f.tool("quint", `printf '%s\n' "${FAKE_CLI_VERSION:-0.33.1}"`)
	f.tool("node", `[ "$3" = "$FAKE_NPM_ROOT/@informalsystems/quint/dist/src/rust/binaryManager.js" ] || exit 11
printf '%s\n' "${FAKE_EVALUATOR_VERSION:-v0.7.1}"`)
	f.tool("uname", `case "$1" in -s) echo Linux;; -m) echo x86_64;; *) echo 'Linux x86_64';; esac`)
	f.tool("curl", `printf '%s\n' "$@" > "$FAKE_FETCH_LOG"
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift 2;;
    --connect-timeout|--max-time|--retry|--retry-max-time) shift 2;;
    *) shift;;
  esac
done
cp "$FAKE_ARCHIVE" "$out"
exit "${FAKE_FETCH_EXIT:-0}"`)
	f.makeArchive([]byte(quintFixtureBinary))
	f.pins([]byte(quintFixtureBinary))
	return f
}

func (f *quintInstallerFixture) tool(name, body string) {
	f.write(filepath.Join(f.bin, name), []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o700)
}

func (f *quintInstallerFixture) write(path string, data []byte, mode os.FileMode) {
	f.t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		f.t.Fatal(err)
	}
}

func (f *quintInstallerFixture) makeArchive(binary []byte) {
	f.t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "quint_evaluator", Mode: 0o755, Size: int64(len(binary))}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		f.t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		f.t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.write(f.archive, b.Bytes(), 0o600)
}

func (f *quintInstallerFixture) pins(binary []byte) {
	archive, err := os.ReadFile(f.archive)
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(f.dir, "quint-evaluator.sha256"), []byte(fmt.Sprintf("%x  quint_evaluator-x86_64-unknown-linux-gnu.tar.gz\n%x  quint_evaluator-x86_64-unknown-linux-gnu\n", sha256.Sum256(archive), sha256.Sum256(binary))), 0o600)
}

func (f *quintInstallerFixture) cache(binary []byte) {
	f.t.Helper()
	if err := os.MkdirAll(f.cacheDir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	f.write(f.binary, binary, 0o700)
}

func (f *quintInstallerFixture) run(env []string) (string, error) {
	cmd := exec.CommandContext(f.t.Context(), "bash", f.script)
	cmd.Env = append(os.Environ(), "PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"QUINT_VERSION=0.33.1", "QUINT_HOME="+filepath.Join(f.dir, "cache"),
		"FAKE_NPM_ROOT="+f.npm, "FAKE_ARCHIVE="+f.archive, "FAKE_FETCH_LOG="+f.fetchLog)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *quintInstallerFixture) wantCache(want []byte) {
	f.t.Helper()
	got, err := os.ReadFile(f.binary)
	if err != nil || !bytes.Equal(got, want) {
		f.t.Fatalf("cache changed: %v\ngot %q\nwant %q", err, got, want)
	}
}

func (f *quintInstallerFixture) wantCleanStage() {
	f.t.Helper()
	entries, err := filepath.Glob(filepath.Join(f.cacheDir, ".install.*"))
	if err != nil || len(entries) != 0 {
		f.t.Fatalf("staging entries remain: %v, %v", entries, err)
	}
}
