package grist

import (
	"encoding/json"
	"fmt"
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

// NewEvent registers E as the event type name, version 1 (slice 1 has one
// version per name; upcasting is slice 3's). The name, not the Go type's,
// is what is stored. Fields marked `grist:"tag"` (strings, integers,
// booleans) are its tags, keyed by their JSON names. E must be a struct
// whose fields the model can hold: floats and times are refused, with
// the fix (integers in a stated unit).
func NewEvent[E any](app *App, name string) *EventType[E] {
	typ := reflect.TypeFor[E]()
	app.register("event", name)
	info := &eventInfo{name: name, version: 1, typ: typ}
	if typ.Kind() != reflect.Struct {
		app.problems = append(app.problems, fmt.Sprintf("event %q: %v is no struct: an event is a struct of named fields", name, typ))
	} else {
		for _, p := range modelable(typ, typ.Name()) {
			app.problems = append(app.problems, fmt.Sprintf("event %q: %s", name, p))
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.Tag.Get("grist") != "tag" {
				continue
			}
			switch f.Type.Kind() {
			case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			default:
				app.problems = append(app.problems, fmt.Sprintf("event %q: tag field %s is %v: a tag is a string, an integer or a boolean", name, f.Name, f.Type))
				continue
			}
			info.tags = append(info.tags, tagField{index: i, key: rec.JSONName(f)})
		}
	}
	if app.events[name] == nil {
		app.events[name] = info
		app.eventOrder = append(app.eventOrder, name)
	}
	return &EventType[E]{info: info}
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

// Decode is a stored event's body as E.
func (t *EventType[E]) Decode(s Stored) (E, error) {
	var e E
	if s.Type != t.info.name {
		return e, fmt.Errorf("grist: event at %d is %s, not %s", s.Pos, s.Type, t.info.name)
	}
	err := json.Unmarshal(s.Body, &e)
	return e, err
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
func scalars(typ reflect.Type, body []byte) (strs []string, ints []int64) {
	strs, ints = []string{}, []int64{}
	if typ == nil || typ.Kind() != reflect.Struct {
		return
	}
	v := reflect.New(typ)
	if json.Unmarshal(body, v.Interface()) != nil {
		return
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
			ints = append(ints, int64(f.Uint()))
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

// failedEvent is the grist.failed event for processor's trigger.
func failedEvent(processor string, trigger Position) Event {
	f := Failed{Processor: processor, Trigger: int64(trigger)}
	body, _ := json.Marshal(f)
	return Event{Type: failedType, Version: 1, Tags: []TagPair{{Key: "processor", Value: processor}}, Body: body}
}
