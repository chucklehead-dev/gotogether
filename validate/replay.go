package validate

import (
	"context"
	"fmt"
	"os"
)

// Replayed is a manifest's run judged again (Replay): the judgment, and
// whether its verdict is the manifest's.
type Replayed struct {
	*Judged
	// Want is the manifest's verdict; Same whether the replay's is it,
	// Note where they differ (or, Same, that reasons are worded otherwise).
	Want RunVerdict
	Same bool
	Note string
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
// not decide: Quint, Dir (default a temporary directory), NoITF; a
// Backend other than the manifest's is refused, since verdicts depend on
// it, and a run judged on the default backend is replayed on it whatever
// QUINT_BACKEND says. Read m with ReadManifest: its paths are resolved
// against its directory.
func Replay(ctx context.Context, m *Manifest, o Options) (*Replayed, error) {
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
		o.noEnvBackend = true
	}
	cmd := Command{Options: o, Format: m.Check.Format, NoVars: m.Check.NoVars, RequireSettle: m.Check.RequireSettle, finishFixed: true,
		Search: m.Check.SearchOptions(), LostPrefix: m.Check.LostPrefix, LostSuffix: m.Check.LostSuffix, CompleteThrough: m.Check.CompleteThrough,
		Module: m.Model.Module, Invariants: m.Model.Invariants, Constants: m.Model.Constants, itfModels: map[string]*RunModel{}}
	if m.Model.Binding != nil {
		cmd.Binding = m.Model.Binding.Path
	}
	for _, in := range m.Inputs {
		if in.Model != nil {
			cmd.itfModels[in.Path] = in.Model
			if in.Model.Binding != nil && cmd.Binding == "" {
				cmd.Binding = in.Model.Binding.Path
			}
		}
	}
	if m.Model.Convert != nil {
		cmd.Convert = m.Model.Convert.Path
	}
	for _, in := range m.Inputs {
		cmd.Files = append(cmd.Files, in.Path)
	}
	if len(cmd.Files) == 0 {
		return nil, fmt.Errorf("the manifest names no input: it was written for a run with none, and there is nothing to judge")
	}
	if q := QuintVersion(o.Quint); q != m.Tools.Quint {
		r.Notes = append(r.Notes, fmt.Sprintf("the run was judged with quint %q, this replay with %q", m.Tools.Quint, q))
	}
	if g := ModuleVersion(); g != m.Tools.Gotogether {
		r.Notes = append(r.Notes, fmt.Sprintf("the run was judged with gotogether %s, this replay with %s", m.Tools.Gotogether, g))
	}
	j, err := cmd.Judge(ctx)
	r.Judged = j
	if err != nil {
		return r, err
	}
	r.Same, r.Note = m.Verdict.Same(j.Verdict)
	return r, nil
}
