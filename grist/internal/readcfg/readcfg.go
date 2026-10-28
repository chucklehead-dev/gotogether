// Package readcfg carries a run's override of an App's read settings
// (grist.Snapshots, grist.AuditSnapshots, grist.Memo) in a context: what
// gristsim's Config.SnapshotEvery sets for one simulation, where the
// App, shared by runs in parallel, cannot change. Apps set theirs with
// grist.New's options; nothing outside the grist module reaches this.
package readcfg

import "context"

// Settings are the overrides: a field set (Has*) replaces the App's.
type Settings struct {
	Every, Audit, Memo          int
	HasEvery, HasAudit, HasMemo bool
}

type key struct{}

// With is ctx carrying s merged over the overrides ctx carries.
func With(ctx context.Context, s Settings) context.Context {
	prev := Of(ctx)
	if !s.HasEvery {
		s.Every, s.HasEvery = prev.Every, prev.HasEvery
	}
	if !s.HasAudit {
		s.Audit, s.HasAudit = prev.Audit, prev.HasAudit
	}
	if !s.HasMemo {
		s.Memo, s.HasMemo = prev.Memo, prev.HasMemo
	}
	return context.WithValue(ctx, key{}, s)
}

// Of is the overrides ctx carries (none: the zero Settings).
func Of(ctx context.Context) Settings {
	s, _ := ctx.Value(key{}).(Settings)
	return s
}
