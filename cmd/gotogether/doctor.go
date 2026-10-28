package main

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/woven"
)

// coreModule is the core's module path (read-only).
const coreModule = "github.com/chucklehead-dev/gotogether"

// A piece is one installed thing doctor reports: its name, its version
// (or why it has none), and a skew with its fix, if any.
type piece struct {
	name, version, note string
	skew                string // "" when it agrees with the rest
}

// doctor reports the installed pieces' versions (the CLI and the core it
// was built with, the woven ABI and the formats it reads, the module's
// core and published modules, qweave, gotogether-nav, binaries named on
// the command line, quint, its evaluator and Node) and names each skew
// with its fix. It is the version-reporting core of I21's doctor.
func doctor(out io.Writer, args []string) (int, error) {
	fset := commandFlags("doctor")
	dir := fset.String("C", ".", "the module whose versions to compare (its go.mod)")
	strict := fset.Bool("strict", false, "exit 1 when a skew is found (CI)")
	fset.Parse(args)
	var ps []piece
	cli := buildOf(debug.ReadBuildInfo())
	ps = append(ps, piece{name: "gotogether (the CLI and its core)", version: cli.String(), note: fmt.Sprintf("woven ABI%d", woven.ABI1)})
	mod, modNote := moduleCore(*dir)
	if mod.path != "" {
		p := piece{name: "the module's core (" + *dir + ")", version: mod.version, note: modNote}
		if !cli.same(mod.version) {
			p.skew = fmt.Sprintf("the CLI is %s, the module's core %s: formats and the navigation protocol may differ; install the CLI at the module's version (go install %s/cmd/gotogether@%s)", cli, mod.version, coreModule, installable(mod.version))
		}
		ps = append(ps, p)
		for _, m := range mod.published {
			ps = append(ps, piece{name: "  " + m.Path, version: m.Version})
		}
	} else {
		ps = append(ps, piece{name: "the module's core", version: "none", note: modNote})
	}
	ps = append(ps, qweavePiece(mod), navPiece(mod))
	for _, bin := range fset.Args() {
		ps = append(ps, binaryPiece(bin, mod))
	}
	ps = append(ps, quintPieces()...)
	for _, p := range ps {
		v := p.version
		if p.note != "" {
			v += " (" + p.note + ")"
		}
		fmt.Fprintf(out, "%-44s %s\n", p.name, v)
	}
	fmt.Fprintln(out, "\nformats this gotogether reads:")
	for _, f := range formats.All() {
		fmt.Fprintf(out, "  %-28s %s\n", f.Range(), f.What)
	}
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GOTOGETHER_") || strings.HasPrefix(kv, "QUINT_") {
			env = append(env, kv)
		}
	}
	if len(env) > 0 {
		slices.Sort(env)
		fmt.Fprintln(out, "\nin effect:")
		for _, kv := range env {
			fmt.Fprintln(out, "  "+kv)
		}
	}
	skews := 0
	for _, p := range ps {
		if p.skew != "" {
			if skews == 0 {
				fmt.Fprintln(out, "\nskew:")
			}
			skews++
			fmt.Fprintf(out, "  %s: %s\n", strings.TrimSpace(p.name), p.skew)
		}
	}
	if skews == 0 {
		fmt.Fprintln(out, "\nno skew found")
	} else if *strict {
		return 1, nil
	}
	return 0, nil
}

// A build is what a binary was built from: its module's version, else
// the commit it was built at.
type build struct{ version, revision string }

func buildOf(bi *debug.BuildInfo, ok bool) build {
	if !ok || bi == nil {
		return build{version: "unknown"}
	}
	b := build{version: bi.Main.Version}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			b.revision = s.Value
		}
	}
	return b
}

func (b build) String() string {
	if (b.version == "" || b.version == "(devel)") && b.revision != "" {
		return "commit " + short(b.revision)
	}
	if b.version == "" {
		return "(devel)"
	}
	return b.version
}

