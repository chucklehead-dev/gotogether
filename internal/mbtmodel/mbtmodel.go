// Package mbtmodel is a spec's main module compiled for connect's traces
// (gotogether.mbt/1; gotogether compile -spec, validate.CompileMBT): the
// artifact connect reads where quint is not installed (DESIGN §110). Its
// own package, below connect and validate, so the CLI writes and checks it
// without importing connect (and testing).
package mbtmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// Format is the format of a compiled model for connect (Model).
const Format = "gotogether.mbt/1"

// A Model is a spec's main module compiled for connect, as its file
// holds it: quint's IR with every definition of the main module kept
// (its vals and defs, for Require and Step.Eval), and what it was
// compiled from.
type Model struct {
	// Format is Format.
	Format string `json:"format"`
	// Quint is `quint --version` of the quint that compiled it.
	Quint string `json:"quint"`
	// Gotogether is the core version that compiled it ("" when the build
	// does not say).
	Gotogether string `json:"gotogether,omitempty"`
	// Spec is the spec's path relative to the artifact's directory, Main
	// its main module ("" for quint's default, the file's name).
	Spec string `json:"spec"`
	Main string `json:"main,omitempty"`
	// Sources are the spec and the files it imports, each path relative
	// to the artifact's directory, with its sha256.
	Sources []Source `json:"sources"`
	// IR is quint compile's JSON of the main module.
	IR json.RawMessage `json:"ir"`
}

// A Source is one source of a compiled model: its path and sha256.
type Source struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// CompiledFor is where a spec's compiled model is kept by convention:
// beside the spec, its name's stem with .mbt.json (model/bank.qnt's is
// model/bank.mbt.json), or with the main module's name between them when
// main is not the file's own (model/bank.bankDesign.mbt.json).
func CompiledFor(spec, main string) string {
	dir, name := filepath.Split(spec)
	stem := strings.TrimSuffix(name, ".qnt")
	if main != "" && main != stem {
		stem += "." + main
	}
	return filepath.Join(dir, stem+".mbt.json")
}

// Write writes the compiled model to path, as JSON.
func (cm *Model) Write(path string) error {
	data, err := json.Marshal(cm)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Differs lists what differs in meaning between cm and other: the spec,
// its main module, its sources and the IR. The versions that compiled
// them are provenance. Empty when they agree.
func (cm *Model) Differs(other *Model) []string {
	var d []string
	if cm.Format != other.Format {
		d = append(d, fmt.Sprintf("format (%s, %s)", cm.Format, other.Format))
	}
	if cm.Spec != other.Spec || cm.Main != other.Main {
		d = append(d, fmt.Sprintf("the spec (%s %s, %s %s)", cm.Spec, cm.Main, other.Spec, other.Main))
	}
	if !slices.Equal(cm.Sources, other.Sources) {
		d = append(d, "the sources")
	}
	var a, b any
	if json.Unmarshal(cm.IR, &a) != nil || json.Unmarshal(other.IR, &b) != nil || !jsonEqual(a, b) {
		d = append(d, "the IR")
	}
	return d
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Load reads a compiled model for connect, refusing another format, a
// quint outside the admission policy and a stale artifact: a source
// present beside it whose sha256 is not the artifact's (an absent one is
// not checked: notes says so).
func Load(path string) (cm *Model, notes []string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	cm = &Model{}
	if err := json.Unmarshal(data, cm); err != nil {
		return nil, nil, fmt.Errorf("compiled model %s: %w: compile it again (gotogether compile -spec S)", path, err)
	}
	if _, err := formats.MBT.Check(cm.Format); err != nil {
		return nil, nil, fmt.Errorf("compiled model %s: %w", path, err)
	}
	if !quintcmd.Supported(cm.Quint) {
		return nil, nil, fmt.Errorf("compiled model %s: compiled with unsupported quint %q; use stable Quint %s or later and compile it again (gotogether compile -spec %s)", path, cm.Quint, quintcmd.MinimumVersion, cm.Spec)
	}
	if len(cm.IR) == 0 || len(cm.Sources) == 0 {
		return nil, nil, fmt.Errorf("compiled model %s holds no model (no IR or sources): compile it again (gotogether compile -spec %s)", path, cm.Spec)
	}
	dir := filepath.Dir(path)
	for _, s := range cm.Sources {
		p := filepath.Join(dir, filepath.FromSlash(s.Path))
		src, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			notes = append(notes, fmt.Sprintf("source %s is absent: the compiled model's hash taken for it", s.Path))
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(src)
		if hex.EncodeToString(sum[:]) != s.SHA256 {
			return nil, nil, fmt.Errorf("compiled model %s is stale: %s changed since it was compiled: compile it again (gotogether compile -spec %s%s) where quint is installed",
				path, s.Path, filepath.Join(dir, filepath.FromSlash(cm.Spec)), MainFlag(cm.Main))
		}
	}
	return cm, notes, nil
}

// MainFlag is the -main flag of gotogether compile -spec for main.
func MainFlag(main string) string {
	if main == "" {
		return ""
	}
	return " -main " + main
}
