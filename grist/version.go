package grist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/grist/internal/values"
)

// An Upcaster turns a stored event of one version into the next
// version's (Upcast): what NewEvent takes for each old version of an
// event whose shape changed.
type Upcaster struct {
	from    int
	in, out reflect.Type
	f       func(reflect.Value) reflect.Value
	drops   []string
	inTags  []tagField // the old struct's tags, as its events were indexed
}

// Upcast is the upcaster of an event's version from: f takes a stored
// event of that version, decoded into Old (the struct the event had, kept
// under a name of its own: BookAddedV1), and returns the next version's
// (New: the event's struct itself, for the newest). f is a pure, total
// function of its argument: it is run over generated values of Old
// (App.Verify, App.CheckUpcasters), and a stored event may be upcast any
// number of times, on any node. It must not change the event's tags: the
// store indexed the event by the tags it was appended with, so a query
// would disagree with a fold; Verify refuses one that does.
//
// A field of Old that f reads nothing of is dropped from every old event:
// CheckUpcasters reports it, unless Drops says so on purpose.
func Upcast[Old, New any](from int, f func(Old) New) Upcaster {
	return Upcaster{from: from, in: reflect.TypeFor[Old](), out: reflect.TypeFor[New](),
		f: func(v reflect.Value) reflect.Value { return reflect.ValueOf(f(v.Interface().(Old))) }}
}

// Drops says that the upcaster drops the fields of Old named (their JSON
// names) on purpose: CheckUpcasters does not report them.
func (u Upcaster) Drops(fields ...string) Upcaster {
	u.drops = append(slices.Clone(u.drops), fields...)
	return u
}

// NewerVersionError is a stored event of a version newer than the app's
// newest: an app of a newer version appended it (a rolling update, or a
// rollback), and this one cannot know what its fields mean, so it refuses
// to decode it rather than guess. A fold, a handler or a rule that meets
// one fails with it, by name.
type NewerVersionError struct {
	Type    string
	Version int // the stored event's
	Newest  int // this app's newest of Type
}

func (e *NewerVersionError) Error() string {
	return fmt.Sprintf("%s is version %d, and this app reads %s up to version %d: a newer version of the app appended it, "+
		"and this one cannot know what its fields mean; finish the rollout. A field added needs no new version (grist.lock takes it, and old readers ignore it); "+
		"a new version's writes cannot yet wait for every node to read it, so its readers ship first only by holding the writers back (docs/grist-platform.md §3.9)", e.Type, e.Version, e.Type, e.Newest)
}

// storedVersion is a stored event's version: 0, an Event written by hand
// with none, is 1.
func storedVersion(v int) int {
	if v == 0 {
		return 1
	}
	return v
}

// chain checks upcasters as the chain of info's versions and sets
// info.version and info.ups, returning what is wrong with them. The
// chain's links are Go types, so a mismatch is found at registration.
func (info *eventInfo) chain(ups []Upcaster) []string {
	info.version = 1 + len(ups)
	if len(ups) == 0 {
		return nil
	}
	name := info.name
	var ps []string
	sorted := slices.Clone(ups)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].from < sorted[j].from })
	for i, u := range sorted {
		switch want := i + 1; {
		case u.from < 1:
			ps = append(ps, fmt.Sprintf("event %q: Upcast(%d, ...): versions start at 1 (the first an event had)", name, u.from))
		case u.from != want:
			ps = append(ps, fmt.Sprintf("event %q: its %d upcasters are from versions %v, where version %d needs one from each of 1 to %d: "+
				"each version an event was stored at has an upcaster to the next (a missing one leaves its stored events unreadable)",
				name, len(ups), froms(sorted), info.version, info.version-1))
		}
		if len(ps) > 0 {
			break
		}
	}
	if len(ps) > 0 {
		return ps
	}
	for i := range sorted {
		u := &sorted[i]
		if u.in.Kind() != reflect.Struct {
			ps = append(ps, fmt.Sprintf("event %q: the upcaster from version %d takes %v, no struct: an old version is the struct its events were stored as", name, u.from, u.in))
			continue
		}
		for _, p := range modelable(u.in, u.in.Name()) {
			ps = append(ps, fmt.Sprintf("event %q version %d: %s", name, u.from, p))
		}
		var tps []string
		u.inTags, tps = tagFieldsOf(u.in)
		for _, p := range tps {
			ps = append(ps, fmt.Sprintf("event %q version %d: %s", name, u.from, p))
		}
		next := info.typ
		if i+1 < len(sorted) {
			next = sorted[i+1].in
		}
		if u.out != next {
			what := fmt.Sprintf("the upcaster from version %d takes", u.from+1)
			if i+1 == len(sorted) {
				what = "the event's own struct is"
			}
			ps = append(ps, fmt.Sprintf("event %q: the upcaster from version %d returns %v, but %s %v: each upcaster returns what the next version is", name, u.from, u.out, what, next))
		}
		fields := jsonFields(u.in)
		for _, d := range u.drops {
			if !slices.Contains(fields, d) {
				ps = append(ps, fmt.Sprintf("event %q: the upcaster from version %d drops %q, which %v has no field of (its fields by JSON name: %v)", name, u.from, d, u.in, fields))
			}
		}
	}
	if len(ps) == 0 {
		info.ups = sorted
	}
	return ps
}