// same reports whether the build is version v (a module version, a
// pseudo-version naming a commit). A build of unknown version agrees with
// anything: doctor cannot tell.
func (b build) same(v string) bool {
	switch {
	case v == "" || strings.HasPrefix(v, "replaced"):
		return true
	case b.version != "" && b.version != "(devel)":
		return b.version == v
	case b.revision != "":
		c := pseudoCommit(v)
		return c != "" && strings.HasPrefix(b.revision, c)
	}
	return true
}

// rePseudo is a pseudo-version's commit (read-only).
var rePseudo = regexp.MustCompile(`-([0-9a-f]{12})(\+incompatible)?$`)

// pseudoCommit is the commit a pseudo-version names, or "".
func pseudoCommit(v string) string {
	if m := rePseudo.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return ""
}

// installable is a version as go install takes it: a pseudo-version's
// commit, a tag as it is.
func installable(v string) string {
	if c := pseudoCommit(v); c != "" {
		return c
	}
	return v
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// A moduleInfo is the core and the published modules a module requires.
type moduleInfo struct {
	path, version string
	published     []listedModule
}

type listedModule struct {
	Path    string
	Version string
	Main    bool // a main module (the module itself, or one of a go.work)
	Replace *struct{ Path, Version string }
}

// moduleCore reads the core and the toolkit's modules the module in dir
// resolves (go list -m all): none, with why, outside a module.
func moduleCore(dir string) (moduleInfo, string) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return moduleInfo{}, "not in a module that builds: " + firstLine(stderr.String(), err)
	}
	var info moduleInfo
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var m listedModule
		if dec.Decode(&m) != nil {
			break
		}
		v := m.Version
		if m.Main {
			v = "replaced by the workspace's" // a main module is the source at hand
			if m.Path == coreModule {
				v = "replaced by the source at hand (a main module)"
			}
		} else if m.Replace != nil {
			v = "replaced by " + m.Replace.Path
			if m.Replace.Version != "" {
				v += " " + m.Replace.Version
			}
		}
		switch {
		case m.Path == coreModule:
			info.path, info.version = m.Path, v
		case strings.HasPrefix(m.Path, coreModule+"/") && !m.Main:
			info.published = append(info.published, listedModule{Path: m.Path, Version: v})
		}
	}
	if info.path == "" {
		return info, "the module requires no gotogether"
	}
	return info, ""
}

func firstLine(s string, err error) string {
	if s = strings.TrimSpace(s); s != "" {
		l, _, _ := strings.Cut(s, "\n")
		return l
	}
	return err.Error()
}

// reQweave is qweave -version's line (read-only).
var reQweave = regexp.MustCompile(`^qweave (\S+) woven (ABI\d+)`)

// qweavePiece is qweave on PATH: its version and the woven ABI its code
// calls, against the CLI's core's.
func qweavePiece(mod moduleInfo) piece {
	p := piece{name: "qweave"}
	bin, err := exec.LookPath("qweave")
	if err != nil {
		p.version, p.note = "not installed", "go install "+coreModule+"/qweave/cmd/qweave@latest, for -cover, -mutate and weaving"
		return p
	}
	out, _ := exec.Command(bin, "-version").Output()
	m := reQweave.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		p.version = "unknown"
		p.skew = fmt.Sprintf("%s predates -version, and with it the woven ABI's check: reinstall it (go install %s/qweave/cmd/qweave@latest)", bin, coreModule)
		return p
	}
	p.version, p.note = m[1], "writes woven "+m[2]
	if want := fmt.Sprintf("ABI%d", woven.ABI1); m[2] != want {
		p.skew = fmt.Sprintf("it writes woven %s, this gotogether's core has %s: install the qweave of the module's core version (go install %s/qweave/cmd/qweave@%s)", m[2], want, coreModule, installable(mod.version))
	}
	return p
}

