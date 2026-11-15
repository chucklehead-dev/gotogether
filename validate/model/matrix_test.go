package model

import (
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// A matrix holds one variant's graph at a time: each variant's report keeps
// its verdicts, and releases its graph before the next variant searches. Held, every search
// ran beside the graphs before it (golem's leadership, #144: from the
// twelfth of 30 variants each was cut by memory at depth 2 to 4).
func TestMatrixReleasesEachVariantsGraph(t *testing.T) {
	skip.Quint(t)
	m, err := Matrix(t.Context(), "../testdata/design.yaml", SimulateOptions{Graph: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Variants) != 2 {
		t.Fatalf("%d variants", len(m.Variants))
	}
	for _, v := range m.Variants {
		g := v.Report.Graph
		if g == nil {
			t.Fatalf("variant %s: no graph result", v.Name)
		}
		if g.graph != nil || g.e != nil || g.keys != nil || g.checker != nil || g.sourceChecker != nil {
			t.Errorf("variant %s still holds its graph after the matrix", v.Name)
		}
		if d := v.Report.Doc(); g.States == 0 || d.Graph == nil || d.Graph.States != g.States {
			t.Errorf("variant %s lost its graph's extent: %d states, document %+v", v.Name, g.States, d.Graph)
		}
	}
	// released, the report's document is what it was with its graph: the
	// same as a run of the variant alone
	vb, err := mustVariant(t, "../testdata/design.yaml", "lease")
	if err != nil {
		t.Fatal(err)
	}
	alone, err := simulate(t.Context(), "../testdata/design.yaml", vb, nil, SimulateOptions{Graph: true, UntilBroken: true})
	if err != nil {
		t.Fatal(err)
	}
	got, want := m.Variants[0].Report.Doc(), alone.Doc()
	got.Graph.PeakResident, want.Graph.PeakResident = 0, 0 // the process's, not the variant's
	got.Graph.ProcessSeen, want.Graph.ProcessSeen = 0, 0
	if !reflect.DeepEqual(got.Checks, want.Checks) || !reflect.DeepEqual(got.Graph, want.Graph) {
		t.Errorf("the matrix's document of lease differs from lease's alone:\n got %+v\nwant %+v", got.Graph, want.Graph)
	}
}

// mustVariant is the binding at path as its variant name has it.
func mustVariant(t *testing.T, path, name string) (*binding.Binding, error) {
	t.Helper()
	b, err := binding.Load(path)
	if err != nil {
		return nil, err
	}
	return b.Variant(name)
}
