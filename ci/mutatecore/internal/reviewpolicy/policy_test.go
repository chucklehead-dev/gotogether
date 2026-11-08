package reviewpolicy

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func fixture(t *testing.T) (Policy, map[string][]Identity) {
	t.Helper()
	c, err := Snapshot(fstest.MapFS{"lib/x.go": {Data: []byte("source")}, "go.mod": {Data: []byte("deps")}}, "go1.27.3 sha256:toolchain", "GOOS=linux GOARCH=amd64 tags=none suites=lib/TestX")
	if err != nil {
		t.Fatal(err)
	}
	x := Identity{"lib/x.go", 0, "return false"}
	y := Identity{"lib/x.go", 1, "return true"}
	p := Policy{"reviewed-survivors/prerelease-1", c, map[string][]Review{"lib": {{x, c.Files[x.File], "equivalent", "unreachable under invariant", "review/test evidence"}}}}
	return p, map[string][]Identity{"lib": {x, y}}
}

func TestExactSurvivorReconciliation(t *testing.T) {
	p, inv := fixture(t)
	a, err := Admit(p, p.Context, inv)
	if err != nil {
		t.Fatal(err)
	}
	x, y := inv["lib"][0], inv["lib"][1]
	cases := []struct {
		name               string
		outcomes           []Outcome
		complete           bool
		wantErr            bool
		unexpected, killed int
	}{
		{"exact", []Outcome{{x, true, ""}, {y, false, "suite"}}, true, false, 0, 0},
		{"same count replacement", []Outcome{{x, false, "first witness"}, {y, true, ""}}, true, false, 1, 1},
		{"review now killed", []Outcome{{x, false, "first witness"}, {y, false, "suite"}}, true, false, 0, 1},
		{"interrupted", nil, false, true, 0, 0},
		{"missing", []Outcome{{x, true, ""}}, true, true, 0, 0},
		{"duplicate", []Outcome{{x, true, ""}, {x, true, ""}}, true, true, 0, 0},
		{"unknown", []Outcome{{Identity{"lib/x.go", 99, "other"}, true, ""}}, true, true, 0, 0},
		{"missing witness", []Outcome{{x, true, ""}, {y, false, ""}}, true, true, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := a.Check(tc.outcomes, tc.complete)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error %v", err)
			}
			if err != nil && d.Passed() {
				t.Fatal("ignored error produced passing verdict")
			}
			if err == nil && (len(d.Unexpected()) != tc.unexpected || len(d.NewlyKilled()) != tc.killed || d.Passed() != (tc.unexpected+tc.killed == 0)) {
				t.Fatalf("difference %+v", d)
			}
		})
	}
}

func TestAdmissionDrift(t *testing.T) {
	for _, change := range []string{"new caller", "deleted dependency", "toolchain", "settings", "generator"} {
		t.Run(change, func(t *testing.T) {
			p, inv := fixture(t)
			c := p.Context
			c.Files = map[string]string{}
			for k, v := range p.Context.Files {
				c.Files[k] = v
			}
			switch change {
			case "new caller":
				c.Files["new/caller.go"] = c.Files["go.mod"]
			case "deleted dependency":
				delete(c.Files, "go.mod")
			case "toolchain":
				c.Toolchain = "different"
			case "settings":
				c.Settings = "other tags"
			case "generator":
				c.Files["qweave/newoperator.go"] = c.Files["go.mod"]
			}
			if _, err := Admit(p, c, inv); err == nil || !strings.Contains(err.Error(), "context changed") {
				t.Fatalf("%v", err)
			}
		})
	}
	for _, change := range []string{"descriptor", "disappeared", "duplicate", "package"} {
		t.Run(change, func(t *testing.T) {
			p, inv := fixture(t)
			switch change {
			case "descriptor":
				inv["lib"][0].Descriptor = "drift"
			case "disappeared":
				inv["lib"] = inv["lib"][1:]
			case "duplicate":
				inv["lib"] = append(inv["lib"], inv["lib"][0])
			case "package":
				inv["other"] = nil
			}
			if _, err := Admit(p, p.Context, inv); err == nil {
				t.Fatal("accepted drift")
			}
		})
	}
}