func froms(ups []Upcaster) []int {
	out := make([]int, len(ups))
	for i, u := range ups {
		out[i] = u.from
	}
	return out
}

// jsonFields are a struct's exported fields' JSON names, in field order.
func jsonFields(typ reflect.Type) []string {
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() && rec.JSONName(f) != "-" {
			out = append(out, rec.JSONName(f))
		}
	}
	return out
}

// upcast is a stored event decoded as its version and upcast to the
// newest: a value of info.typ.
func (info *eventInfo) upcast(e Event) (reflect.Value, error) {
	v := storedVersion(e.Version)
	switch {
	case v > info.version:
		return reflect.Value{}, &NewerVersionError{Type: info.name, Version: v, Newest: info.version}
	case v < 1:
		return reflect.Value{}, fmt.Errorf("%s is version %d: versions start at 1; no grist appends it", info.name, v)
	case v == info.version:
		p := reflect.New(info.typ)
		if err := json.Unmarshal(e.Body, p.Interface()); err != nil {
			return reflect.Value{}, err
		}
		return p.Elem(), nil
	case info.ups == nil:
		return reflect.Value{}, fmt.Errorf("%s version %d has no upcaster to version %d (the app does not verify: App.Verify says why)", info.name, v, v+1)
	}
	p := reflect.New(info.ups[v-1].in)
	if err := json.Unmarshal(e.Body, p.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("%s version %d: %w", info.name, v, err)
	}
	return info.upcastFrom(v, p.Elem())
}

// upcastFrom runs the upcasters from version v over val (a value of
// version v's struct), recovering an upcaster's panic as its error.
func (info *eventInfo) upcastFrom(v int, val reflect.Value) (out reflect.Value, err error) {
	k := v
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("the upcaster of %s from version %d panicked: %v (an upcaster is total: it returns the next version for every stored value)", info.name, k, p)
		}
	}()
	for ; k < info.version; k++ {
		val = info.ups[k-1].f(val)
	}
	return val, nil
}

// currentBody is a stored event's body as the newest version's JSON: the
// body itself when it is the newest, else its upcast, encoded.
func (info *eventInfo) currentBody(e Event) (json.RawMessage, error) {
	if storedVersion(e.Version) == info.version {
		return e.Body, nil
	}
	v, err := info.upcast(e)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v.Interface())
}

