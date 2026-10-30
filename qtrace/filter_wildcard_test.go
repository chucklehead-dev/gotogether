package qtrace_test

import (
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

func TestFilterKeepsOnlyRequestedDynamicMap(t *testing.T) {
	steps := []qtrace.Step{{Action: "a", Args: map[string]any{"id": "x", "extra": "a"}, Obs: map[string]any{"native": "n", "extra": "o"}}, {Action: "b", Args: map[string]any{"id": "y", "extra": "b"}, Obs: map[string]any{"native": "m", "extra": "p"}}}
	got := qtrace.Filter{Keep: map[string][]string{"a": {"args.*", "obs.native"}, "b": {"id", "obs.*"}}}.Apply(steps)
	if !reflect.DeepEqual(got[0].Args, steps[0].Args) || len(got[0].Obs) != 1 || got[0].Obs["native"] != "n" {
		t.Fatalf("argument wildcard: %+v", got[0])
	}
	if !reflect.DeepEqual(got[1].Obs, steps[1].Obs) || len(got[1].Args) != 1 || got[1].Args["id"] != "y" {
		t.Fatalf("observation wildcard: %+v", got[1])
	}
}
