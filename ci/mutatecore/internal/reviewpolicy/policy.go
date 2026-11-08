// Package reviewpolicy prototypes a strict reviewed-survivor ledger. It is not
// connected to mutatecore admission or its command line yet.
package reviewpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"slices"
	"strings"
)

// LedgerPath is the sole proposed allowance file, excluded from its own snapshot.
const LedgerPath = "ci/mutation-survivors.json"

// Identity names a registration in uninstrumented repository source.
type Identity struct {
	File       string `json:"file"`
	Ordinal    int    `json:"ordinal"`
	Descriptor string `json:"descriptor"`
}

// Review records human judgment, never inferred from observed survivors.
type Review struct {
	Identity
	SourceSHA256 string `json:"source_sha256"`
	Kind         string `json:"kind"`
	Rationale    string `json:"rationale"`
	Evidence     string `json:"evidence"`
}

// Context binds a closed input universe, including tests, models, generator/runtime,
// build configuration and dependency locks, plus externally supplied identities.
type Context struct {
	Files     map[string]string `json:"files"`
	Toolchain string            `json:"toolchain"`
	Settings  string            `json:"settings"`
}

// Policy is the single prerelease ledger format. Each admitted prefix must occur,
// even when its reviewed list is empty.
type Policy struct {
	Format   string              `json:"format"`
	Context  Context             `json:"context"`
	Packages map[string][]Review `json:"packages"`
}

// Snapshot hashes a supplied closed input tree, not arbitrary checkout contents.
// A future collector must include every build/review input, exclude runtime
// outputs, and reject additional unaccounted inputs (including ignored Go files).
// This function does not establish that collector boundary. Symlinks fail closed:
// their external referents cannot be bounded by this snapshot.
func Snapshot(root fs.FS, toolchain, settings string) (Context, error) {
	c := Context{Files: map[string]string{}, Toolchain: toolchain, Settings: settings}
	err := fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if name == LedgerPath || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("context %s: require regular repository file", name)
		}
		b, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		c.Files[name] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return Context{}, err
	}
	if err := validateContext(c); err != nil {
		return Context{}, err
	}
	return c, nil
}

// Load rejects unknown fields, duplicate JSON keys, trailing values and malformed
// reviews. It does not create or refresh approvals.
func Load(r io.Reader) (Policy, error) {
	b, err := io.ReadAll(io.LimitReader(r, 16<<20+1))
	if err != nil {
		return Policy{}, err
	}
	if len(b) > 16<<20 {
		return Policy{}, fmt.Errorf("policy exceeds 16 MiB")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	if err := uniqueJSON(d, "policy"); err != nil {
		return Policy{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return Policy{}, fmt.Errorf("policy: trailing JSON")
	}
	d = json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	var p Policy
	if err := d.Decode(&p); err != nil {
		return Policy{}, err
	}
	return p, p.Validate()
}

func uniqueJSON(d *json.Decoder, schema string) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				t, err := d.Token()
				if err != nil {
					return err
				}
				k, ok := t.(string)
				if !ok || seen[k] {
					return fmt.Errorf("policy: duplicate/invalid JSON key %v", t)
				}
				seen[k] = true
				child, valid := jsonField(schema, k)
				if !valid {
					return fmt.Errorf("policy: noncanonical/unknown %s field %q", schema, k)
				}
				if err := uniqueJSON(d, child); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := uniqueJSON(d, schema); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("policy: unexpected delimiter")
		}
		_, err = d.Token()
		return err
	}
	return nil
}

// jsonField deliberately uses exact spellings: encoding/json accepts case aliases
// for struct fields, which would otherwise allow a second semantic assignment.
func jsonField(schema, key string) (string, bool) {
	switch schema {
	case "policy":
		switch key {
		case "format":
			return "scalar", true
		case "context":
			return "context", true
		case "packages":
			return "packages", true
		}
	case "context":
		switch key {
		case "files":
			return "files", true
		case "toolchain", "settings":
			return "scalar", true
		}
	case "packages":
		return "review", true
	case "files":
		return "scalar", true
	case "review":
		switch key {
		case "file", "ordinal", "descriptor", "source_sha256", "kind", "rationale", "evidence":
			return "scalar", true
		}
	}
	return "", false
}

func validPath(s string) bool { return fs.ValidPath(s) && s != "." && !strings.Contains(s, "\\") }
func digest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}
func validateContext(c Context) error {
	if len(c.Files) == 0 || strings.TrimSpace(c.Toolchain) == "" || strings.TrimSpace(c.Settings) == "" {
		return fmt.Errorf("context: require repository files, exact toolchain identity and inventory settings")
	}
	for name, hash := range c.Files {
		if !validPath(name) || name == LedgerPath || !digest(hash) {
			return fmt.Errorf("context: invalid file/digest %q", name)
		}
	}
	return nil
}
func validID(id Identity) bool {
	return validPath(id.File) && id.Ordinal >= 0 && strings.TrimSpace(id.Descriptor) != ""
}
func site(id Identity) string { return fmt.Sprintf("%s ordinal %d", id.File, id.Ordinal) }

