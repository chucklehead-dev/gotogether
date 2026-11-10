package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// inputManifest describes static Go build inputs, not a hermetic test runtime.
// Discovery is repeated at sealing: added source files and embed matches are
// inputs even when ignored by Git. Documentation outside these inputs is not.
type inputManifest struct {
	Eligible        bool              `json:"eligible"`
	Digest          string            `json:"digest"`
	Settings        map[string]string `json:"settings"`
	Files           []inputFile       `json:"files"`
	Tools           []inputFile       `json:"tools"`
	Discoveries     []inputDiscovery  `json:"discoveries"`
	Limitations     []string          `json:"limitations"`
	TargetDiscovery []byte            `json:"-"`
}
type inputFile struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256,omitempty"`
	Size     int64  `json:"size"`
	Mode     uint32 `json:"mode"`
	Link     string `json:"link,omitempty"`
	Resolved string `json:"resolved,omitempty"`
	Absent   bool   `json:"absent,omitempty"`
}
type inputDiscovery struct {
	Directory string         `json:"directory"`
	Arguments []string       `json:"arguments"`
	Packages  []inputPackage `json:"packages"`
}

// Keep the selected membership, including external-test and embed membership,
// rather than unstable go-list cache diagnostics or generated export paths.
type inputPackage struct {
	ImportPath                                                                                              string
	Dir                                                                                                     string
	ForTest                                                                                                 string
	Standard                                                                                                bool
	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SwigFiles, SwigCXXFiles, SysoFiles []string
	TestGoFiles, XTestGoFiles                                                                               []string
	EmbedPatterns, EmbedFiles, TestEmbedPatterns, TestEmbedFiles, XTestEmbedPatterns, XTestEmbedFiles       []string
	Imports, TestImports, XTestImports                                                                      []string
	Module                                                                                                  *inputModule
	Error                                                                                                   *struct{ Err string }
	DepsErrors                                                                                              []struct{ Err string }
}
type inputModule struct {
	Path, Version, Sum, GoMod, Dir string
	Main                           bool
	Replace                        *inputModule
}

