package binding

import (
	"encoding/json"
	"slices"
	"testing"
)

// Pre-flattening IDs identify declarations, not instances. A private stateful
// instance must not borrow capture coverage from an exported copy of its source.
func TestCheckpointScopeRefusesUnknownAndInstanceState(t *testing.T) {
	y := tcDecl{ID: json.Number("1"), Kind: "var", Name: "y"}
	for _, kind := range []string{"instance", "import"} {
		decls := map[string][]tcDecl{
			"outer": {{Kind: kind, ProtoName: "inner", DefName: "*", QualifiedName: "private"}},
			"inner": {y},
		}
		scope := checkpointScope("outer", decls, []tcDecl{y})
		if !slices.Contains(scope, "stateful import inner") {
			t.Errorf("%s shares source IDs but creates private state: %v", kind, scope)
		}
	}
	if scope := checkpointScope("outer", map[string][]tcDecl{"outer": {{Kind: "import", ProtoName: "unknown", DefName: "*"}}}, nil); !slices.Contains(scope, "unresolved module unknown") {
		t.Errorf("unknown imported scope accepted: %v", scope)
	}
	// Two same-named declarations are not the same state cell. The bound
	// module's own declaration wins lookup, leaving the imported one uncaptured.
	own := tcDecl{ID: json.Number("2"), Kind: "var", Name: "y"}
	decls := map[string][]tcDecl{"outer": {{Kind: "import", ProtoName: "inner", DefName: "*"}, own}, "inner": {y}}
	if scope := checkpointScope("outer", decls, []tcDecl{y, own}); !slices.Contains(scope, "inner::y") {
		t.Errorf("same-named private state accepted: %v", scope)
	}
}
