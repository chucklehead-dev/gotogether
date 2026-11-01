package receipt

import (
	"reflect"
	"slices"
)

// Clone snapshots the composition and its nested receipt storage. The result
// shares no mutable receipt containers with the source. Assumed-effect argument
// containers retain their concrete Go types and integer precision; opaque values
// such as functions and unexported object state are retained as values.
func (c *Composition) Clone() *Composition {
	if c == nil {
		return nil
	}
	out := *c
	arguments := argumentCloner{seen: map[argumentRef]reflect.Value{}}
	cloneHistory := func(j *History) *History {
		if j == nil {
			return nil
		}
		copy := *j
		copy.PartitionInstances = make([]PartitionInstance, len(j.PartitionInstances))
		for i := range j.PartitionInstances {
			copy.PartitionInstances[i] = *j.PartitionInstances[i].Clone()
		}
		copy.Actors = slices.Clone(j.Actors)
		for i := range copy.Actors {
			actor := &copy.Actors[i]
			actor.PartitionInfo = actor.PartitionInfo.Clone()
			actor.Checks = slices.Clone(actor.Checks)
			if actor.Search != nil {
				search := *actor.Search
				actor.Search = &search
			}
			actor.Choices = actor.Choices.Clone()
			actor.AssumedEffects = slices.Clone(actor.AssumedEffects)
			for k := range actor.AssumedEffects {
				if actor.AssumedEffects[k].Args != nil {
					actor.AssumedEffects[k].Args = arguments.clone(reflect.ValueOf(actor.AssumedEffects[k].Args)).Interface().(map[string]any)
				}
			}
			actor.Diagnostics = slices.Clone(actor.Diagnostics)
			for k := range actor.Diagnostics {
				diagnostic := &actor.Diagnostics[k]
				diagnostic.Items = slices.Clone(diagnostic.Items)
				for l := range diagnostic.Items {
					diagnostic.Items[l].Evidence.Matches = slices.Clone(diagnostic.Items[l].Evidence.Matches)
				}
			}
		}
		return &copy
	}
	out.Local = slices.Clone(c.Local)
	for i := range out.Local {
		out.Local[i].Judgment = cloneHistory(out.Local[i].Judgment)
	}
	out.Peers = slices.Clone(c.Peers)
	for i := range out.Peers {
		out.Peers[i].Judgment = cloneHistory(out.Peers[i].Judgment)
	}
	out.Protocol.Judgment = cloneHistory(c.Protocol.Judgment)
	out.Protocol.Counterexample = slices.Clone(c.Protocol.Counterexample)
	return &out
}

// argumentRef tracks typed container identity so cycles terminate and a repeated
// argument container remains shared inside the independent snapshot.
type argumentRef struct {
	kind    reflect.Kind
	typ     reflect.Type
	pointer uintptr
	length  int
}

type argumentCloner struct{ seen map[argumentRef]reflect.Value }

func (c argumentCloner) clone(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(c.clone(v.Elem()))
		return out
	case reflect.Map, reflect.Slice, reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		ref := argumentRef{kind: v.Kind(), typ: v.Type(), pointer: uintptr(v.UnsafePointer())}
		if v.Kind() == reflect.Slice {
			ref.length = v.Len()
		}
		if out, exists := c.seen[ref]; exists {
			return out
		}
		switch v.Kind() {
		case reflect.Map:
			out := reflect.MakeMapWithSize(v.Type(), v.Len())
			c.seen[ref] = out
			it := v.MapRange()
			for it.Next() {
				out.SetMapIndex(it.Key(), c.clone(it.Value()))
			}
			return out
		case reflect.Slice:
			out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			c.seen[ref] = out
			for i := 0; i < v.Len(); i++ {
				out.Index(i).Set(c.clone(v.Index(i)))
			}
			return out
		default:
			out := reflect.New(v.Type().Elem()).Convert(v.Type())
			c.seen[ref] = out
			out.Elem().Set(c.clone(v.Elem()))
			return out
		}
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(c.clone(v.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath == "" {
				out.Field(i).Set(c.clone(v.Field(i)))
			}
		}
		return out
	default:
		return v
	}
}
