package validate

import (
	"os"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func TestCheckpointITFRejectsMalformedRole(t *testing.T) {
	skip.Quint(t)
	b, err := binding.Load("testdata/snapshot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []itf.Value{itf.BoolOf(true), itf.StrOf("true")} {
		tr := &itf.Trace{States: []itf.State{{Action: "init", Extra: map[string]itf.Value{"gotogether::checkpoint": marker}}}}
		_, err := ValidateITF(t.Context(), tr, ITFOptions{Binding: b, Options: Options{Dir: t.TempDir()}})
		if err == nil || !strings.Contains(err.Error(), "state 0:") {
			t.Fatalf("initial-state checkpoint marker %v accepted: %v", marker, err)
		}
	}
}

// The role must survive both the model-level observed ITF and the annotated
// evidence written by a successful native/Quint replay, independently of
// checking ITF variable values. Otherwise an edited later checkpoint can reset.
func TestCheckpointITFRoleRoundTrip(t *testing.T) {
	skip.Quint(t)
	for _, mode := range []string{"go", "quint"} {
		t.Run(mode, func(t *testing.T) {
			c, err := NewChecker("testdata/snapshot.yaml", Options{Dir: t.TempDir(), Eval: mode})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			steps := []qtrace.Step{
				{Action: "deposit", Recorder: "r", Process: "p", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}},
				{Action: "restore", Recorder: "r", Process: "p", Seq: 2, Args: map[string]any{"b": int64(5), "n": int64(1)}},
			}
			traces, _ := c.Binding.Reconstruct(steps)
			data, err := c.Binding.ObservedITF(traces[0])
			if err != nil {
				t.Fatal(err)
			}
			observed, err := itf.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			rep, err := c.Check(t.Context(), steps)
			if err != nil || !rep.OK() {
				t.Fatalf("baseline %v: %v", rep, err)
			}
			native, err := os.ReadFile(rep.Actors[0].ITF)
			if err != nil {
				t.Fatal(err)
			}
			validated, err := itf.Parse(native)
			if err != nil {
				t.Fatal(err)
			}
			for name, tr := range map[string]*itf.Trace{"observed": observed, "validated": validated} {
				t.Run(name, func(t *testing.T) {
					st := &tr.States[2]
					if marker := st.Extra["gotogether::checkpoint"]; marker.Kind != itf.Bool || !marker.B {
						t.Fatalf("ITF lost role: %+v", st)
					}
					st.Picks["b"] = itf.IntOf(50)
					res, err := ValidateITF(t.Context(), tr, ITFOptions{Binding: c.Binding, NoVarCheck: true, Options: Options{Dir: t.TempDir()}})
					if err != nil || res.Conforms != Fail || res.Failure == nil || res.Failure.Index != 1 {
						t.Fatalf("checkpoint became reset: result=%+v err=%v", res, err)
					}
				})
			}
		})
	}
}
