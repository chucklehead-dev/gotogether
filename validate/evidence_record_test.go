package validate

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
)

func TestNavigatorRecordInspection(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/diagnostic/binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	steps := diagnosticSteps(t, true)
	n, err := c.Nav(NavStart{Steps: steps, Actor: "tenant-a", At: 0})
	if err != nil {
		t.Fatal(err)
	}
	before, err := n.Node()
	if err != nil {
		t.Fatal(err)
	}
	d, err := n.Record(diagnostic.RecordRef{Actor: "tenant-a", Recorder: "tenant-a", Seq: 2})
	if err != nil || d.Action != "request" {
		t.Fatalf("inspect captured record beyond cursor: %+v %v", d, err)
	}
	after, err := n.Node()
	if err != nil {
		t.Fatal(err)
	}
	if before.ID != after.ID || before.Start != after.Start || len(before.Diagnostics) != len(after.Diagnostics) {
		t.Fatal("record inspection changed cursor")
	}
}
