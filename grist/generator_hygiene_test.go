package grist_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/gen"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// Application vocabulary belongs in public actions and bindings; local draws
// must not force an application to rename its command or its JSON fields.
func TestGeneratorDrawsDoNotShadowApplicationActions(t *testing.T) {
	requireQuint(t)
	type orderArgs struct {
		Order    string `json:"order"`
		ArgOrder int    `json:"arg_order"`
	}
	type placeArgs struct {
		TriplesOf string `json:"triplesOf"`
	}
	type lookupArgs struct {
		Lookup   string `json:"lookup"`
		Log      string `json:"log"`
		LogInt   int    `json:"log_int"`
		Answered bool   `json:"answered"`
	}
	type warehouseFields struct {
		Warehouse string `json:"warehouse"`
		Key       string `json:"key"`
	}
	for _, tc := range []struct {
		command  string
		extended bool
	}{{"order", false}, {"order", true}, {"caller", false}, {"id", false}, {"place", false}} {
		name := tc.command
		if tc.extended {
			name += "/query-and-site"
		}
		t.Run(name, func(t *testing.T) {
			app := grist.New("hygiene")
			if tc.command == "place" {
				grist.NewCommand(app, tc.command, func(*grist.Reads, placeArgs) ([]grist.Event, error) { return nil, nil }, grist.Unconditioned("records a placement"), grist.Internal())
			} else {
				grist.NewCommand(app, tc.command, func(*grist.Reads, orderArgs) ([]grist.Event, error) { return nil, nil }, grist.Unconditioned("records an order"), grist.Internal())
			}
			if tc.extended {
				grist.NewQuery(app, "lookup", func(*grist.Reads, lookupArgs) (int, error) { return 0, nil }, grist.Internal())
				grist.NewSite[warehouseFields](app, "warehouse")
			}
			dir := t.TempDir()
			starter, err := gen.Starter(app)
			if err != nil {
				t.Fatal(err)
			}
			for path, data := range starter {
				target := filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			generated, err := gen.Generate(app, dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(generated.Missing) != 0 {
				t.Fatalf("starter left holes: %v", generated.Missing)
			}
			if err := generated.Write(dir); err != nil {
				t.Fatal(err)
			}
			steps := string(generated.Files["model/hygiene_steps.qnt"])
			for _, signature := range []string{"action order(order: str, arg_order: int, id: str, caller: str, readPos: int, reads: Set[(str, str, str)], result: str)", "action order(order: str, arg_order: int, id: str, caller: str, result: str)"} {
				signature = strings.Replace(signature, "action order(", "action "+tc.command+"(", 1)
				if tc.command == "place" {
					signature = strings.Replace(signature, "order: str, arg_order: int", "triplesOf: str", 1)
				}
				if !strings.Contains(steps, signature) {
					t.Errorf("public action signature changed: %s", signature)
				}
			}
			for _, binding := range []string{"hygiene.binding.yaml", "hygiene.calls.binding.yaml"} {
				if !strings.Contains(string(generated.Files[binding]), "  "+tc.command+":") {
					t.Errorf("binding lost %s: %s", tc.command, binding)
				}
			}
			modelDir := filepath.Join(dir, "model")
			hygieneQuint(t, modelDir, "typecheck", "hygiene.qnt")
			if !tc.extended {
				// With only this command, executing the actual generated step must answer
				// one identity. The client action also runs explicitly: its generated
				// step includes denied calls, which intentionally answer no identity.
				clientArgs := `"a", 1, "i1", "a", "ok"`
				if tc.command == "place" {
					clientArgs = `"a", "i1", "a", "ok"`
				}
				witness := fmt.Sprintf(`module hygieneWitness {
 import hygiene.* from "./hygiene"
 run commandTest = init.then(step).expect(answered.size() == 1)
}
module hygieneCallsWitness {
 import hygieneCalls.* from "./hygiene"
 run generatedStepTest = init.then(step).expect(answered.size() <= 1)
 run commandTest = init.then(%s(%s)).expect(answered.size() == 1)
}
`, tc.command, clientArgs)
				if err := os.WriteFile(filepath.Join(modelDir, "witness.qnt"), []byte(witness), 0644); err != nil {
					t.Fatal(err)
				}
				hygieneQuint(t, modelDir, "test", "--main=hygieneWitness", "witness.qnt")
				hygieneQuint(t, modelDir, "test", "--main=hygieneCallsWitness", "witness.qnt")
			}
		})
	}
}

// Each external check belongs to the test context, including evaluator children.
func hygieneQuint(t *testing.T, dir string, args ...string) {
	t.Helper()
	for i, arg := range args {
		if strings.HasSuffix(arg, ".qnt") && !filepath.IsAbs(arg) {
			args[i] = filepath.Join(dir, arg)
		}
	}
	cmd := quintcmd.Command(t.Context(), "", dir, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("quint %v: %v\n%s", args, err, out)
	}
}
