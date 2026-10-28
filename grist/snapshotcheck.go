package grist

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
)

// checkPositions bounds how many positions CheckSnapshots takes a
// snapshot at, per read model and key.
const checkPositions = 200

// CheckSnapshots checks every read model's snapshots in st against a
// fold from the start of the log, in every tenant and for every key its
// events carry: the snapshot st holds now, plus the events after it,
// must equal a fold of every event through the head (a snapshot of
// another fold, of a fold changed without a new fingerprint, or taken at
// the wrong position does not); then a snapshot taken at each position
// (each event's of the read model, and the one before it; at most 200)
// plus the events after it must too. States are compared deeply, so a
// state that loses something in JSON fails. It writes snapshots (the last
// at the head), so run it on a test's store. It returns every
// disagreement, naming the read model, the tenant, the key and both
// states; nil when all agree. gristtest.Snapshots runs it.
func (a *App) CheckSnapshots(ctx context.Context, st Store) error {
	if _, ok := st.(SnapshotStore); !ok {
		return fmt.Errorf("grist: store %s (%T) keeps no snapshots (store.SnapshotStore)", st.Name(), st)
	}
	tenants, err := st.Tenants(ctx)
	if err != nil {
		return err
	}
	var ps []string
	for _, rm := range a.readModels {
		for _, t := range tenants {
			ps = append(ps, rm.check(ctx, st, t)...)
		}
	}
	if len(ps) == 0 {
		return nil
	}
	return fmt.Errorf("grist: app %s's snapshots disagree with a fold from the start:\n  %s", a.Name, strings.Join(ps, "\n  "))
}

// checkSnapshots is CheckSnapshots' work for one read model and tenant.
func (m *ReadModel[S]) checkSnapshots(ctx context.Context, app *App, st Store, tenant string) []string {
	if _, survives := m.info.fp(); !survives {
		var zero S
		return []string{fmt.Sprintf("read model %s: its state (%T) does not survive JSON (an unexported field, or a value JSON changes), "+
			"so it is never snapshotted: every read folds its whole log; give it exported fields of plain values", m.info.name, zero)}
	}
	_, head, err := st.Read(ctx, tenant, Selector{}, 0)
	if err != nil {
		return []string{fmt.Sprintf("read model %s, tenant %s: %v", m.info.name, tenant, err)}
	}
	all, _, err := st.Read(ctx, tenant, m.selector(), head)
	if err != nil {
		return []string{fmt.Sprintf("read model %s, tenant %s: %v", m.info.name, tenant, err)}
	}
	keyed := len(m.info.keys) > 0
	var keys [][]TagPair
	if keyed {
		seen := map[string]bool{}
		for _, e := range all {
			var tags []TagPair
			for _, k := range m.info.keys {
				for _, t := range e.Tags {
					if t.Key == k {
						tags = append(tags, t)
						break
					}
				}
			}
			if len(tags) == len(m.info.keys) && !seen[snapshotKey(tags)] {
				seen[snapshotKey(tags)] = true
				keys = append(keys, tags)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return snapshotKey(keys[i]) < snapshotKey(keys[j]) })
	} else {
		keys = [][]TagPair{nil}
	}
	var ps []string
	for _, tags := range keys {
		evs, _, err := st.Read(ctx, tenant, m.selector(tags...), head)
		if err != nil {
			return append(ps, fmt.Sprintf("read model %s, tenant %s: %v", m.info.name, tenant, err))
		}
		want, err := m.Fold(evs)
		if err != nil {
			return append(ps, fmt.Sprintf("read model %s, tenant %s: %v", m.info.name, tenant, err))
		}
		at := func(why string, got S) bool {
			if reflect.DeepEqual(got, want) {
				return true
			}
			g, _ := json.Marshal(got)
			w, _ := json.Marshal(want)
			key := snapshotKey(tags)
			if key == "" {
				key = "(plain)"
			}
			ps = append(ps, fmt.Sprintf("read model %s, tenant %s, key %s: %s gives %s at the head (%d); a fold from the start gives %s",
				m.info.name, tenant, key, why, g, head, w))
			return false
		}
		// the snapshot st holds now, whoever took it
		if !at("its stored snapshot plus the events after it", m.getWith(readsAt(ctx, app, st, tenant, head, ""), tags, math.MaxInt, true)) {
			continue
		}
		// a snapshot at each position that matters
		for _, p := range snapshotPositions(evs, head) {
			before := evs[:sort.Search(len(evs), func(i int) bool { return evs[i].Pos > p })]
			s, err := m.Fold(before)
			if err != nil {
				return append(ps, fmt.Sprintf("read model %s, tenant %s: %v", m.info.name, tenant, err))
			}
			ss := st.(SnapshotStore)
			fp, _ := m.info.fp()
			m.save(readsAt(ctx, app, st, tenant, p, ""), ss, snapshotKey(tags), fp, s)
			if !at(fmt.Sprintf("a snapshot taken at %d plus the events after it", p), m.getWith(readsAt(ctx, app, st, tenant, head, ""), tags, math.MaxInt, true)) {
				break
			}
		}
	}
	return ps
}

// snapshotPositions are the positions CheckSnapshots takes a snapshot
// at: each event's and the one before it (from 1), and the head, at most
// checkPositions of them, in order.
func snapshotPositions(evs []Stored, head Position) []Position {
	seen := map[Position]bool{}
	var out []Position
	add := func(p Position) {
		if p >= 1 && p <= head && !seen[p] && len(out) < checkPositions {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, e := range evs {
		add(e.Pos - 1)
		add(e.Pos)
	}
	add(head)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
