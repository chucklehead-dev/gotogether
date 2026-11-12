package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// journalVersion and receiptVersion name the one current shape. Prerelease
// evidence has no compatibility reader: an older journal or receipt is refused.
const (
	journalVersion = 2
	receiptVersion = 2
)

// shardSpec selects every count-th source of the full ordered inventory,
// starting at index. Count 1 (index 0) is the unsharded campaign.
type shardSpec struct {
	Index int `json:"index"`
	Count int `json:"count"`
}

func parseShard(s string) (shardSpec, error) {
	i, n, ok := strings.Cut(s, "/")
	index, err1 := strconv.Atoi(i)
	count, err2 := strconv.Atoi(n)
	if !ok || err1 != nil || err2 != nil || count < 1 || index < 0 || index >= count || strconv.Itoa(index) != i || strconv.Itoa(count) != n {
		return shardSpec{}, fmt.Errorf("-shard %q: want i/n with 0 <= i < n (e.g. -shard 0/4)", s)
	}
	return shardSpec{index, count}, nil
}

func (s shardSpec) String() string { return fmt.Sprintf("%d/%d", s.Index, s.Count) }

// selection is applied only after the complete inventory has been admitted
// (every suite built, inventoried and its baseline passed), so every shard
// selects from the same ordered list. It returns indices into that list.
func (s shardSpec) selection(total int) []int {
	selected := []int{}
	for i := s.Index; i < total; i += s.Count {
		selected = append(selected, i)
	}
	return selected
}

// gatePolicy is where the survivor limit is decided. A shard of a split
// campaign never applies it: the aggregate applies the package's unchanged
// max once, to the sum of every shard's survivors.
type gatePolicy struct {
	Max    int
	Shards int
}

func unsharded(max int) gatePolicy { return gatePolicy{Max: max, Shards: 1} }

// Gate values a complete record may carry.
const (
	gatePassed   = "passed"
	gateFailed   = "failed"
	gateDeferred = "deferred_to_aggregate"
)

func (g gatePolicy) verdict(survivors int) string {
	switch {
	case g.Shards > 1:
		return gateDeferred
	case g.Max < 0 || survivors <= g.Max:
		return gatePassed
	}
	return gateFailed
}

// sharedSource is one entry of the full ordered candidate inventory, by its
// source identity (file and file-local ordinal, which keeps repeated identical
// descriptors distinct) and the suites linking it, by package, in the order
// they run. Local binary IDs are not part of it.
type sharedSource struct {
	File       string   `json:"file"`
	Ordinal    int      `json:"ordinal"`
	Descriptor string   `json:"descriptor"`
	Suites     []string `json:"suites"`
}

type candidateInventory struct {
	Count   int            `json:"count"`
	Digest  string         `json:"digest"`
	Sources []sharedSource `json:"sources"`
}

// inventoryOf admits the manifest's independently executed suite inventories
// (no baselines run) into the ordered candidate list every shard selects from.
func inventoryOf(manifest producerManifest) (candidateInventory, []candidate, error) {
	suites, err := manifestSuites(manifest)
	if err != nil {
		return candidateInventory{}, nil, err
	}
	candidates, err := admit(suites, func(suite) error { return nil })
	if err != nil {
		return candidateInventory{}, nil, err
	}
	inv := candidateInventory{Count: len(candidates), Sources: make([]sharedSource, len(candidates))}
	for i, c := range candidates {
		s := sharedSource{File: c.file, Ordinal: c.ordinal, Descriptor: c.descriptor}
		for _, t := range c.targets {
			s.Suites = append(s.Suites, suites[t.suite].pkg)
		}
		inv.Sources[i] = s
	}
	inv.Digest, err = valueDigest(inv.Sources)
	return inv, candidates, err
}

