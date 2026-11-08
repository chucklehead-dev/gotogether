// Package reviewjournal checks the structure of mutatecore's current journal.
// It establishes neither source provenance nor successful durable writes.
package reviewjournal

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"
)

const module = "github.com/chucklehead-dev/gotogether"

// Start is the exact execution configuration expected independently of a log.
type Start struct {
	Version        int    `json:"version"`
	Root           string `json:"root"`
	Package        string `json:"package"`
	Jobs           int    `json:"jobs"`
	MemMB          int    `json:"mem_mb"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxSurvivors   int    `json:"max_survivors"`
}

// Identity identifies an uninstrumented source registration.
type Identity struct {
	File       string `json:"file"`
	Ordinal    int    `json:"ordinal"`
	Descriptor string `json:"descriptor"`
}

// Registration maps a source identity to one suite's local activation ID.
type Registration struct {
	File       string `json:"file"`
	Ordinal    int    `json:"ordinal"`
	Descriptor string `json:"descriptor"`
	ID         int    `json:"id"`
}

// Suite is a complete externally expected suite and its linked inventory.
type Suite struct {
	Index     int            `json:"index"`
	Package   string         `json:"package"`
	Directory string         `json:"directory"`
	Binary    string         `json:"binary"`
	Inventory []Registration `json:"inventory"`
}

// Manifest must come from an independently accounted campaign universe. Copying
// these fields out of the same journal checks consistency, not completeness.
type Manifest struct {
	Start  Start
	Suites []Suite
}

// Outcome is structurally resolved evidence, not authorization for admission.
type Outcome struct {
	Identity Identity
	Survived bool
	// KillingSuite is the first suite whose test process failed. The current
	// producer also classifies resource-limit failures as failed, so this is
	// not proof that a behavioral assertion detected the mutation.
	KillingSuite string
}

// Result cannot be constructed by callers as a successful validation.
type Result struct {
	validated  bool
	outcomes   []Outcome
	gatePassed bool
}

func (r Result) StructurallyComplete() bool { return r.validated }
func (r Result) Outcomes() []Outcome        { return slices.Clone(r.outcomes) }
func (r Result) CountGatePassed() bool      { return r.validated && r.gatePassed }

type target struct {
	Suite int `json:"suite"`
	ID    int `json:"id"`
}
type source struct {
	File       string   `json:"file"`
	Ordinal    int      `json:"ordinal"`
	Descriptor string   `json:"descriptor"`
	Targets    []target `json:"targets"`
}

func (s source) identity() Identity { return Identity{s.File, s.Ordinal, s.Descriptor} }

type baseline struct {
	Package string `json:"package"`
	Passed  bool   `json:"passed"`
	Error   string `json:"error,omitempty"`
}
type admitted struct {
	Sources []source `json:"sources"`
	Suites  int      `json:"suites"`
}
type attempt struct {
	Suite   int    `json:"suite"`
	ID      int    `json:"id"`
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
}
type outcome struct {
	Source   source    `json:"source"`
	Outcome  string    `json:"outcome"`
	Attempts []attempt `json:"attempts"`
	Error    string    `json:"error,omitempty"`
}
type complete struct {
	Total      int  `json:"total"`
	Survivors  int  `json:"survivors"`
	GatePassed bool `json:"gate_passed"`
}
type envelope struct {
	Event string          `json:"event"`
	At    time.Time       `json:"at"`
	Data  json.RawMessage `json:"data"`
}

// Validate accepts only one fully framed current-format campaign with all
// expected baselines and outcomes. Outcome order is deliberately unconstrained:
// parallel workers append completed source outcomes in completion order.
func Validate(r io.Reader, expected Manifest) (Result, error) {
	sources, err := inventory(expected)
	if err != nil {
		return Result{}, err
	}
	scan := bufio.NewReader(r)
	stage, index, baselines := 0, 0, 0
	seen := map[Identity]bool{}
	byID := map[Identity]source{}
	for _, s := range sources {
		byID[s.identity()] = s
	}
	result := Result{}
	line := 0
	for {
		b, err := readLine(scan)
		if err == io.EOF && len(b) == 0 {
			break
		}
		line++
		if err != nil {
			return Result{}, fmt.Errorf("journal line %d: missing newline or read failure: %w", line, err)
		}
		if len(b) > 16<<20 {
			return Result{}, fmt.Errorf("journal line %d exceeds 16 MiB", line)
		}
		var e envelope
		if err := decode(b, &e); err != nil {
			return Result{}, fmt.Errorf("journal line %d: %w", line, err)
		}
		if e.At.IsZero() {
			return Result{}, fmt.Errorf("journal line %d: missing timestamp", line)
		}
		fail := func() (Result, error) {
			return Result{}, fmt.Errorf("journal line %d: invalid %q event in phase %d", line, e.Event, stage)
		}
		switch e.Event {
		case "start":
			if stage != 0 {
				return fail()
			}
			var x Start
			if err := decode(e.Data, &x); err != nil {
				return Result{}, err
			}
			if x != expected.Start {
				return Result{}, fmt.Errorf("start differs from expected manifest")
			}
			stage = 1
		case "suite":
			if stage != 1 || index >= len(expected.Suites) {
				return fail()
			}
			var x Suite
			if err := decode(e.Data, &x); err != nil {
				return Result{}, err
			}
			if !reflect.DeepEqual(x, expected.Suites[index]) {
				return Result{}, fmt.Errorf("suite %d differs from expected manifest", index)
			}
			index++
		case "baseline":
			if (stage != 1 && stage != 2) || index != len(expected.Suites) || baselines >= len(expected.Suites) {
				return fail()
			}
			var x baseline
			if err := decode(e.Data, &x); err != nil {
				return Result{}, err
			}
			if x.Package != expected.Suites[baselines].Package || !x.Passed || x.Error != "" {
				return Result{}, fmt.Errorf("baseline %d did not pass exactly", baselines)
			}
			baselines++
			stage = 2
		case "admitted":
			if stage != 2 || baselines != len(expected.Suites) {
				return fail()
			}
			var x admitted
			if err := decode(e.Data, &x); err != nil {
				return Result{}, err
			}
			if x.Suites != len(expected.Suites) || !reflect.DeepEqual(x.Sources, sources) {
				return Result{}, fmt.Errorf("admitted inventory differs from expected union")
			}
			stage = 3
		case "outcome":
			if stage != 3 {
				return fail()
			}
			var x outcome
			if err := decode(e.Data, &x); err != nil {
				return Result{}, err
			}
			id := x.Source.identity()
			want, ok := byID[id]
			if !ok || seen[id] || !reflect.DeepEqual(want, x.Source) {
				return Result{}, fmt.Errorf("unknown, duplicate or changed source outcome")
			}
			o, err := resolve(x, expected.Suites)
			if err != nil {
				return Result{}, err
			}
			seen[id] = true
			result.outcomes = append(result.outcomes, o)
		case "complete":
			if stage != 3 || len(seen) != len(sources) {
				return fail()
			}
			var x complete
			if err := decode(e.Data, &x); err != nil {
				return Result{}, err
			}
			survivors := 0
			for _, o := range result.outcomes {
				if o.Survived {
					survivors++
				}
			}
			passed := expected.Start.MaxSurvivors < 0 || survivors <= expected.Start.MaxSurvivors
			if x.Total != len(sources) || x.Survivors != survivors || x.GatePassed != passed {
				return Result{}, fmt.Errorf("complete counts/gate disagree with resolved outcomes")
			}
			result.gatePassed = passed
			stage = 4
		default:
			return fail()
		}
	}
	if stage != 4 {
		return Result{}, fmt.Errorf("journal missing structurally complete terminal event")
	}
	slices.SortFunc(result.outcomes, func(a, b Outcome) int {
		if n := strings.Compare(a.Identity.File, b.Identity.File); n != 0 {
			return n
		}
		return cmp.Compare(a.Identity.Ordinal, b.Identity.Ordinal)
	})
	result.validated = true
	return result, nil
}

func resolve(x outcome, suites []Suite) (Outcome, error) {
	o := Outcome{Identity: x.Source.identity()}
	if x.Error != "" || len(x.Attempts) == 0 || len(x.Attempts) > len(x.Source.Targets) {
		return o, fmt.Errorf("invalid resolved source attempts")
	}
	for i, a := range x.Attempts {
		if a.Error != "" || a.Suite != x.Source.Targets[i].Suite || a.ID != x.Source.Targets[i].ID {
			return o, fmt.Errorf("attempt is not an exact target prefix")
		}
		if a.Outcome == "failed" {
			if i != len(x.Attempts)-1 || x.Outcome != "killed" {
				return o, fmt.Errorf("failed attempt must terminate killed source")
			}
			o.KillingSuite = suites[a.Suite].Package
			return o, nil
		}
		if a.Outcome != "passed" {
			return o, fmt.Errorf("invalid attempt verdict")
		}
	}
	if x.Outcome != "survived" || len(x.Attempts) != len(x.Source.Targets) {
		return o, fmt.Errorf("survivor requires every linked suite to pass")
	}
	o.Survived = true
	return o, nil
}

func validPath(s string) bool {
	return s != "" && s != "." && path.Clean(s) == s && !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "../") && !strings.Contains(s, "\\")
}
func inventory(m Manifest) ([]source, error) {
	st := m.Start
	if st.Version != 1 || strings.TrimSpace(st.Root) == "" || !validPath(st.Package) || st.Jobs <= 0 || st.MemMB <= 0 || st.TimeoutSeconds != 300 || st.MaxSurvivors < -1 || len(m.Suites) == 0 {
		return nil, fmt.Errorf("invalid expected campaign manifest")
	}
	byID := map[Identity]*source{}
	descriptors := map[string]string{}
	counts := map[string]int{}
	pkgs := map[string]bool{}
	for i, s := range m.Suites {
		prefix := module + "/" + st.Package
		if s.Index != i || pkgs[s.Package] || !(s.Package == prefix || strings.HasPrefix(s.Package, prefix+"/")) || strings.TrimSpace(s.Directory) == "" || strings.TrimSpace(s.Binary) == "" {
			return nil, fmt.Errorf("invalid expected suite %d", i)
		}
		pkgs[s.Package] = true
		local := map[string]int{}
		for j, r := range s.Inventory {
			if r.ID != j || !validPath(r.File) || !(strings.HasPrefix(r.File, st.Package+"/")) || r.Ordinal != local[r.File] || strings.TrimSpace(r.Descriptor) == "" {
				return nil, fmt.Errorf("invalid expected registration %d/%d", i, j)
			}
			local[r.File]++
			key := fmt.Sprintf("%s:%d", r.File, r.Ordinal)
			if old, ok := descriptors[key]; ok && old != r.Descriptor {
				return nil, fmt.Errorf("inconsistent source descriptor")
			}
			descriptors[key] = r.Descriptor
			id := Identity{r.File, r.Ordinal, r.Descriptor}
			src := byID[id]
			if src == nil {
				src = &source{File: r.File, Ordinal: r.Ordinal, Descriptor: r.Descriptor}
				byID[id] = src
			}
			src.Targets = append(src.Targets, target{i, j})
		}
		for file, n := range local {
			if old, ok := counts[file]; ok && old != n {
				return nil, fmt.Errorf("inconsistent per-file inventory length")
			}
			counts[file] = n
		}
	}
	sources := make([]source, 0, len(byID))
	for _, s := range byID {
		owner := module + "/" + path.Dir(s.File)
		for i, t := range s.Targets {
			if m.Suites[t.Suite].Package == owner {
				copy(s.Targets[1:i+1], s.Targets[:i])
				s.Targets[0] = t
				break
			}
		}
		sources = append(sources, *s)
	}
	slices.SortFunc(sources, func(a, b source) int {
		if n := strings.Compare(a.File, b.File); n != 0 {
			return n
		}
		return cmp.Compare(a.Ordinal, b.Ordinal)
	})
	if len(sources) == 0 {
		return nil, fmt.Errorf("expected inventory has no source mutants")
	}
	return sources, nil
}

// decode rejects case aliases, duplicate keys, missing/null fields and trailing
// values, including zero-valued integer/Boolean fields whose absence is unsafe.
func decode(b []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := unique(d)
	if err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	if err := shape(v, reflect.TypeOf(dst).Elem()); err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}
func unique(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("invalid key")
			}
			if _, ok := m[key]; ok {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			v, err := unique(d)
			if err != nil {
				return nil, err
			}
			m[key] = v
		}
		_, err := d.Token()
		return m, err
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, err := unique(d)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err := d.Token()
		return a, err
	default:
		return t, nil
	}
}

var rawType = reflect.TypeFor[json.RawMessage]()
var timeType = reflect.TypeFor[time.Time]()

func shape(v any, t reflect.Type) error {
	if t == rawType {
		return nil
	}
	if v == nil {
		if t.Kind() == reflect.Slice {
			return nil
		}
		return fmt.Errorf("null %s", t)
	}
	if t == timeType {
		if _, ok := v.(string); !ok {
			return fmt.Errorf("invalid timestamp")
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object %s", t)
		}
		allowed := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			parts := strings.Split(f.Tag.Get("json"), ",")
			key := parts[0]
			allowed[key] = true
			value, present := m[key]
			if !present {
				if slices.Contains(parts, "omitempty") {
					continue
				}
				return fmt.Errorf("missing %s", key)
			}
			if err := shape(value, f.Type); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
		for k := range m {
			if !allowed[k] {
				return fmt.Errorf("unknown field %q", k)
			}
		}
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return fmt.Errorf("expected array")
		}
		for _, value := range a {
			if err := shape(value, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Int:
		n, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("expected integer")
		}
		if _, err := n.Int64(); err != nil {
			return err
		}
	case reflect.String:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("expected string")
		}
	case reflect.Bool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("expected Boolean")
		}
	default:
		return fmt.Errorf("unsupported schema %s", t)
	}
	return nil
}

// readLine enforces the limit while reading rather than after unbounded allocation.
func readLine(r *bufio.Reader) ([]byte, error) {
	var result []byte
	for {
		b, err := r.ReadSlice('\n')
		if len(result)+len(b) > 16<<20 {
			return nil, fmt.Errorf("record exceeds 16 MiB")
		}
		result = append(result, b...)
		if err != bufio.ErrBufferFull {
			return result, err
		}
	}
}