// navPiece is gotogether-nav on PATH: the core it was built with, which
// fixes its navigation protocol, against the module's core.
func navPiece(mod moduleInfo) piece {
	p := piece{name: "gotogether-nav"}
	bin, err := exec.LookPath("gotogether-nav")
	if err != nil {
		p.version, p.note = "not installed", "optional: go install "+coreModule+"/qnav/cmd/gotogether-nav@latest"
		return p
	}
	q := binaryPiece(bin, mod)
	q.name = p.name
	return q
}

// binaryPiece is a Go binary built with the core: the core version it
// was built with, against the module's.
func binaryPiece(bin string, mod moduleInfo) piece {
	p := piece{name: filepath.Base(bin)}
	bi, err := buildinfo.ReadFile(bin)
	if err != nil {
		p.version, p.note = "unknown", err.Error()
		return p
	}
	p.version = bi.Main.Version
	core := ""
	for _, d := range bi.Deps {
		if d.Path == coreModule {
			core = d.Version
			if d.Replace != nil {
				core = "replaced by " + d.Replace.Path
			}
		}
	}
	if bi.Main.Path == coreModule {
		core = bi.Main.Version
	}
	if core == "" {
		p.note = "built without the core"
		return p
	}
	p.note = "core " + core
	if mod.version != "" && !strings.HasPrefix(mod.version, "replaced") && !strings.HasPrefix(core, "replaced") && core != mod.version && core != "(devel)" {
		p.skew = fmt.Sprintf("built with core %s, the module's is %s: rebuild it against the module's core (go install %s@%s, or go build in the module)", core, mod.version, bi.Path, installable(mod.version))
	}
	return p
}

// reEvaluator is the evaluator version quint's binary manager expects
// (read-only).
var reEvaluator = regexp.MustCompile(`QUINT_EVALUATOR_VERSION\s*=\s*['"]([^'"]+)['"]`)

// quintPieces are quint, the Rust evaluator its version expects, and
// Node.
func quintPieces() []piece {
	q := piece{name: "quint"}
	ev := piece{name: "quint's Rust evaluator"}
	node := piece{name: "node"}
	if bin, err := exec.LookPath("node"); err == nil {
		out, _ := exec.Command(bin, "--version").Output()
		node.version = strings.TrimSpace(string(out))
	} else {
		node.version, node.note = "not installed", "quint runs on Node: install Node 22"
	}
	bin, err := exec.LookPath("quint")
	if err != nil {
		q.version, q.note = "not installed", "npm i -g @informalsystems/quint; tests that need it skip"
		ev.version = "-"
		return []piece{q, ev, node}
	}
	out, _ := exec.Command(bin, "--version").Output()
	q.version = strings.TrimSpace(string(out))
	want := evaluatorOf(bin)
	if want == "" {
		ev.version = "unknown"
		ev.note = "quint's binary manager not found beside it"
		return []piece{q, ev, node}
	}
	home := os.Getenv("QUINT_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".quint")
	}
	ev.version = want
	if _, err := os.Stat(filepath.Join(home, "rust-evaluator-"+want, "quint_evaluator")); err != nil {
		ev.note = "not installed in " + home
		ev.skew = fmt.Sprintf("quint %s runs evaluator %s, not in %s: quint fetches it on first use (rate-limited by GitHub), or QUINT_VERSION=%s ci/install-quint.sh installs it", q.version, want, home, q.version)
	} else {
		ev.note = "installed"
	}
	return []piece{q, ev, node}
}

// evaluatorOf is the evaluator version the quint at bin expects, read
// from its package's binary manager ("" if it cannot be found).
func evaluatorOf(bin string) string {
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return ""
	}
	// npm links bin/quint to <package>/dist/src/cli.js
	for d := filepath.Dir(real); d != filepath.Dir(d); d = filepath.Dir(d) {
		b, err := os.ReadFile(filepath.Join(d, "dist", "src", "rust", "binaryManager.js"))
		if err == nil {
			if m := reEvaluator.FindSubmatch(b); m != nil {
				return string(m[1])
			}
			return ""
		}
	}
	return ""
}
