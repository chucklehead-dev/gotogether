package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Replayed is a manifest's run judged again (Replay): the replay's
// judgment and verdict (Got, Got.Verdict), the manifest's (Want), and
// whether they are the same.
type Replayed struct {
	// Got is the replay's judgment; Want the manifest's verdict.
	Got  *Judged
	Want validate.RunVerdict
	// Same is whether Got's verdict is Want (RunVerdict.Same), Differs
	// where they differ (or, Same, how the reasons' wording does).
	Same    bool
	Differs string
	// Warnings are Verify's (an artifact changed: no verdict reads it);
	// Notes what differs from the run's judging and does not refuse the
	// replay (another quint or gotogether version, QUINT_BACKEND ignored).
	Warnings, Notes []string
}

// Replay judges the run of manifest m again, as `gotogether validate
// -replay` does: its files unchanged (Verify: a changed input, binding,
// spec or import is an error naming each), judged as the manifest says
// (its instance, searches, finish check, declared losses and
// watermarks, backend), and its verdict compared with the manifest's
// (Replayed.Same, RunVerdict.Same). o gives only what the manifest does
// not decide: Quint, Dir (default a temporary directory: Got.Close
// removes it), NoITF; a Backend other than the manifest's is refused,
// since verdicts depend on it, and a run judged on the default backend
// is replayed on it whatever QUINT_BACKEND says. Read m with
// validate.ReadManifest: its paths are resolved against its directory.
func Replay(ctx context.Context, m *validate.Manifest, o validate.Options) (*Replayed, error) {
	if m.Receipt != nil && m.Receipt.Composition != nil {
		return nil, fmt.Errorf("composition receipt is evidence, not an executable registration plan: rerun the originating registered System/test; primary-binding replay cannot reproduce its local, peer and protocol checks")
	}
	warns, err := m.Verify()
	if err != nil {
		return nil, err
	}
	r := &Replayed{Want: m.Verdict, Warnings: warns}
	if o.Backend != "" && o.Backend != m.Tools.Backend {
		return nil, fmt.Errorf("-backend %s: the run was judged on backend %q (\"\": the default), and verdicts depend on it: drop -backend", o.Backend, m.Tools.Backend)
	}
	o.Backend = m.Tools.Backend
	if env := os.Getenv("QUINT_BACKEND"); m.Tools.Backend == "" && env != "" {
		r.Notes = append(r.Notes, fmt.Sprintf("QUINT_BACKEND=%s ignored: the run was judged on the default backend", env))
		o.NoEnvBackend = true
	}
	chk := Check{Options: o, Format: formatOf(m.Check.Format), NoVars: m.Check.NoVars, RequireFinish: m.Check.RequireFinish, finishFixed: true,
		Search: m.Check.SearchOptions(), LostPrefix: m.Check.LostPrefix, LostSuffix: m.Check.LostSuffix, CompleteThrough: m.Check.CompleteThrough,
		Module: m.Model.Module, Invariants: m.Model.Invariants, Constants: m.Model.Constants, itfModels: map[string]*validate.RunModel{}}
	if m.Model.Binding != nil {
		chk.Binding = m.Model.Binding.Path
	}
	for _, in := range m.Inputs {
		if in.Model != nil {
			chk.itfModels[in.Path] = in.Model
			if in.Model.Binding != nil && chk.Binding == "" {
				chk.Binding = in.Model.Binding.Path
			}
		}
	}
	if m.Model.Convert != nil {
		chk.Convert = m.Model.Convert.Path
	}
	for _, in := range m.Inputs {
		chk.Files = append(chk.Files, in.Path)
	}
	if len(chk.Files) == 0 {
		return nil, fmt.Errorf("the manifest names no input: it was written for a run with none, and there is nothing to judge")
	}
	now := validate.CurrentTools(o)
	if now.Quint != m.Tools.Quint {
		r.Notes = append(r.Notes, fmt.Sprintf("the run was judged with quint %q, this replay with %q", m.Tools.Quint, now.Quint))
	}
	if now.Gotogether != m.Tools.Gotogether {
		r.Notes = append(r.Notes, fmt.Sprintf("the run was judged with gotogether %s, this replay with %s", m.Tools.Gotogether, now.Gotogether))
	}
	j, err := chk.Judge(ctx)
	r.Got = j
	if err != nil {
		return r, err
	}
	r.Same, r.Differs = m.Verdict.Same(j.Verdict)
	return r, nil
}

// formatOf is a manifest's recorded format as Check takes it: "auto"
// detects.
func formatOf(f string) input.Format {
	if f == "auto" {
		return input.Unknown
	}
	return input.Format(f)
}
