package choice

import (
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
)

// Nemesis choice families are known names, while ordinary fault kinds are
// exact scripted targets. A bare declaration must work without option names
// incidentally making Covers return true through a longer name.
func TestDeclareInDistinguishesNemesisNamesFromKinds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		exactKind bool
	}{
		{"test.contract.declared.normal", true},
		{"nemesis.test.contract.declared.family", false},
	} {
		names := &hooks.FaultNames{}
		DeclareIn(names, true, tc.name)
		if !names.Covers(tc.name) {
			t.Fatalf("bare declaration %q was not recorded", tc.name)
		}
		if got := names.HasKind(tc.name); got != tc.exactKind {
			t.Fatalf("declaration %q exact kind=%v, want %v", tc.name, got, tc.exactKind)
		}
		if KnownStatic(tc.name) {
			t.Fatalf("run-local declaration %q became static", tc.name)
		}
		if (&hooks.FaultNames{}).Covers(tc.name) {
			t.Fatalf("declaration %q leaked into another run's names", tc.name)
		}
	}
}

// Script admission needs exact static kinds, rather than an option label,
// a dot prefix, a run-local kind, or a nemesis family.
func TestKnownStaticKindContract(t *testing.T) {
	base := "test.contract." + t.Name()
	DeclareIn(nil, false, base+".ordinary", "lost")
	DeclareIn(&hooks.FaultNames{}, true, base+".run")
	DeclareSite(base+".sited", "pkg.ContractSite")
	nemesis := "nemesis." + base
	DeclareIn(nil, false, nemesis)
	for _, tc := range []struct {
		name string
		want bool
	}{
		{base + ".ordinary", true},
		{base + ".sited", true},
		{base, false},
		{base + ".ordinary.lost", false},
		{base + ".run", false},
		{base + ".unknown", false},
		{nemesis, false},
	} {
		if got := KnownStaticKind(tc.name); got != tc.want {
			t.Errorf("KnownStaticKind(%q)=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// Process-wide site declarations feed coverage even when no simulation chose
// the fault. The returned map and its slices are owned by the caller.
func TestStaticSitesContract(t *testing.T) {
	kind := "test.contract." + t.Name()
	withoutSite := kind + ".noSite"
	DeclareIn(nil, false, withoutSite)
	DeclareSite(kind, "pkg.ContractPoint")
	sites := Sites()
	if !slices.Equal(sites[kind], []string{"pkg.ContractPoint"}) {
		t.Fatalf("declared static site missing: %v", sites[kind])
	}
	if _, ok := sites[withoutSite]; ok {
		t.Fatal("kind with no sites appeared in site coverage")
	}
	sites[kind][0] = "changed by caller"
	delete(sites, kind)
	if got := Sites()[kind]; !slices.Equal(got, []string{"pkg.ContractPoint"}) {
		t.Fatalf("caller modified stored static sites: %v", got)
	}
}

// An unsupported future tape needs a newer reader. Asking the user to encode
// it again with this older reader is the wrong recovery action.
func TestFutureBytesVersionExplainsUpgrade(t *testing.T) {
	data := []byte{0xff, 'q', 'c', byte(BytesVersion + 1), 255}
	b := FromBytes(data, Rates{Default: 1})
	if got := b.Choose("sched.contract", 2); got != 0 {
		t.Fatalf("future-version tape made choice %d", got)
	}
	err := b.Err()
	if err == nil || !strings.Contains(err.Error(), "later version") || !strings.Contains(err.Error(), "upgrade gotogether") {
		t.Fatalf("future tape did not explain reader upgrade: %v", err)
	}
}
