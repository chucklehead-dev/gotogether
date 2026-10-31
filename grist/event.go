package grist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
)

// Tag is the tag key=value, as a query names it: Exists(Created,
// Tag("name", n)). The key is a tagged field's JSON name; a read refuses
// a key the event type has no tag of.
func Tag(key string, value any) TagPair { return TagPair{Key: key, Value: fmt.Sprint(value)} }

// eventInfo is a registered event type.
type eventInfo struct {
	name    string
	version int
	typ     reflect.Type
	tags    []tagField
	// ups are the upcasters, ups[k-1] from version k to k+1 (nil when
	// the chain does not verify: Verify says why)
	ups []Upcaster
	// writes is the version an append writes, through its downcaster
	// (WritesVersion); nil: the newest
	writes *downcaster
}

type tagField struct {
	index int
	key   string
}

// hasTag reports whether the event type has a tag keyed key.
func (i *eventInfo) hasTag(key string) bool {
	for _, t := range i.tags {
		if t.key == key {
			return true
		}
	}
	return false
}

// tagKeys are the event type's tag keys, for an error's fix.
func (i *eventInfo) tagKeys() []string {
	out := []string{}
	for _, t := range i.tags {
		out = append(out, t.key)
	}
	return out
}

// EventType is a registered event type whose body is E.
type EventType[E any] struct {
	info *eventInfo
}

// NewEvent registers E as the event type name. The name, not the Go
// type's, is what is stored. Fields marked `grist:"tag"` (strings,
// integers, booleans) are its tags, keyed by their JSON names. E must be
// a struct whose fields the model can hold: floats and times are refused,
// with the fix (integers in a stated unit).
//
// Its version is 1, or 1 plus its upcasters: an event whose shape changed
// keeps its old struct (BookAddedV1) and declares how a stored event of it
// becomes the next version's, Upcast(1, func(v BookAddedV1) BookAdded {...}),
// one upcaster per old version, each to the next (docs/grist.md §2.1). A
// read upcasts before any fold, handler or rule sees the event; an append
// writes the newest version only; a stored version newer than E's is
// refused by name (a *NewerVersionError), never decoded.
//
// A release that reads a new version while nodes of the previous one
// still run (a rolling update) writes the older version until they are
// gone: WritesVersion(n, downcaster), removed in the next release
// (docs/grist.md §2.1, readers first).
func NewEvent[E any](app *App, name string, opts ...EventOption) *EventType[E] {
	typ := reflect.TypeFor[E]()
	app.register("event", name)
	info := &eventInfo{name: name, version: 1, typ: typ}
	if typ.Kind() != reflect.Struct {
		app.problems = append(app.problems, fmt.Sprintf("event %q: %v is no struct: an event is a struct of named fields", name, typ))
	} else {
		for _, p := range modelable(typ, typ.Name()) {
			app.problems = append(app.problems, fmt.Sprintf("event %q: %s", name, p))
		}
		var ps []string
		info.tags, ps = tagFieldsOf(typ)
		for _, p := range ps {
			app.problems = append(app.problems, fmt.Sprintf("event %q: %s", name, p))
		}
	}
	var upcasters []Upcaster
	var writes []*downcaster
	for _, o := range opts {
		switch o := o.(type) {
		case Upcaster:
			upcasters = append(upcasters, o)
		case *downcaster:
			writes = append(writes, o)
		}
	}
	app.problems = append(app.problems, info.chain(upcasters)...)
	app.problems = append(app.problems, info.writesProblems(writes)...)
	if app.events[name] == nil {
		app.events[name] = info
		app.eventOrder = append(app.eventOrder, name)
	}
	return &EventType[E]{info: info}
}

// tagFieldsOf are a struct's tag fields (marked grist:"tag"), and why a
// marked field cannot be one.
func tagFieldsOf(typ reflect.Type) (tags []tagField, problems []string) {
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Tag.Get("grist") != "tag" {
			continue
		}
		switch f.Type.Kind() {
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		default:
			problems = append(problems, fmt.Sprintf("tag field %s is %v: a tag is a string, an integer or a boolean", f.Name, f.Type))
			continue
		}
		tags = append(tags, tagField{index: i, key: rec.JSONName(f)})
	}
	return tags, problems
}

// Name is the event type's registered name.
func (t *EventType[E]) Name() string { return t.info.name }

// Of is e as an event to append.
func (t *EventType[E]) Of(e E) Event {
	body, err := json.Marshal(e)
	if err != nil {
		panic(fmt.Sprintf("grist: event %s: %v", t.info.name, err)) // a registered struct of modelable fields always encodes
	}
	v := reflect.ValueOf(e)
	tags := make([]TagPair, 0, len(t.info.tags))
	for _, tf := range t.info.tags {
		tags = append(tags, TagPair{Key: tf.key, Value: fmt.Sprint(v.Field(tf.index).Interface())})
	}
	return Event{Type: t.info.name, Version: t.info.version, Tags: tags, Body: body}
}

