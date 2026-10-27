// Package shrinkfiles writes a shrink's files beside its minimal run's
// manifest, as qtest.ShrinkActions and gotogether shrink both lay them
// out: the layout is theirs, not qshrink's, which knows no manifest.
package shrinkfiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chucklehead-dev/gotogether/qshrink"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Write writes actions.json (actions, the shrunk sequence: res.Set's
// actions of the original, as JSON, or each as Go prints it when they do
// not marshal) and shrink.json (res, qshrink.Result.MarshalJSON) into
// dir, and returns them as the manifest's artifacts, named "actions" and
// "shrink". actions must be res.Set's: a count that is not len(res.Set)
// is refused, since shrink.json would contradict actions.json.
func Write(dir string, res *qshrink.Result, actions []any) ([]validate.Artifact, error) {
	if len(actions) != len(res.Set) {
		return nil, fmt.Errorf("shrinkfiles: %d actions for a shrunk set of %d", len(actions), len(res.Set))
	}
	acts, err := json.MarshalIndent(actions, "", "  ")
	if err != nil {
		printed := make([]string, len(actions))
		for i, a := range actions {
			printed[i] = fmt.Sprintf("%+v", a)
		}
		acts, _ = json.MarshalIndent(printed, "", "  ")
	}
	rec, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var arts []validate.Artifact
	for _, f := range []struct {
		name string
		data []byte
	}{{"actions", acts}, {"shrink", rec}} {
		p := filepath.Join(dir, f.name+".json")
		if err := os.WriteFile(p, append(f.data, '\n'), 0o644); err != nil {
			return nil, err
		}
		arts = append(arts, validate.Artifact{Name: f.name, Path: p})
	}
	return arts, nil
}