// Validate checks ledger structure independently of campaign evidence.
func (p Policy) Validate() error {
	if p.Format != "reviewed-survivors/prerelease-1" {
		return fmt.Errorf("policy: unsupported format %q", p.Format)
	}
	if err := validateContext(p.Context); err != nil {
		return err
	}
	if len(p.Packages) == 0 {
		return fmt.Errorf("policy: require explicit package entries")
	}
	seen := map[string]bool{}
	for prefix, reviews := range p.Packages {
		if !validPath(prefix) || reviews == nil {
			return fmt.Errorf("policy: package %q needs explicit review list", prefix)
		}
		for _, r := range reviews {
			key := site(r.Identity)
			if !validID(r.Identity) || !(r.File == prefix || strings.HasPrefix(r.File, prefix+"/")) || seen[key] {
				return fmt.Errorf("policy: invalid/duplicate reviewed identity %s", key)
			}
			seen[key] = true
			if !digest(r.SourceSHA256) || p.Context.Files[r.File] != r.SourceSHA256 {
				return fmt.Errorf("policy: reviewed source digest mismatch at %s", key)
			}
			if (r.Kind != "equivalent" && r.Kind != "external-runtime-gap") || strings.TrimSpace(r.Rationale) == "" || strings.TrimSpace(r.Evidence) == "" {
				return fmt.Errorf("policy: %s needs explicit kind, rationale and evidence", key)
			}
		}
	}
	return nil
}

// Admission binds validated policy to a specific inventory. Fields are private so
// callers cannot fabricate a successful admission or mutate its review set.
type Admission struct {
	validated bool
	inventory map[Identity]bool
	reviewed  map[Identity]bool
}

// Admit rejects context drift, missing/unknown package keys and stale/ambiguous
// identities before execution. Inventory is already unioned across suites.
func Admit(p Policy, current Context, inventory map[string][]Identity) (*Admission, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := validateContext(current); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(p.Context, current) {
		return nil, fmt.Errorf("review expired: context changed; inspect repository paths, toolchain and inventory settings before refreshing reviews")
	}
	a := &Admission{inventory: map[Identity]bool{}, reviewed: map[Identity]bool{}}
	if len(inventory) != len(p.Packages) {
		return nil, fmt.Errorf("policy: package set differs from inventory")
	}
	sites := map[string]Identity{}
	for prefix, ids := range inventory {
		reviews, ok := p.Packages[prefix]
		if !ok {
			return nil, fmt.Errorf("policy: unknown package %q", prefix)
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("inventory: package %q has no source mutants", prefix)
		}
		local := map[Identity]bool{}
		for _, id := range ids {
			key := site(id)
			if !validID(id) || !(id.File == prefix || strings.HasPrefix(id.File, prefix+"/")) || current.Files[id.File] == "" {
				return nil, fmt.Errorf("inventory: invalid source %s", key)
			}
			if _, ok := sites[key]; ok {
				return nil, fmt.Errorf("inventory: duplicate source %s", key)
			}
			sites[key] = id
			a.inventory[id] = true
			local[id] = true
		}
		for _, r := range reviews {
			if !local[r.Identity] {
				return nil, fmt.Errorf("reviewed site disappeared or descriptor drifted: %s (%s)", site(r.Identity), r.Descriptor)
			}
			a.reviewed[r.Identity] = true
		}
	}
	a.validated = true
	return a, nil
}

// Outcome is one resolved source outcome; a killed source needs its first
// killing suite as a witness. Errors and interrupted trials are not outcomes.
type Outcome struct {
	Identity     Identity
	Survived     bool
	KillingSuite string
}

// Difference is a read-only reconciliation result. Either list makes the gate red.
type Difference struct {
	validated   bool
	unexpected  []Identity
	newlyKilled []Outcome
}

// Check requires complete validated campaign evidence and exact set equality.
// The caller must validate its journal and successful durable writes before
// setting complete; this leaf does not parse journals or execute mutants.
func (a *Admission) Check(outcomes []Outcome, complete bool) (Difference, error) {
	var diff Difference
	if a == nil || !a.validated || !complete {
		return diff, fmt.Errorf("policy: require admitted, complete validated evidence")
	}
	seen := map[Identity]bool{}
	for _, o := range outcomes {
		if !a.inventory[o.Identity] || seen[o.Identity] {
			return Difference{}, fmt.Errorf("outcome: unknown/duplicate source %s", site(o.Identity))
		}
		seen[o.Identity] = true
		if !o.Survived && strings.TrimSpace(o.KillingSuite) == "" {
			return Difference{}, fmt.Errorf("outcome: killed source needs first killing suite")
		}
		if o.Survived && o.KillingSuite != "" {
			return Difference{}, fmt.Errorf("outcome: survivor cannot have killing suite")
		}
		if o.Survived && !a.reviewed[o.Identity] {
			diff.unexpected = append(diff.unexpected, o.Identity)
		}
		if !o.Survived && a.reviewed[o.Identity] {
			diff.newlyKilled = append(diff.newlyKilled, o)
		}
	}
	if len(seen) != len(a.inventory) {
		return Difference{}, fmt.Errorf("policy: incomplete source outcomes")
	}
	slices.SortFunc(diff.unexpected, compareID)
	slices.SortFunc(diff.newlyKilled, func(a, b Outcome) int { return compareID(a.Identity, b.Identity) })
	diff.validated = true
	return diff, nil
}
func compareID(a, b Identity) int {
	if n := strings.Compare(a.File, b.File); n != 0 {
		return n
	}
	if a.Ordinal < b.Ordinal {
		return -1
	}
	if a.Ordinal > b.Ordinal {
		return 1
	}
	return strings.Compare(a.Descriptor, b.Descriptor)
}

// Unexpected returns a copy of the unreviewed survivor identities.
func (d Difference) Unexpected() []Identity { return slices.Clone(d.unexpected) }

// NewlyKilled returns a copy of reviews requiring witness reconciliation.
func (d Difference) NewlyKilled() []Outcome { return slices.Clone(d.newlyKilled) }

// Passed reports validated exact agreement. Zero values and error results fail.
func (d Difference) Passed() bool {
	return d.validated && len(d.unexpected) == 0 && len(d.newlyKilled) == 0
}