// Version is the event type's newest version: what reads upcast to, and
// what an append writes unless WritesVersion holds it back (Writes).
func (t *EventType[E]) Version() int { return t.info.version }

// Writes is the version an append of the event type writes: its newest,
// or WritesVersion's while a rollout lasts.
func (t *EventType[E]) Writes() int { return t.info.writesVersion() }

// Decode is a stored event's body as E: decoded as the version it was
// stored at and upcast to the newest (NewEvent's upcasters). A version
// newer than E's is a *NewerVersionError: a newer version of the app
// appended it, and this one cannot know what its fields mean.
func (t *EventType[E]) Decode(s Stored) (E, error) {
	var e E
	if s.Type != t.info.name {
		return e, fmt.Errorf("grist: event at %d is %s, not %s", s.Pos, s.Type, t.info.name)
	}
	if v := storedVersion(s.Version); v == t.info.version {
		err := json.Unmarshal(s.Body, &e)
		return e, err
	}
	v, err := t.info.upcast(s.Event)
	if err != nil {
		return e, fmt.Errorf("grist: event at %d: %w", s.Pos, err)
	}
	return v.Interface().(E), nil
}

// An EventName is a registered event type (an *EventType, whatever its
// body): what Exists takes. Its method is unexported, so a read model, a
// command or a processor, which have names too, are not event names.
type EventName interface {
	Name() string
	registered() *eventInfo
}

func (t *EventType[E]) registered() *eventInfo { return t.info }

// modelable lists why typ holds a value the model cannot: a float or a
// time, at path.
func modelable(typ reflect.Type, path string) []string {
	if typ == reflect.TypeFor[time.Time]() {
		return []string{fmt.Sprintf("%s is a time.Time: store Unix milliseconds as an int64 (grist.Meta carries the append's time)", path)}
	}
	switch typ.Kind() {
	case reflect.Float32, reflect.Float64:
		return []string{fmt.Sprintf("%s is a %v: the model holds integers; store an integer in a stated unit (cents, basis points)", path, typ)}
	case reflect.Struct:
		var out []string
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.IsExported() {
				out = append(out, modelable(f.Type, path+"."+f.Name)...)
			}
		}
		return out
	case reflect.Slice, reflect.Array, reflect.Pointer:
		return modelable(typ.Elem(), path+"[]")
	case reflect.Map:
		return append(modelable(typ.Key(), path+"[key]"), modelable(typ.Elem(), path+"[]")...)
	case reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func, reflect.Interface, reflect.UnsafePointer:
		return []string{fmt.Sprintf("%s is a %v, which the model cannot hold", path, typ)}
	}
	return nil
}

