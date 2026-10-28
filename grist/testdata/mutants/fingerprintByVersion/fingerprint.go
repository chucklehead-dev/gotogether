package grist

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"
	"github.com/chucklehead-dev/gotogether/grist/internal/values"
)

// The events a fingerprint's probe folds: every combination of these at
// each field, at most probeSamples per event type, interleaved.
var (
	probeAlphabet = []string{"a", "b"} // read-only
	probeInts     = []int{-1, 0, 1, 3} // read-only
)

const probeSamples = 16

// eventShape is an event type's newest shape, as a fingerprint and
// grist.lock record it: its version, its body's JSON Schema and its tags.
func eventShape(info *eventInfo) string {
	b, _ := json.Marshal(struct {
		Version int            `json:"version"`
		Body    map[string]any `json:"body"`
		Tags    []string       `json:"tags"`
	}{info.version, catalog.Schema(info.typ), info.tagKeys()})
	return string(b)
}

// fingerprint is what the read model's snapshots are keyed by, and
// whether its state survives JSON (else it is never snapshotted): a hash
// of its name, version and keys, the newest shape of each event type it
// folds, and its probe, what its fold makes of generated events of those
// types (each state's JSON, in turn). A snapshot taken by another fold is
// then another fingerprint's, never read: a fold given a new event type,
// an event whose shape changed, or a fold whose code changed what it makes
// of the probe's events. Each upcaster of those types is in it too, by
// what it makes of generated values of its own version (upcastProbe), so
// a fixed upcaster refolds every snapshot folded through the buggy one. A
// change the probes cannot see (a branch none of their values takes) is
// the version's to say.
func (m *ReadModel[S]) fingerprint() (string, bool) {
	h := sha256.New()
	fmt.Fprintf(h, "grist.snapshot/1\x00%s\x00%d\x00%q\x00", m.info.name, m.info.version, m.info.keys)
	// mutant: the fingerprint is the read model's name, version and keys
	// alone, so a snapshot of a fold that changed without a new version is
	// reused
	survives := m.probe(func([]byte) {})
	return "f" + hex.EncodeToString(h.Sum(nil))[:24], survives
}

// probe folds generated events of the read model's types from its
// initial value, giving each state's JSON to see, and reports whether
// every state survives JSON (decodes, onto a fresh initial value, to a
// state deeply equal to it: no unexported field, nothing JSON drops). A
// fold that panics or fails ends the probe there, the failure seen.
func (m *ReadModel[S]) probe(see func([]byte)) (survives bool) {
	var sets [][]any
	for _, info := range m.events {
		sets = append(sets, values.All(info.typ, probeAlphabet, probeInts, probeSamples))
	}
	survives = true
	defer func() {
		if p := recover(); p != nil {
			see(fmt.Appendf(nil, "panic: %v", p))
		}
	}()
	s := m.init()
	pos := Position(0)
	for k := 0; k < probeSamples; k++ {
		for i, info := range m.events {
			if k >= len(sets[i]) {
				continue
			}
			body, _ := json.Marshal(sets[i][k])
			pos++
			v := reflect.ValueOf(sets[i][k])
			e := Stored{Event: Event{Type: info.name, Version: info.version, Tags: tagsOf(v, info.tags), Body: body},
				Tenant: "probe", Pos: pos, Time: int64(pos) * 1000, Cause: "probe"}
			var err error
			if s, err = m.on[info.name](s, e); err != nil {
				see(fmt.Appendf(nil, "error: %v", err))
				return survives
			}
			state, err := json.Marshal(s)
			if err != nil {
				see(fmt.Appendf(nil, "unencodable: %v", err))
				return false
			}
			see(state)
			back := m.init()
			if json.Unmarshal(state, &back) != nil || !reflect.DeepEqual(back, s) {
				survives = false
			}
		}
	}
	return survives
}

// upcastProbe gives see what each upcaster of info's chain makes, through
// the rest of the chain, of generated values of its own version (their
// newest-version JSON, or the failure): the upcasters' part of a
// fingerprint.
func upcastProbe(info *eventInfo, see func([]byte)) {
	for _, u := range info.ups {
		for _, v := range values.All(u.in, probeAlphabet, probeInts, probeSamples) {
			out, err := info.upcastFrom(u.from, reflect.ValueOf(v))
			if err != nil {
				see(fmt.Appendf(nil, "v%d error: %v", u.from, err))
				continue
			}
			b, _ := json.Marshal(out.Interface())
			see(fmt.Appendf(nil, "v%d %s", u.from, b))
		}
	}
}
