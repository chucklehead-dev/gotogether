package grist

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A Site is a dependency's own record of an effect, made where the
// effect takes place: a bank's payment, a ledger's post, recorded by the
// double or adapter that makes it, not by the processor that asked. An
// effect step says what the processor saw; a site record says what
// happened, so a payment made twice inside one call, or made while its
// answer was lost, is in the trace whatever the caller saw (docs/grist.md
// §3.1). Its step's action is the site's Quint name, its actor the
// tenant and its process the App's (the store's), so it joins the
// tenant's trace beside the commits; its fields are T's scalar fields by
// JSON name. The model generator (Main's model command) writes its variable (a list
// of T's records) and its action, guarded by the app's <site>Admits.
type Site[T any] struct {
	app  *App
	info *siteInfo
}

// siteInfo is a site's registration, untyped.
type siteInfo struct {
	name, quint string
	typ         reflect.Type
}

// NewSite registers a site of records T, a struct of the fields the
// model compares (strings, integers, booleans): the tenant is the
// record's actor, not one of them.
func NewSite[T any](app *App, name string) *Site[T] {
	app.register("site", name)
	q, _ := catalog.QuintName(name)
	info := &siteInfo{name: name, quint: q, typ: reflect.TypeFor[T]()}
	if info.typ.Kind() != reflect.Struct {
		app.problems = append(app.problems, fmt.Sprintf("site %q: %v is no struct: a site record is a struct of named fields", name, info.typ))
	} else {
		for _, p := range modelable(info.typ, info.typ.Name()) {
			app.problems = append(app.problems, fmt.Sprintf("site %q: %s", name, p))
		}
	}
	app.sites = append(app.sites, info)
	return &Site[T]{app: app, info: info}
}

// Name is the site's name.
func (s *Site[T]) Name() string { return s.info.name }

// Record records v as the site's step of tenant, to ctx's recorder (in
// a simulation, the run's: a double's handler's context is its
// service's task's): actor tenant, process the App's name.
func (s *Site[T]) Record(ctx context.Context, tenant string, v T) {
	body, err := json.Marshal(v)
	var kv []any
	if err == nil {
		kv, err = rec.StepFields(s.info.typ, body, "")
	}
	qobs.Record(ctx, s.info.quint, err, append([]any{"actor", tenant, "process", s.app.Name}, kv...)...)
}