func TestStrictLedger(t *testing.T) {
	p, _ := fixture(t)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(strings.NewReader(string(b))); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{string(b) + " {}", strings.Replace(string(b), `"format":`, `"unknown":1,"format":`, 1), strings.Replace(string(b), `"format":`, `"format":"duplicate","format":`, 1)} {
		if _, err := Load(strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	for _, change := range []string{"duplicate identity", "rationale", "kind", "source hash", "nil reviews"} {
		t.Run(change, func(t *testing.T) {
			p, _ := fixture(t)
			r := p.Packages["lib"][0]
			switch change {
			case "duplicate identity":
				p.Packages["lib"] = append(p.Packages["lib"], r)
			case "rationale":
				r.Rationale = " "
				p.Packages["lib"][0] = r
			case "kind":
				r.Kind = "observed"
				p.Packages["lib"][0] = r
			case "source hash":
				r.SourceSHA256 = strings.Repeat("0", 64)
				p.Packages["lib"][0] = r
			case "nil reviews":
				p.Packages["lib"] = nil
			}
			if err := p.Validate(); err == nil {
				t.Fatal("accepted malformed policy")
			}
		})
	}
}

func TestSnapshotUniverse(t *testing.T) {
	tree := fstest.MapFS{"lib/x.go": {Data: []byte("same")}, ".git/config": {Data: []byte("ignored")}, LedgerPath: {Data: []byte("ledger")}}
	a, err := Snapshot(tree, "toolchain", "settings")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Files) != 1 {
		t.Fatal(a.Files)
	}
	tree["new/caller.go"] = &fstest.MapFile{Data: []byte("caller")}
	b, err := Snapshot(tree, "toolchain", "settings")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 2 {
		t.Fatal(b.Files)
	}
	tree["symlink"] = &fstest.MapFile{Mode: 1 << 27, Data: []byte("external")}
	if _, err := Snapshot(tree, "toolchain", "settings"); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestZeroValuesCannotPass(t *testing.T) {
	if (Difference{}).Passed() {
		t.Fatal("zero result passed")
	}
	for _, a := range []*Admission{nil, {}} {
		d, err := a.Check(nil, true)
		if err == nil || d.Passed() {
			t.Fatalf("unadmitted campaign passed: %v", err)
		}
	}
}

func TestEmptyRequestedInventoryRejected(t *testing.T) {
	for _, empty := range [][]Identity{nil, {}} {
		p, inv := fixture(t)
		p.Packages["lib"] = []Review{}
		inv["lib"] = empty
		if a, err := Admit(p, p.Context, inv); err == nil || a != nil {
			t.Fatalf("empty inventory admitted: %v", err)
		}
	}
	// Empty policy and inventory sets cannot produce an admission either.
	p, _ := fixture(t)
	p.Packages = map[string][]Review{}
	if a, err := Admit(p, p.Context, map[string][]Identity{}); err == nil || a != nil {
		t.Fatalf("empty campaign admitted: %v", err)
	}
}

func TestDiagnosticsCannotChangeVerdict(t *testing.T) {
	p, inv := fixture(t)
	a, err := Admit(p, p.Context, inv)
	if err != nil {
		t.Fatal(err)
	}
	x, y := inv["lib"][0], inv["lib"][1]
	d, err := a.Check([]Outcome{{x, false, "first witness"}, {y, true, ""}}, true)
	if err != nil {
		t.Fatal(err)
	}
	unexpected, killed := d.Unexpected(), d.NewlyKilled()
	unexpected[0] = Identity{}
	killed[0] = Outcome{}
	if d.Passed() || len(d.Unexpected()) != 1 || len(d.NewlyKilled()) != 1 || d.Unexpected()[0] != y || d.NewlyKilled()[0].KillingSuite != "first witness" {
		t.Fatalf("diagnostic mutation altered result: %+v", d)
	}
}

func TestCanonicalJSONFieldNames(t *testing.T) {
	p, _ := fixture(t)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"format", "context", "packages", "files", "toolchain", "settings", "file", "ordinal", "descriptor", "source_sha256", "kind", "rationale", "evidence"} {
		t.Run(field, func(t *testing.T) {
			canonical := `"` + field + `":`
			alias := `"` + strings.ToUpper(field[:1]) + field[1:] + `":`
			wrongCase := strings.Replace(string(b), canonical, alias, 1)
			if _, err := Load(strings.NewReader(wrongCase)); err == nil {
				t.Fatal("accepted case alias")
			}
			// An alias may not overwrite a canonical assignment, in either order.
			var duplicate string
			if field == "format" {
				duplicate = strings.Replace(string(b), canonical, `"Format":"bad",`+canonical, 1)
			} else {
				duplicate = strings.Replace(string(b), canonical, alias+`null,`+canonical, 1)
			}
			if _, err := Load(strings.NewReader(duplicate)); err == nil {
				t.Fatal("accepted semantic duplicate")
			}
		})
	}
	if _, err := Load(strings.NewReader(`{"format":"bad","Format":"reviewed-survivors/prerelease-1"` + string(b)[strings.Index(string(b), `,"context"`):])); err == nil {
		t.Fatal("accepted later alias overwrite")
	}
}

func TestReviewsMustResolveWithinOwnPrefix(t *testing.T) {
	// Parent prefix review points to an identity present only in the child prefix.
	// Global union lookup used to accept this depending on map traversal order.
	for range 200 {
		p, inv := fixture(t)
		x := inv["lib"][0]
		x.File = "lib/sub/x.go"
		p.Context.Files[x.File] = p.Context.Files["lib/x.go"]
		r := p.Packages["lib"][0]
		r.Identity = x
		p.Packages["lib"] = []Review{r}
		p.Packages["lib/sub"] = []Review{}
		inv["lib/sub"] = []Identity{x}
		a, err := Admit(p, p.Context, inv)
		if err == nil || a != nil {
			t.Fatal("review resolved from another prefix")
		}
	}
}
