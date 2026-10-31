package grist

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/grist/internal/values"
)

// An EventOption configures NewEvent: an Upcaster (Upcast), or the
// version its appends write while a rollout lasts (WritesVersion).
type EventOption interface{ eventOption() }

func (Upcaster) eventOption() {}

// downcaster is WritesVersion's: the version appends write, and how the
// event's struct becomes that version's.
type downcaster struct {
	version int
	in, out reflect.Type // the event's struct, and version's
	f       func(reflect.Value) reflect.Value
	outTags []tagField // version's struct's tags (its upcaster's input's)
}

func (*downcaster) eventOption() {}

// WritesVersion makes the event type's appends write version (an older
// one) instead of its newest, through down, which turns the newest struct
// (New, the event's own) into version's (Old, the struct its upcaster
// takes): the release that adds a version reads it, and writes the
// version the release before it reads, so a rolling update never shows a
// node of the older release an event it refuses (a *NewerVersionError).
// The next release, deployed once no node of the older one runs, drops
// WritesVersion and writes the newest: readers first, then writers
// (docs/grist.md §2.1, docs/grist-platform.md §3.9).
//
//	var Added = grist.NewEvent[BookAdded](App, "book-added",
//		grist.Upcast(1, func(v BookAddedV1) BookAdded { return BookAdded{Book: v.Book, Count: v.Copies, Shelf: "main"} }),
//		grist.WritesVersion(1, func(v BookAdded) BookAddedV1 { return BookAddedV1{Book: v.Book, Copies: v.Count} }))
//
// down is pure and total, and keeps every tag (Verify runs it over
// generated values at boot); CheckUpcasters also requires it to undo the
// upcasters (a version event upcast, then written again, is what it
// was). An append whose event the older version cannot hold (down, then
// the upcasters, give another event: a value only the newest version has
// a field for) fails, naming the event: the release writing the older
// version must not decide what only the newest can say, until the next.
func WritesVersion[New, Old any](version int, down func(New) Old) EventOption {
	return &downcaster{version: version, in: reflect.TypeFor[New](), out: reflect.TypeFor[Old](),
		f: func(v reflect.Value) reflect.Value { return reflect.ValueOf(down(v.Interface().(New))) }}
}

// writesVersion is the version an append writes.
func (info *eventInfo) writesVersion() int {
	if info.writes != nil {
		return info.writes.version
	}
	return info.version
}

// writesProblems checks ws, the event's WritesVersion options, against
// its chain (info.ups, set by chain) and sets info.writes.
func (info *eventInfo) writesProblems(ws []*downcaster) []string {
	if len(ws) == 0 {
		return nil
	}
	name := info.name
	if len(ws) > 1 {
		return []string{fmt.Sprintf("event %q: %d WritesVersion options: an event's appends write one version", name, len(ws))}
	}
	d := ws[0]
	switch {
	case d.version >= info.version:
		return []string{fmt.Sprintf("event %q: WritesVersion(%d, ...) of an event whose newest version is %d: appends write the newest already; "+
			"WritesVersion names an older version, the previous release's newest, while a rollout lasts: remove it once no node of that release runs", name, d.version, info.version)}
	case d.version < 1:
		return []string{fmt.Sprintf("event %q: WritesVersion(%d, ...): versions start at 1", name, d.version)}
	case info.ups == nil:
		return nil // the chain's problems say why
	case d.in != info.typ:
		return []string{fmt.Sprintf("event %q: WritesVersion's downcaster takes %v, but the event's struct is %v: it turns the newest version into the one written", name, d.in, info.typ)}
	}
	if want := info.ups[d.version-1].in; d.out != want {
		return []string{fmt.Sprintf("event %q: WritesVersion(%d, ...)'s downcaster returns %v, but version %d's struct is %v (what its upcaster takes): it returns the struct the version was stored as", name, d.version, d.out, d.version, want)}
	}
	d.outTags = info.ups[d.version-1].inTags
	info.writes = d
	return nil
}

// down is the downcaster over val (a value of the event's struct), its
// panic recovered as its error.
func (d *downcaster) down(val reflect.Value) (out reflect.Value, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("WritesVersion(%d, ...)'s downcaster panicked: %v (a downcaster is total: it returns version %d for every value of the event's struct)", d.version, p, d.version)
		}
	}()
	return d.f(val), nil
}

