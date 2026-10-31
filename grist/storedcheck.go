package grist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// CheckStoredBatch checks that a retained committed batch is readable by this
// App and that its recorded event observations describe its actual events.
// Every event is decoded and upcast, including types no read model consumes.
// Step rows must be present and account for every event exactly once. Recorded
// scalar observations are checked in their recorded version, before a relay
// renders them for today's model. No commands, processors or effects run.
//
// This checks the supplied batch, not whether a fixture omitted other batches
// or whether today's business folds preserve an older release's meaning.
// Call Verify first; the error names an event or step within this batch, so a
// caller retaining several batches should add its fixture, tenant and index.
func (a *App) CheckStoredBatch(b store.Batch) error {
	if a == nil {
		return fmt.Errorf("grist: checking stored batch: nil App")
	}
	for i, e := range b.Events {
		info := a.events[e.Type]
		if info == nil {
			return fmt.Errorf("grist: stored event %d type %q version %d is not registered on %s", i+1, e.Type, e.Version, a.Name)
		}
		v, _, tags, err := info.storedValue(e, storedVersion(e.Version))
		if err == nil {
			err = checkStoredTags(e.Tags, tagsOf(v, tags))
		}
		if err == nil && storedVersion(e.Version) < info.version {
			v, _, _, err = info.storedValue(e, info.version)
			if err == nil {
				err = checkStoredTags(e.Tags, tagsOf(v, info.tags))
			}
		}
		if err != nil {
			return fmt.Errorf("grist: stored event %d type %s version %d: %w", i+1, e.Type, e.Version, err)
		}
	}
	if len(bytes.TrimSpace(b.Step)) == 0 {
		return fmt.Errorf("grist: stored batch has no step row: model evidence is missing")
	}
	// Use the runtime payload type and decoder. Head/time below only provide
	// structural decoder context; they are not recorded or presented as evidence.
	var row stepRow
	d := json.NewDecoder(bytes.NewReader(b.Step))
	d.UseNumber()
	if err := d.Decode(&row); err != nil {
		return fmt.Errorf("grist: stored batch step row: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("grist: stored batch step row must contain exactly one JSON object")
	}
	if len(row.Steps) == 0 {
		return fmt.Errorf("grist: stored batch step row contains no steps: model evidence is missing")
	}
	total := 0
	for i, s := range row.Steps {
		if strings.TrimSpace(s.Action) == "" {
			return fmt.Errorf("grist: stored step %d has no action", i+1)
		}
		if s.Events < 0 || s.Events > len(b.Events)-total {
			return fmt.Errorf("grist: stored step %d event count %d does not fit the batch's %d events", i+1, s.Events, len(b.Events))
		}
		total += s.Events
		seen := map[string]bool{}
		for _, pair := range s.KV {
			if len(pair) != 2 {
				return fmt.Errorf("grist: stored step %d has a field that is not a key/value pair", i+1)
			}
			key, ok := pair[0].(string)
			if !ok || key == "" || seen[key] || storedRelayControl(key) {
				return fmt.Errorf("grist: stored step %d has an invalid, repeated or relay-owned field key %v", i+1, pair[0])
			}
			seen[key] = true
		}
	}
	if total != len(b.Events) {
		return fmt.Errorf("grist: stored step row accounts for %d events, but the batch contains %d", total, len(b.Events))
	}
	steps, err := decodeRow(store.StepRow{Step: b.Step, Head: store.Position(len(b.Events)), Time: 1})
	if err != nil {
		return fmt.Errorf("grist: stored batch step row: %w", err)
	}
	at := 0
	for i, s := range steps {
		if err := a.checkStoredObservations(s, b.Events[at:at+s.events], b.Cause); err != nil {
			return fmt.Errorf("grist: stored step %d action %s: %w", i+1, s.action, err)
		}
		at += s.events
	}
	return nil
}

// A retained row supplies observations and arguments, never the recorder's
// routing/filter controls. Otherwise a row could look covered while a relay
// redirects it to another actor or suppresses it altogether.
func storedRelayControl(key string) bool {
	switch key {
	case "actor", "process", "thread", "when", "invoked", "obs.head", "order.pos":
		return true
	}
	return false
}

func checkStoredTags(got, want []TagPair) error {
	got = slices.Clone(got)
	slices.SortFunc(got, func(a, b TagPair) int { return strings.Compare(a.Key, b.Key) })
	if !slices.Equal(got, want) {
		return fmt.Errorf("stored tags %v disagree with decoded body tags %v", got, want)
	}
	return nil
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

// scalars is the runtime observation projection, whose integer result is int64.
// Validate each stored/upcast shape with the recorder's exact scalar rules first:
// wrapping a uint64 into a negative observation is not faithful model evidence,
// even if a later upcaster would discard that intermediate value.
func storedScalarFields(v reflect.Value) error {
	body, err := json.Marshal(v.Interface())
	if err != nil {
		return err
	}
	_, err = rec.StepFields(v.Type(), body, "")
	return err
}

func (a *App) checkStoredObservations(s relayStep, events []Event, cause string) error {
	fields := map[string]any{}
	for i := 0; i+1 < len(s.kv); i += 2 {
		fields[s.kv[i].(string)] = s.kv[i+1]
	}
	// Resume is the one framework row with no event observations at all.
	if s.action == "gristResumed" && len(events) == 0 {
		noObservations := true
		for _, key := range []string{"obs.events", "obs.strs", "obs.ints", "obs.causes", renderedKey} {
			_, exists := fields[key]
			noObservations = noObservations && !exists
		}
		if noObservations {
			return nil
		}
	}
	versions := make([]int, len(events))
	if value, exists := fields[renderedKey]; exists {
		list, ok := value.([]any)
		if !ok || len(list) != len(events) {
			return fmt.Errorf("%s must name one rendering version per event", renderedKey)
		}
		for i, raw := range list {
			n, ok := raw.(json.Number)
			if !ok {
				return fmt.Errorf("%s version %d is not an integer", renderedKey, i+1)
			}
			k, err := n.Int64()
			if err != nil || k < 1 || k > int64(a.events[events[i].Type].version) {
				return fmt.Errorf("%s version %v is outside this app's registered versions", renderedKey, raw)
			}
			versions[i] = int(k)
		}
	}
	types, strs, ints, causes := []string{}, []string{}, []int64{}, [][]string{}
	kind, name := "command", cause
	if strings.HasPrefix(cause, "processor:") {
		kind, name = "processor", strings.TrimPrefix(cause, "processor:")
	}
	for i, e := range events {
		version := versions[i]
		if version == 0 {
			version = storedVersion(e.Version)
		}
		v, typ, _, err := a.events[e.Type].storedValue(e, version)
		if err != nil {
			return fmt.Errorf("event %d (%s) observations: %w", i+1, e.Type, err)
		}
		body, err := json.Marshal(v.Interface())
		if err != nil {
			return err
		}
		ss, ns := scalars(typ, body)
		types, strs, ints, causes = append(types, e.Type), append(strs, ss...), append(ints, ns...), append(causes, []string{kind, name})
	}
	for _, observation := range []struct {
		key  string
		want any
	}{{"obs.events", types}, {"obs.strs", strs}, {"obs.ints", ints}, {"obs.causes", causes}} {
		key, want := observation.key, observation.want
		got, exists := fields[key]
		if !exists {
			return fmt.Errorf("missing %s observation", key)
		}
		// Compare JSON representations: decoded numbers and slices have their
		// wire types, while the framework's expected values have Go types.
		g, gerr := json.Marshal(got)
		w, werr := json.Marshal(want)
		if gerr != nil || werr != nil || !bytes.Equal(g, w) {
			return fmt.Errorf("%s does not describe the batch's stored events (got %s, want %s)", key, g, w)
		}
	}
	return nil
}