func manifestSuites(manifest producerManifest) ([]suite, error) {
	if len(manifest.Suites) == 0 {
		return nil, errors.New("manifest suite roster incomplete")
	}
	suites := make([]suite, len(manifest.Suites))
	packages := map[string]bool{}
	for i, s := range manifest.Suites {
		if s.Index != i || s.Package == "" || packages[s.Package] {
			return nil, errors.New("manifest suite roster invalid")
		}
		packages[s.Package] = true
		suites[i] = suite{pkg: s.Package, dir: s.Directory, binary: s.Binary}
		counts := map[string]int{}
		for j, m := range s.Inventory {
			if m.ID != j || m.File == "" || m.Descriptor == "" || m.Ordinal != counts[m.File] {
				return nil, errors.New("manifest inventory invalid")
			}
			counts[m.File]++
			suites[i].mutants = append(suites[i].mutants, mutant{source: source{m.File, m.Ordinal}, descriptor: m.Descriptor, id: m.ID})
		}
	}
	return suites, nil
}

// sharedIdentity is what every shard of one campaign must have in common:
// the source and its candidate inventory, the generator, compiler and
// execution tools, and the campaign's configuration. It is computed from a
// receipt's own recorded evidence, with three path anchors made relative (the
// checkout root, GOROOT and GOMODCACHE); nothing else is dropped or relaxed.
// Each shard's exact execution identity (its inherited environment and paths:
// the input digest) stays in its receipt and differs between runners.
type sharedIdentity struct {
	Package    string             `json:"package"`
	Shards     int                `json:"shards"`
	Config     sharedConfig       `json:"config"`
	Source     sourceIdentity     `json:"source"`
	Candidates candidateInventory `json:"candidates"`
	Suites     []sharedSuite      `json:"suites"`
	Generator  generatorIdentity  `json:"generator"`
}
type sharedConfig struct {
	JournalVersion int               `json:"journal_version"`
	Jobs           int               `json:"jobs"`
	MemMB          int               `json:"mem_mb"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Discovery      []string          `json:"discovery_arguments"`
	GoEnv          map[string]string `json:"go_env"`
	// Environment is every semantic variable, set or unset (env.go), and
	// PathTools the external programs tests look up, by content.
	Environment []envSetting `json:"semantic_environment"`
	PathTools   []pathTool   `json:"path_tools"`
}

// sourceIdentity digests the static input files (content, size, mode, link)
// and the Go discovery membership that selected them, by anchored path.
type sourceIdentity struct {
	Files     int    `json:"files"`
	Digest    string `json:"digest"`
	Discovery string `json:"discovery_digest"`
}
type sharedSuite struct {
	Package   string       `json:"package"`
	Directory string       `json:"directory"`
	Mutants   int          `json:"mutants"`
	Binary    digestedFile `json:"binary"`
}
type digestedFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// generatorIdentity: this helper's own executable, the qweave it built and
// ran, the Go toolchain's binaries and the shell and timeout every test runs
// under, each by content; and the overlay bytes qweave generated, by the
// anchored source each replaces.
type generatorIdentity struct {
	GoVersion string         `json:"go_version"`
	Producer  digestedFile   `json:"producer"`
	Qweave    digestedFile   `json:"qweave"`
	Toolchain []digestedFile `json:"toolchain"`
	Execution []digestedFile `json:"execution_tools"`
	Generated string         `json:"generated_digest"`
}

// anchors relativize the three locations that legitimately differ between
// runners of one checkout. Longer anchors are tried first.
type anchors [][2]string

func anchorsFor(root string, settings map[string]string) anchors {
	a := anchors{{root, "$ROOT"}}
	for _, k := range []string{"GOROOT", "GOMODCACHE"} {
		if v := settings[k]; v != "" {
			a = append(a, [2]string{v, "$" + k})
		}
	}
	slices.SortFunc(a, func(x, y [2]string) int { return len(y[0]) - len(x[0]) })
	return a
}

func (a anchors) path(p string) string {
	for _, anchor := range a {
		if p == anchor[0] {
			return anchor[1]
		}
		if rest, ok := strings.CutPrefix(p, anchor[0]+"/"); ok {
			return anchor[1] + "/" + rest
		}
	}
	return p
}

func (a anchors) file(name, sha string, size int64) digestedFile {
	return digestedFile{Name: a.path(name), SHA256: sha, Size: size}
}

func (a anchors) module(m *inputModule) *inputModule {
	if m == nil {
		return nil
	}
	c := *m
	c.GoMod, c.Dir = a.path(m.GoMod), a.path(m.Dir)
	c.Replace = a.module(m.Replace)
	return &c
}

// sharedIdentityOf computes the shared block from a receipt's recorded
// evidence. A receipt whose input collection was unavailable has no source
// identity, and cannot be aggregated with others.
func sharedIdentityOf(r completionReceipt) (sharedIdentity, error) {
	var id sharedIdentity
	if r.Inputs.Unavailable != "" || !r.InputsRechecked {
		return id, fmt.Errorf("source identity unavailable (%s): an aggregate cannot establish that its shards checked one source", r.Inputs.Unavailable)
	}
	start, err := decodeStart(r.Manifest.Start)
	if err != nil {
		return id, err
	}
	in := r.Inputs.Inputs
	a := anchorsFor(start.Root, in.Settings)
	id.Package, id.Shards = start.Package, start.Shard.Count
	id.Config = sharedConfig{JournalVersion: start.Version, Jobs: start.Jobs, MemMB: start.MemMB, TimeoutSeconds: start.TimeoutSeconds, Discovery: r.Inputs.DiscoveryArguments, GoEnv: map[string]string{}}
	for k, v := range in.Settings {
		id.Config.GoEnv[k] = a.path(v)
	}
	id.Config.Environment, id.Config.PathTools = r.Inputs.Environment.Semantic, r.Inputs.Environment.Tools
	for _, t := range id.Config.PathTools {
		// a tool found without a content digest establishes no identity:
		// agreeing on the same collection error is not agreeing on a tool
		if t.Found && (t.Unavailable != "" || !isSHA256(t.SHA256)) {
			reason := t.Unavailable
			if reason == "" {
				reason = fmt.Sprintf("digest %q", t.SHA256)
			}
			return id, fmt.Errorf("PATH tool %s was found but its content digest is unavailable (%s): an aggregate cannot establish that its shards ran the same tool", t.Name, reason)
		}
	}
	files := make([]inputFile, len(in.Files))
	for i, f := range in.Files {
		f.Path, f.Resolved = a.path(f.Path), a.path(f.Resolved)
		if strings.HasPrefix(f.Link, "/") {
			f.Link = a.path(f.Link)
		}
		files[i] = f
	}
	slices.SortFunc(files, func(x, y inputFile) int { return strings.Compare(x.Path, y.Path) })
	id.Source.Files = len(files)
	if id.Source.Digest, err = valueDigest(files); err != nil {
		return id, err
	}
	discoveries := make([]inputDiscovery, len(in.Discoveries))
	for i, d := range in.Discoveries {
		d.Directory = a.path(d.Directory)
		packages := make([]inputPackage, len(d.Packages))
		for j, p := range d.Packages {
			p.Dir = a.path(p.Dir)
			p.Module = a.module(p.Module)
			packages[j] = p
		}
		d.Packages = packages
		discoveries[i] = d
	}
	if id.Source.Discovery, err = valueDigest(discoveries); err != nil {
		return id, err
	}
	if id.Candidates, _, err = inventoryOf(r.Manifest); err != nil {
		return id, err
	}
	if len(r.Manifest.Binaries) != len(r.Manifest.Suites) {
		return id, errors.New("manifest suite/binary roster incomplete")
	}
	for i, s := range r.Manifest.Suites {
		b := r.Manifest.Binaries[i]
		id.Suites = append(id.Suites, sharedSuite{Package: s.Package, Directory: a.path(s.Directory), Mutants: len(s.Inventory), Binary: digestedFile{Name: s.Package, SHA256: b.SHA256, Size: b.Size}})
	}
	id.Generator.GoVersion = in.Settings["GOVERSION"]
	id.Generator.Producer = digestedFile{Name: "mutatecore", SHA256: r.Producer.SHA256, Size: r.Producer.Size}
	id.Generator.Qweave = digestedFile{Name: "qweave", SHA256: r.Qweave.SHA256, Size: r.Qweave.Size}
	for _, t := range in.Tools {
		id.Generator.Toolchain = append(id.Generator.Toolchain, a.file(t.Path, t.SHA256, t.Size))
	}
	for _, t := range r.ExecutionTools {
		id.Generator.Execution = append(id.Generator.Execution, a.file(t.Path, t.SHA256, t.Size))
	}
	if id.Generator.Generated, err = generatedDigest(a, r.Generated); err != nil {
		return id, err
	}
	return id, nil
}

// generatedDigest identifies the overlay bytes qweave generated. Its go
// invocations run concurrently and are captured in no particular order;
// those without an overlay carry no generated bytes (their argv stays in the
// execution identity). Each build's replaced sources, by anchored path, are
// compared as a sorted set.
func generatedDigest(a anchors, invocations []capturedInvocation) (string, error) {
	type generated struct {
		Source  string `json:"source"`
		Deleted bool   `json:"deleted"`
		SHA256  string `json:"sha256"`
		Size    int64  `json:"size"`
	}
	var overlays []string
	for _, inv := range invocations {
		if len(inv.Inputs) == 0 {
			continue
		}
		var g []generated
		for _, input := range inv.Inputs {
			g = append(g, generated{a.path(input.Source), input.Deleted, input.File.SHA256, input.File.Size})
		}
		slices.SortFunc(g, func(x, y generated) int { return strings.Compare(x.Source, y.Source) })
		d, err := valueDigest(g)
		if err != nil {
			return "", err
		}
		overlays = append(overlays, d)
	}
	slices.Sort(overlays)
	return valueDigest(overlays)
}

// differingBlock names the first block two shared identities disagree on.
func differingBlock(a, b sharedIdentity) string {
	for _, block := range []struct {
		name string
		x, y any
	}{
		{"package", a.Package, b.Package}, {"shard count", a.Shards, b.Shards},
		{"semantic environment " + differingSetting(a.Config.Environment, b.Config.Environment), a.Config.Environment, b.Config.Environment},
		{"tools on PATH (" + differingTool(a.Config.PathTools, b.Config.PathTools) + ")", a.Config.PathTools, b.Config.PathTools},
		{"configuration (journal version, JOBS, MEM_MB, per-test timeout, discovery, go env)", a.Config, b.Config},
		{"source files", a.Source, b.Source}, {"candidate inventory", a.Candidates, b.Candidates}, {"suite roster and binaries", a.Suites, b.Suites},
		{"generator, compiler and execution tools", a.Generator, b.Generator},
	} {
		dx, _ := valueDigest(block.x)
		dy, _ := valueDigest(block.y)
		if dx != dy {
			return block.name
		}
	}
	return ""
}

// differingSetting names the first semantic variable two shards disagree on.
func differingSetting(a, b []envSetting) string {
	show := func(s envSetting, ok bool) string {
		switch {
		case !ok:
			return "not classified"
		case !s.Set:
			return "unset"
		}
		return fmt.Sprintf("%q", s.Value)
	}
	byName := func(l []envSetting) map[string]envSetting {
		m := map[string]envSetting{}
		for _, s := range l {
			m[s.Name] = s
		}
		return m
	}
	ma, mb := byName(a), byName(b)
	var names []string
	for k := range ma {
		names = append(names, k)
	}
	for k := range mb {
		if _, ok := ma[k]; !ok {
			names = append(names, k)
		}
	}
	slices.Sort(names)
	for _, k := range names {
		x, okx := ma[k]
		y, oky := mb[k]
		if okx != oky || x != y {
			return fmt.Sprintf("(%s: %s, then %s)", k, show(x, okx), show(y, oky))
		}
	}
	return ""
}

func differingTool(a, b []pathTool) string {
	for i := range max(len(a), len(b)) {
		if i >= len(a) || i >= len(b) || a[i] != b[i] {
			if i < len(a) {
				return a[i].Name
			}
			return b[i].Name
		}
	}
	return ""
}

// isSHA256 reports whether s is a lowercase hex SHA-256 digest.
func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}
