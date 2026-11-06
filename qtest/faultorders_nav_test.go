package qtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
)

func TestFaultOrdersNavigationRetainsRequestedScope(t *testing.T) {
	RequireQuint(t)
	path := filepath.Join(t.TempDir(), "node.json")
	ref := t.Name() + "/test.ordered.a_then_test.ordered.b#1:seed=2"
	t.Setenv("GOTOGETHER_RUN", ref)
	t.Setenv("GOTOGETHER_NAV", "start")
	t.Setenv("GOTOGETHER_NAV_OUT", path)
	result := faultOrdersNativeSystem().FaultOrders(t, FaultOrderConfig{Seeds: 3, Faults: []qenv.Scripted{
		{Kind: "test.ordered.a", Name: "failed"},
		{Kind: "test.ordered.b", Name: "failed"},
	}})
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var node navproto.Node
	if err := json.Unmarshal(body, &node); err != nil {
		t.Fatal(err)
	}
	if node.Run != ref {
		t.Fatalf("run %q, want %q", node.Run, ref)
	}
	notes := strings.Join(node.Notes, "\n")
	if !strings.Contains(notes, "requested fault order: test.ordered.a=failed") || !strings.Contains(notes, "then test.ordered.b=failed") || !strings.Contains(notes, "seed budget 3") || !strings.Contains(notes, "node may change the faults") {
		t.Fatalf("requested navigation scope missing: %s", notes)
	}
	// Navigation is an inspection, not a fresh sweep that witnesses its goals.
	if e := result.Evidence(); e.Role != RoleReplay || e.Sample != nil {
		t.Fatalf("navigation became fresh evidence: %+v", e)
	}
}