// scalars are the top-level scalar fields of a JSON-encoded struct of
// type typ, in field order: strings, then integers (booleans as 0 and 1).
// Slice 1's recording names an event's values so (obs.strs, obs.ints):
// its binding takes scalars and toList only (docs/grist.md §3.2).
func scalars(typ reflect.Type, body []byte) (strs []string, ints []int64, err error) {
	strs, ints = []string{}, []int64{}
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("event observations require a registered struct shape")
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return nil, nil, fmt.Errorf("event body must be a JSON object, not null or a scalar")
	}
	if _, err := rec.StepFields(typ, body, ""); err != nil {
		return nil, nil, err
	}
	v := reflect.New(typ)
	if err := json.Unmarshal(body, v.Interface()); err != nil {
		return nil, nil, err
	}
	v = v.Elem()
	for i := 0; i < typ.NumField(); i++ {
		if !typ.Field(i).IsExported() {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			strs = append(strs, f.String())
		case reflect.Bool:
			if f.Bool() {
				ints = append(ints, 1)
			} else {
				ints = append(ints, 0)
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			ints = append(ints, f.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			u := f.Uint()
			if u > math.MaxInt64 {
				return nil, nil, fmt.Errorf("field %s is %d, past the model's integers (int64): give a value at most %d, or make the field an int64 (a string for an identifier)", rec.JSONName(typ.Field(i)), u, int64(math.MaxInt64))
			}
			ints = append(ints, int64(u))
		}
	}
	return
}

// failedType is the event a processor appends when a trigger's handling
// failed every try: it names the processor and the trigger.
const failedType = "grist.failed"

// Failed is the body of a grist.failed event. The failure's text is not
// in it (the model could not compute it): it is in the effect's steps.
// Its cause, like every processor event's, is "processor:<name>".
type Failed struct {
	Processor string `json:"processor" grist:"tag"`
	Trigger   int64  `json:"trigger"`
}

func failedInfo() *eventInfo {
	return &eventInfo{name: failedType, version: 1, typ: reflect.TypeFor[Failed](), tags: []tagField{{index: 0, key: "processor"}}}
}

// unknownType is the event an at-most-once effect's outcome is when it
// is unknown: f returned an error that does not say the effect did not
// happen (NotApplied), so it may have (a payment whose answer was lost).
// It names the processor, the trigger and f's error, for the
// reconciliation an unknown outcome needs.
const unknownType = "grist.unknown"

// Unknown is the body of a grist.unknown event: its effect's error text
// is in it (the outcome step records it, so the model computes it).
type Unknown struct {
	Processor string `json:"processor" grist:"tag"`
	Error     string `json:"error"`
	Trigger   int64  `json:"trigger"`
}

func unknownInfo() *eventInfo {
	return &eventInfo{name: unknownType, version: 1, typ: reflect.TypeFor[Unknown](), tags: []tagField{{index: 0, key: "processor"}}}
}

// unknownEvent is the grist.unknown event for processor's trigger, whose
// effect returned err.
func unknownEvent(processor string, trigger Position, err string) Event {
	body, _ := json.Marshal(Unknown{Processor: processor, Error: err, Trigger: int64(trigger)})
	return Event{Type: unknownType, Version: 1, Tags: []TagPair{{Key: "processor", Value: processor}}, Body: body}
}

// failedEvent is the grist.failed event for processor's trigger.
func failedEvent(processor string, trigger Position) Event {
	f := Failed{Processor: processor, Trigger: int64(trigger)}
	body, _ := json.Marshal(f)
	return Event{Type: failedType, Version: 1, Tags: []TagPair{{Key: "processor", Value: processor}}, Body: body}
}

// storedValue reads the stored shape and applies only the registered links up
// to target. Rows from a rolling writer can already describe a newer shape than
// the event on disk, while an old row may still describe its original shape.
func (info *eventInfo) storedValue(e Event, target int) (v reflect.Value, typ reflect.Type, tags []tagField, err error) {
	version := storedVersion(e.Version)
	if version > info.version || target > info.version {
		return v, nil, nil, &NewerVersionError{Type: info.name, Version: max(version, target), Newest: info.version}
	}
	if version < 1 || target < version {
		return v, nil, nil, fmt.Errorf("cannot render stored version %d as version %d", version, target)
	}
	body := bytes.TrimSpace(e.Body)
	if len(body) == 0 || body[0] != '{' {
		return v, nil, nil, fmt.Errorf("event body must be a JSON object, not null or a scalar")
	}
	typ, tags = info.typ, info.tags
	if version < info.version {
		if version > len(info.ups) {
			return v, nil, nil, fmt.Errorf("version %d has no registered upcaster", version)
		}
		typ, tags = info.ups[version-1].in, info.ups[version-1].inTags
	}
	p := reflect.New(typ)
	if err = json.Unmarshal(body, p.Interface()); err != nil {
		return v, nil, nil, err
	}
	v = p.Elem()
	if err := storedScalarFields(v); err != nil {
		return v, nil, nil, fmt.Errorf("version %d scalar observations: %w", version, err)
	}
	link := version
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("upcaster of %s from version %d panicked: %v", info.name, link, value)
		}
	}()
	for ; link < target; link++ {
		if link > len(info.ups) {
			return v, nil, nil, fmt.Errorf("version %d has no registered upcaster", link)
		}
		v = info.ups[link-1].f(v)
		if err := storedScalarFields(v); err != nil {
			return v, nil, nil, fmt.Errorf("version %d scalar observations: %w", link+1, err)
		}
	}
	typ = v.Type()
	tags = info.tags
	if target < info.version {
		tags = info.ups[target-1].inTags
	}
	return v, typ, tags, nil
}

// storedScalarFields checks every stored/upcast shape with the shared
// projection rules before the next registered link can discard its fields:
// wrapping a uint64 into a negative observation is not faithful model evidence,
// even if a later upcaster would discard that intermediate value.
func storedScalarFields(v reflect.Value) error {
	body, err := json.Marshal(v.Interface())
	if err != nil {
		return err
	}
	_, _, err = scalars(v.Type(), body)
	return err
}

// checkEventObservations validates emitted shapes without converting them to
// a rollout's stored version: business rules must still see the newest body.
func (a *App) checkEventObservations(block string, events []Event) error {
	for _, e := range events {
		info := a.events[e.Type]
		if info == nil {
			return fmt.Errorf("grist: %s emits unregistered event %s", block, e.Type)
		}
		if _, _, _, err := info.storedValue(e, info.version); err != nil {
			return fmt.Errorf("grist: %s's event %s observations: %w", block, e.Type, err)
		}
	}
	return nil
}