// tagsOf are the tags of v, a struct whose tag fields are tags, sorted.
func tagsOf(v reflect.Value, tags []tagField) []TagPair {
	out := make([]TagPair, 0, len(tags))
	for _, tf := range tags {
		out = append(out, TagPair{Key: tf.key, Value: fmt.Sprint(v.Field(tf.index).Interface())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// The values every upcaster is run over: every combination of these at
// each field, at most upcastSamples of them (values.All).
var (
	upcastAlphabet = []string{"a", "b", ""}   // read-only
	upcastInts     = []int{-1, 0, 1, 2, 1000} // read-only
)

const upcastSamples = 64

// upcasterProblems runs every upcaster of the App over generated values
// of its version's struct: each must be total (no panic), pure (the same
// value twice gives the same JSON) and keep the event's tags; with
// fieldUse, each field of the old struct must reach the next version
// (perturbing it changes the upcaster's output for some value) unless the
// upcaster Drops it.
func (a *App) upcasterProblems(fieldUse bool) []string {
	var ps []string
	for _, n := range a.eventOrder {
		info := a.events[n]
		for _, u := range info.ups {
			ps = append(ps, info.checkUpcaster(u, fieldUse)...)
		}
	}
	return ps
}

func (info *eventInfo) checkUpcaster(u Upcaster, fieldUse bool) []string {
	var ps []string
	samples := values.All(u.in, upcastAlphabet, upcastInts, upcastSamples)
	decode := func(b []byte) reflect.Value {
		p := reflect.New(u.in)
		json.Unmarshal(b, p.Interface()) // encoded from a value of the type: it decodes
		return p.Elem()
	}
	for _, s := range samples {
		b, _ := json.Marshal(s)
		out1, err := info.upcastFrom(u.from, decode(b))
		if err != nil {
			ps = append(ps, fmt.Sprintf("event %q: on stored version %d %s: %v", info.name, u.from, b, err))
			break
		}
		out2, _ := info.upcastFrom(u.from, decode(b))
		j1, _ := json.Marshal(out1.Interface())
		j2, _ := json.Marshal(out2.Interface())
		if !bytes.Equal(j1, j2) {
			ps = append(ps, fmt.Sprintf("event %q: upcasting stored version %d %s twice gave %s and %s: an upcaster is a pure function of the stored event (no clock, no randomness, no state)", info.name, u.from, b, j1, j2))
			break
		}
		if in, got := tagsOf(decode(b), u.inTags), tagsOf(out1, info.tags); !slices.Equal(in, got) {
			ps = append(ps, fmt.Sprintf("event %q: stored version %d %s has tags %v, its upcast to version %d %v: the store indexed it by the tags it was appended with, "+
				"so a query would disagree with a fold; an upcaster keeps every tag (key and value), and a tag that must change is a new event type", info.name, u.from, b, in, info.version, got))
			break
		}
	}
	if !fieldUse || len(ps) > 0 {
		return ps
	}
	for i := 0; i < u.in.NumField(); i++ {
		f := u.in.Field(i)
		name := rec.JSONName(f)
		if !f.IsExported() || name == "-" || slices.Contains(u.drops, name) {
			continue
		}
		used, perturbable := false, false
		for _, s := range samples {
			b, _ := json.Marshal(s)
			base, changed := decode(b), decode(b)
			if !values.Perturb(changed.Field(i)) {
				continue
			}
			perturbable = true
			o1, err1 := info.step(u, base)
			o2, err2 := info.step(u, changed)
			if err1 != nil || err2 != nil {
				used = true // a panic is the totality check's; the field reached the upcaster
				break
			}
			j1, _ := json.Marshal(o1.Interface())
			j2, _ := json.Marshal(o2.Interface())
			if !bytes.Equal(j1, j2) {
				used = true
				break
			}
		}
		if perturbable && !used {
			ps = append(ps, fmt.Sprintf("event %q: the upcaster from version %d ignores %s (%v.%s): every event stored at version %d loses it on the way to version %d; "+
				"copy it to the next version's field (renamed, converted), or say it is dropped on purpose: Upcast(%d, ...).Drops(%q)",
				info.name, u.from, name, u.in, f.Name, u.from, u.from+1, u.from, name))
		}
	}
	return ps
}

// step is one upcaster over val, its panic recovered.
func (info *eventInfo) step(u Upcaster, val reflect.Value) (out reflect.Value, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	return u.f(val), nil
}

// CheckUpcasters runs every upcaster of the App over generated values of
// the struct it takes (strings, integers and booleans at each field, a
// few of each, every combination up to 64): each must be total, pure and
// keep the event's tags (what Verify checks at boot), and every field of
// the old struct must reach the next version: perturbing it changes the
// upcaster's output for some value, unless the upcaster Drops it. It
// returns every problem found, naming the event, the version and the
// fix; nil when every upcaster passes. Main's verify and model commands
// and gristtest.Upcasts run it.
func (a *App) CheckUpcasters() error {
	ps := a.upcasterProblems(true)
	if len(ps) == 0 {
		return nil
	}
	return fmt.Errorf("grist: app %s's upcasters:\n  %s", a.Name, strings.Join(ps, "\n  "))
}