// downcasterProblems runs the event's downcaster, if any, over generated
// values of its struct: total, pure, keeping every tag; with inverse,
// each generated value of the version it writes, upcast and written
// again, must be what it was (but the fields its upcaster Drops).
func (info *eventInfo) downcasterProblems(inverse bool) []string {
	d := info.writes
	if d == nil {
		return nil
	}
	var ps []string
	decode := func(typ reflect.Type, b []byte) reflect.Value {
		p := reflect.New(typ)
		json.Unmarshal(b, p.Interface()) // encoded from a value of the type: it decodes
		return p.Elem()
	}
	for _, s := range values.All(info.typ, upcastAlphabet, upcastInts, upcastSamples) {
		b, _ := json.Marshal(s)
		out1, err := d.down(decode(info.typ, b))
		if err != nil {
			ps = append(ps, fmt.Sprintf("event %q: on %s: %v", info.name, b, err))
			break
		}
		out2, _ := d.down(decode(info.typ, b))
		j1, _ := json.Marshal(out1.Interface())
		j2, _ := json.Marshal(out2.Interface())
		if !bytes.Equal(j1, j2) {
			ps = append(ps, fmt.Sprintf("event %q: WritesVersion(%d, ...)'s downcaster made %s into %s and then %s: a downcaster is a pure function of the event (no clock, no randomness, no state)", info.name, d.version, b, j1, j2))
			break
		}
		if in, got := tagsOf(decode(info.typ, b), info.tags), tagsOf(out1, d.outTags); !slices.Equal(in, got) {
			ps = append(ps, fmt.Sprintf("event %q: %s has tags %v, written as version %d %v: the store indexes an event by the tags it is written with, "+
				"and the upcast reads it with the newest's; a downcaster keeps every tag (key and value)", info.name, b, in, d.version, got))
			break
		}
	}
	if !inverse || len(ps) > 0 {
		return ps
	}
	u := info.ups[d.version-1]
	for _, s := range values.All(d.out, upcastAlphabet, upcastInts, upcastSamples) {
		b, _ := json.Marshal(s)
		up, err := info.upcastFrom(d.version, decode(d.out, b))
		if err != nil {
			break // the upcasters' own check reports it
		}
		back, err := d.down(up)
		if err != nil {
			ps = append(ps, fmt.Sprintf("event %q: on %s upcast: %v", info.name, b, err))
			break
		}
		was, now := decode(d.out, b), back
		for _, drop := range u.drops {
			for i := 0; i < d.out.NumField(); i++ {
				if rec.JSONName(d.out.Field(i)) == drop {
					was.Field(i).Set(reflect.Zero(d.out.Field(i).Type))
					now.Field(i).Set(reflect.Zero(d.out.Field(i).Type))
				}
			}
		}
		j1, _ := json.Marshal(was.Interface())
		j2, _ := json.Marshal(now.Interface())
		if !bytes.Equal(j1, j2) {
			ps = append(ps, fmt.Sprintf("event %q: version %d %s, upcast to version %d and written again (WritesVersion's downcaster), is %s: "+
				"a node of this release would write another event than the release before it for the same decision; the downcaster undoes the upcasters", info.name, d.version, b, info.version, j2))
			break
		}
	}
	return ps
}

// written makes events, a block's appends, what the store keeps: an
// event of a type that writes an older version (WritesVersion) as that
// version, its body downcast. It fails on one that version cannot hold:
// the downcast, upcast again, is another event.
func (a *App) written(block string, events []Event) error {
	if err := a.checkEventObservations(block, events); err != nil {
		return err
	}
	for i, e := range events {
		info := a.events[e.Type]

		if info.writes == nil || storedVersion(e.Version) != info.version {
			continue
		}
		d := info.writes
		p := reflect.New(info.typ)
		if err := json.Unmarshal(e.Body, p.Interface()); err != nil {
			return fmt.Errorf("grist: %s's event %s: %w", block, e.Type, err)
		}
		was, _ := json.Marshal(p.Elem().Interface())
		old, err := d.down(p.Elem())
		if err != nil {
			return fmt.Errorf("grist: %s's event %s: %w", block, e.Type, err)
		}
		back, err := info.upcastFrom(d.version, old)
		if err != nil {
			return fmt.Errorf("grist: %s's event %s: %w", block, e.Type, err)
		}
		if now, _ := json.Marshal(back.Interface()); !bytes.Equal(was, now) {
			return fmt.Errorf("grist: %s appends %s %s, and this release writes %s as version %d (grist.WritesVersion) until no node of the release before it runs, "+
				"which cannot hold it: written as version %d it reads back as %s. Decide only what version %d can say until the release that writes version %d (the next, which drops WritesVersion)",
				block, e.Type, was, e.Type, d.version, d.version, now, d.version, info.version)
		}
		body, _ := json.Marshal(old.Interface())
		tags := make([]TagPair, 0, len(d.outTags))
		for _, tf := range d.outTags { // in field order, as Of gives them
			tags = append(tags, TagPair{Key: tf.key, Value: fmt.Sprint(old.Field(tf.index).Interface())})
		}
		events[i] = Event{Type: e.Type, Version: d.version, Tags: tags, Body: body}
		if _, _, _, err := info.storedValue(events[i], info.version); err != nil {
			return fmt.Errorf("grist: %s's written event %s observations: %w", block, e.Type, err)
		}
	}
	return nil
}

// readsAll reports whether a reads every version of every event type
// other reads: a newer release of the same app, or the same one.
func (a *App) readsAll(other *App) bool {
	for n, info := range other.events {
		if mine := a.events[n]; mine == nil || mine.version < info.version {
			return false
		}
	}
	return true
}

// An olderRelease is a command or query this node cannot serve: a read
// met an event of a version newer than its release reads (a
// *NewerVersionError), appended by a newer release while a rollout
// lasts. Nothing was decided or recorded: it is ErrUnavailable (a 503
// over the API), retried under its key, which a node of the newer
// release serves.
type olderRelease struct {
	block string
	nv    *NewerVersionError
}

func (e *olderRelease) Error() string {
	return fmt.Sprintf("grist: %s: this node cannot serve it, a node of a newer release can (retry later): %v", e.block, e.nv)
}

func (e *olderRelease) Is(target error) bool { return target == ErrUnavailable }
func (e *olderRelease) Unwrap() error        { return e.nv }

// newerIn is the *NewerVersionError among errs, or nil.
func newerIn(errs ...error) *NewerVersionError {
	for _, err := range errs {
		var nv *NewerVersionError
		if errors.As(err, &nv) {
			return nv
		}
	}
	return nil
}