func collectInputs(ctx context.Context, root, prefix string) (inputManifest, error) {
	var m inputManifest
	root, err := filepath.Abs(root)
	if err != nil {
		return m, err
	}
	if prefix == "" || filepath.IsAbs(prefix) || strings.Contains(prefix, "\\") || strings.Contains(prefix, "..") || strings.ContainsAny(prefix, " \t\r\n") {
		return m, fmt.Errorf("unsupported mutation prefix %q: use a relative package directory", prefix)
	}
	settings := []string{"GOOS", "GOARCH", "GOAMD64", "GOARM", "GOARM64", "GO386", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOEXPERIMENT", "GOFLAGS", "CGO_ENABLED", "GOROOT", "GOTOOLDIR", "GOVERSION", "GOTOOLCHAIN", "GOENV", "GOWORK", "GOMOD", "GO111MODULE", "GOPATH", "GOMODCACHE", "GOCACHEPROG", "GODEBUG", "GOFIPS140", "GO_EXTLINK_ENABLED", "CC", "CXX", "FC", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_FFLAGS", "CGO_LDFLAGS"}
	output, err := inputCommand(ctx, root, append([]string{"env", "-json"}, settings...)...)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(output, &m.Settings); err != nil {
		return m, fmt.Errorf("decode build settings: %w", err)
	}
	if m.Settings["GOCACHEPROG"] != "" {
		return m, unsupportedInput("GOCACHEPROG %q: external cache producers are not accounted", m.Settings["GOCACHEPROG"])
	}
	// Flags changing the input universe need explicit collection, not a label.
	for _, flag := range strings.Fields(m.Settings["GOFLAGS"]) {
		if !(strings.HasPrefix(flag, "-tags=") || flag == "-trimpath" || flag == "-mod=readonly" || flag == "-buildvcs=false" || flag == "-buildvcs=true" || flag == "-buildvcs=auto") || strings.ContainsAny(flag, "'\"") {
			return m, unsupportedInput("GOFLAGS input %q: overlays, alternate tool executors and other flags need explicit input accounting", flag)
		}
	}
	files := map[string]inputFile{}
	add := func(path string, optional bool) error {
		record, err := fingerprintInput(path, optional)
		if err != nil {
			return err
		}
		files[record.Path] = record
		return nil
	}
	for _, name := range []string{"orchestrion.tool.go", "gosim.tool.go"} {
		p := filepath.Join(root, name)
		if _, err := os.Lstat(p); err == nil {
			return m, unsupportedInput("aspect tool input %s: aspect package/YAML closure is not collected", p)
		} else if !os.IsNotExist(err) {
			return m, err
		}
	}
	if m.Settings["GOENV"] != "off" {
		if err := add(m.Settings["GOENV"], true); err != nil {
			return m, err
		}
	}
	work := m.Settings["GOWORK"]
	if work != "" && work != "off" {
		if err := add(work, false); err != nil {
			return m, err
		}
		if err := add(work+".sum", true); err != nil {
			return m, err
		}
	}
	// This is qweave's actual default graph, not just the binaries' target graph:
	// the CLI passes no Config.Patterns and Weave loads ./... including tests.
	requests := []struct {
		dir  string
		args []string
	}{
		{root, []string{"list", "-json", "./" + prefix + "/..."}},
		{root, []string{"list", "-deps", "-test", "-json", "./..."}},
		{filepath.Join(root, "qweave"), []string{"list", "-deps", "-json", "./cmd/qweave"}},
	}
	nativeInputs := false
	nativeToolSettings := map[string]bool{}
	for i, request := range requests {
		output, err := inputCommand(ctx, request.dir, request.args...)
		if err != nil {
			return m, err
		}
		if i == 0 {
			m.TargetDiscovery = output
		}
		packages, err := decodeInputPackages(output)
		if err != nil {
			return m, err
		}
		discovery := inputDiscovery{Directory: request.dir, Arguments: request.args, Packages: packages}
		m.Discoveries = append(m.Discoveries, discovery)
		for _, p := range packages {
			if p.Error != nil || len(p.DepsErrors) > 0 {
				return m, fmt.Errorf("unresolved build package %s: go list returned package errors", p.ImportPath)
			}
			if len(p.CgoFiles)+len(p.CFiles)+len(p.CXXFiles)+len(p.MFiles)+len(p.FFiles)+len(p.SwigFiles)+len(p.SwigCXXFiles)+len(p.SysoFiles) > 0 {
				nativeInputs = true
				nativeToolSettings["CC"] = true
				if len(p.CXXFiles)+len(p.SwigCXXFiles) > 0 {
					nativeToolSettings["CXX"] = true
				}
				if len(p.FFiles) > 0 {
					nativeToolSettings["FC"] = true
				}
				m.Limitations = append(m.Limitations, "Native dependency closure not sealed for package "+p.ImportPath)
			}
			groups := [][]string{p.GoFiles, p.TestGoFiles, p.XTestGoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.FFiles, p.SwigFiles, p.SwigCXXFiles, p.SysoFiles, p.HFiles, p.SFiles, p.EmbedFiles, p.TestEmbedFiles, p.XTestEmbedFiles}
			for _, group := range groups {
				for _, name := range group {
					full := name
					if !filepath.IsAbs(full) {
						full = filepath.Join(p.Dir, name)
					}
					if err := add(full, false); err != nil {
						return m, fmt.Errorf("%s input: %w", p.ImportPath, err)
					}
				}
			}
			// PGO's conventional profile is an automatic go build input when present.
			if p.Dir != "" {
				if err := add(filepath.Join(p.Dir, "default.pgo"), true); err != nil {
					return m, err
				}
			}
			for module := p.Module; module != nil; module = module.Replace {
				if module.GoMod != "" {
					if err := add(module.GoMod, false); err != nil {
						return m, err
					}
					sum := strings.TrimSuffix(module.GoMod, ".mod") + ".sum"
					if filepath.Base(module.GoMod) == "go.mod" {
						sum = filepath.Join(filepath.Dir(module.GoMod), "go.sum")
					}
					// Downloaded modules have a cache .mod without a sibling .sum. The
					// owning main module's locks and actual selected bytes are collected.
					if module.Main || module.Replace != nil || filepath.Base(module.GoMod) == "go.mod" {
						if err := add(sum, true); err != nil {
							return m, err
						}
					}
				}
			}
		}
	}
	// go list's source closure includes the selected standard library sources;
	// binaries identify the actual compiler/linker/assembler, not version labels.
	goPath, err := exec.LookPath("go")
	if err != nil {
		return m, err
	}
	paths := []string{goPath, filepath.Join(m.Settings["GOROOT"], "bin", "go")}
	for _, name := range []string{"compile", "link", "asm", "preprofile"} {
		paths = append(paths, filepath.Join(m.Settings["GOTOOLDIR"], name))
	}
	if nativeInputs {
		paths = append(paths, filepath.Join(m.Settings["GOTOOLDIR"], "cgo"))
		for _, setting := range []string{"CC", "CXX", "FC"} {
			if !nativeToolSettings[setting] {
				continue
			}
			name := strings.Fields(m.Settings[setting])
			if len(name) == 0 {
				continue
			}
			binary, err := exec.LookPath(name[0])
			if err != nil {
				m.Limitations = append(m.Limitations, fmt.Sprintf("Native tool %s=%q unresolved: %v", setting, m.Settings[setting], err))
				continue
			}
			paths = append(paths, binary)
		}
		m.Limitations = append(m.Limitations, "Selected native source/object files and resolved native compiler binaries are recorded; transitive system headers, external libraries, linker scripts and native compiler helper executables are not accounted.")
	}
	headers, err := filepath.Glob(filepath.Join(m.Settings["GOROOT"], "pkg", "include", "*.h"))
	if err != nil {
		return m, err
	}
	for _, header := range headers {
		if err := add(header, false); err != nil {
			return m, err
		}
	}
	seen := map[string]bool{}
	for _, path := range paths {
		f, err := fingerprintInput(path, false)
		if err != nil {
			return m, err
		}
		if !seen[f.Path] {
			m.Tools = append(m.Tools, f)
			seen[f.Path] = true
		}
	}
	for _, file := range files {
		m.Files = append(m.Files, file)
	}
	slices.SortFunc(m.Files, func(a, b inputFile) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(m.Tools, func(a, b inputFile) int { return strings.Compare(a.Path, b.Path) })
	m.Limitations = append(m.Limitations, []string{"VCS metadata embedded by go build is not independently collected; the final built binary digest records its resulting bytes.", "Static Go source/build closure only; arbitrary test runtime file reads, external commands/services and hostile concurrent changes are not authenticated.", "The generator currently loads the entire root ./... graph; unrelated root Go source changes can change this context, while unrelated documentation is excluded.", "Aspect tool configurations, external cache programs and unaccounted GOFLAGS are unsupported; a parent can retain an explicitly uncollected receipt without treating it as identity evidence."}...)
	content, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	digest := sha256.Sum256(content)
	m.Digest = fmt.Sprintf("%x", digest)
	return m, nil
}

func inputCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := groupCommand(ctx, "go", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("collect build inputs in %s: go %s: %w: %s", dir, strings.Join(args, " "), err, stderr.String())
	}
	return output, nil
}
func decodeInputPackages(data []byte) ([]inputPackage, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	var out []inputPackage
	for {
		var p inputPackage
		if err := decoder.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode go input discovery: %w", err)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty build input discovery")
	}
	slices.SortFunc(out, func(a, b inputPackage) int { return strings.Compare(a.ImportPath, b.ImportPath) })
	return out, nil
}
func fingerprintInput(path string, optional bool) (inputFile, error) {
	var f inputFile
	if path == "" || path == "off" {
		return f, fmt.Errorf("invalid input path %q", path)
	}
	full, err := filepath.Abs(path)
	if err != nil {
		return f, err
	}
	f.Path = full
	stat, err := os.Lstat(full)
	if err != nil {
		if optional && os.IsNotExist(err) {
			f.Absent = true
			return f, nil
		}
		return f, fmt.Errorf("account input %s: %w", full, err)
	}
	f.Mode = uint32(stat.Mode())
	if stat.Mode()&os.ModeSymlink != 0 {
		f.Link, err = os.Readlink(full)
		if err != nil {
			return f, err
		}
		f.Resolved, err = filepath.EvalSymlinks(full)
		if err != nil {
			return f, fmt.Errorf("resolve input link %s: %w", full, err)
		}
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return f, fmt.Errorf("read input %s: %w", full, err)
	}
	f.Size = int64(len(content))
	digest := sha256.Sum256(content)
	f.SHA256 = fmt.Sprintf("%x", digest)
	return f, nil
}

// An unsupported collection does not change count-gate execution semantics. Its
// parent may retain the reason and execute without claiming collected identity.
type unsupportedInputsError struct{ Reason string }

func (e *unsupportedInputsError) Error() string {
	return "unsupported mutation input collection: " + e.Reason
}
func unsupportedInput(format string, args ...any) error {
	return &unsupportedInputsError{Reason: fmt.Sprintf(format, args...)}
}
