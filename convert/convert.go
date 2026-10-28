// Package convert turns telemetry that was not written for gotogether, an
// application's ordinary OpenTelemetry spans and span events, into model
// steps (qtrace.Step), so existing traces can be checked against a Quint
// model without new instrumentation. A YAML rule file says which spans are
// which model actions and where each argument and observation comes from:
//
//	# bank.convert.yaml
//	rules:
//	  - span: "Bank/Deposit"            # span name; * and ? glob
//	    action: deposit
//	    args:
//	      account: attr:bank.account    # event, then span, then resource attribute
//	      amount: attr:bank.amount
//	    obs:
//	      balance: attr:bank.balance
//	  - span: "Bank/Withdraw"
//	    where: { rpc.system: grpc }     # attribute filters (string equality)
//	    action: withdraw
//	    args: { account: attr:bank.account, amount: attr:bank.amount }
//	    ref: attr:bank.receipt_url      # the step's link to its artifact (quint.ref)
//
// Each matched span (or span event, with `event:`) is one step at the
// span's end (or the event's time). The step's outcome is "error" when the
// span's status is ERROR, else "ok", unless `outcome:` says otherwise.
// Steps are grouped into recorders (`recorder:`, default the resource's
// service.instance.id, else service.name) and numbered densely in time
// order per recorder: converted traces carry no gap information, so a lost
// span is not detected the way a lost quint.step is.
//
// Value expressions:
//
//	attr:K      event attribute K, else span attribute, else resource attribute
//	event:K     span:K    resource:K       that layer only
//	name  trace_id  span_id  parent_span_id  status   (status is "ok" or "error")
//	=text       the literal text
//	expr|int    convert the value to an integer (also |string, |bool)
package convert

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"gopkg.in/yaml.v3"
)

// Rules is a parsed rule file.
type Rules struct {
	// Format, if set, names the rule file's format (FormatName,
	// gotogether.convert/1): a later major is refused with the fix, not
	// with its new keys "unknown". Empty: gotogether.convert/1.
	Format string `yaml:"format,omitempty"`
	// Recorder groups steps for numbering: an expression; default
	// resource:service.instance.id, else resource:service.name.
	Recorder string `yaml:"recorder"`
	// Defaults applied to every rule that does not set them.
	Actor   string `yaml:"actor"`
	Process string `yaml:"process"`
	Thread  string `yaml:"thread"`
	// Ref, an expression, is each step's link to its artifact
	// (qtrace.Step.Ref): "attr:app.screenshot", "=trace.jsonl". A span
	// (or matched event) carrying quint.ref keeps it when no rule sets one.
	Ref   string `yaml:"ref"`
	Rules []Rule `yaml:"rules"`
}

// Rule maps matching spans (or span events) to one model action.
type Rule struct {
	Span    string            `yaml:"span"`  // span name glob (required)
	Event   string            `yaml:"event"` // if set, match the span's events of this name instead of the span
	Where   map[string]string `yaml:"where"` // attribute filters: attr:K semantics, string equality ("*" = present)
	Action  string            `yaml:"action"`
	Actor   string            `yaml:"actor"`
	Process string            `yaml:"process"`
	Thread  string            `yaml:"thread"`
	Outcome string            `yaml:"outcome"`
	Ref     string            `yaml:"ref"` // the step's ref (Rules.Ref)
	Args    map[string]string `yaml:"args"`
	Obs     map[string]string `yaml:"obs"`
	Order   map[string]string `yaml:"order"`
	// Drop: matched spans produce no step (and stop later rules matching them).
	Drop bool `yaml:"drop"`
}

// FormatName is the rule file format this version reads and writes, the
// value of a rule file's optional format key.
const FormatName = "gotogether.convert/1"

// Load reads a rule file.
func Load(p string) (*Rules, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse parses rules from YAML.
func Parse(b []byte) (*Rules, error) {
	// the format first: a later file's keys are unknown here, and the
	// fix is a newer gotogether, not removing them
	var head struct {
		Format string `yaml:"format"`
	}
	if yaml.Unmarshal(b, &head) == nil && head.Format != "" {
		if _, err := formats.Convert.Check(head.Format); err != nil {
			return nil, err
		}
	}
	var r Rules
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		if strings.Contains(err.Error(), "not found in type") {
			return nil, fmt.Errorf("%w (this gotogether reads %s: a key another version of the format has is unknown here: write it in this version's, or upgrade gotogether for a later one's)", err, formats.Convert.Name())
		}
		return nil, err
	}
	for i, ru := range r.Rules {
		if ru.Span == "" {
			return nil, fmt.Errorf("rule %d: span is required", i)
		}
		if ru.Action == "" && !ru.Drop {
			return nil, fmt.Errorf("rule %d (%s): action is required", i, ru.Span)
		}
		if _, err := path.Match(ru.Span, ""); err != nil {
			return nil, fmt.Errorf("rule %d: bad span pattern %q: %w", i, ru.Span, err)
		}
	}
	return &r, nil
}

type ctx struct {
	res, span, event map[string]any
	sp               *otelio.Span
}

func (c *ctx) eval(expr string) (any, bool, error) {
	expr = strings.TrimSpace(expr)
	conv := ""
	if i := strings.LastIndexByte(expr, '|'); i >= 0 && !strings.HasPrefix(expr, "=") {
		expr, conv = strings.TrimSpace(expr[:i]), strings.TrimSpace(expr[i+1:])
	}
	v, ok := c.raw(expr)
	if !ok || conv == "" {
		return v, ok, nil
	}
	switch conv {
	case "int":
		switch x := v.(type) {
		case int64:
			return x, true, nil
		case float64:
			return int64(x), true, nil
		case bool:
			if x {
				return int64(1), true, nil
			}
			return int64(0), true, nil
		}
		n, err := strconv.ParseInt(fmt.Sprint(v), 10, 64)
		return n, true, err
	case "string":
		return fmt.Sprint(v), true, nil
	case "bool":
		b, err := strconv.ParseBool(fmt.Sprint(v))
		return b, true, err
	}
	return nil, false, fmt.Errorf("unknown conversion |%s", conv)
}

func (c *ctx) raw(expr string) (any, bool) {
	if strings.HasPrefix(expr, "=") {
		return expr[1:], true
	}
	k, v, has := strings.Cut(expr, ":")
	if !has {
		switch k {
		case "name":
			return c.sp.Name, true
		case "trace_id":
			return c.sp.TraceID, true
		case "span_id":
			return c.sp.SpanID, true
		case "parent_span_id":
			return c.sp.ParentSpanID, c.sp.ParentSpanID != ""
		case "status":
			if c.sp.Status.IsError() {
				return "error", true
			}
			return "ok", true
		}
		return nil, false
	}
	lookup := func(m map[string]any) (any, bool) { x, ok := m[v]; return x, ok }
	switch k {
	case "attr":
		for _, m := range []map[string]any{c.event, c.span, c.res} {
			if x, ok := lookup(m); ok {
				return x, true
			}
		}
	case "event":
		return lookup(c.event)
	case "span":
		return lookup(c.span)
	case "resource":
		return lookup(c.res)
	}
	return nil, false
}

func (c *ctx) str(expr, def string) (string, error) {
	if expr == "" {
		expr = def
	}
	if expr == "" {
		return "", nil
	}
	v, ok, err := c.eval(expr)
	if err != nil || !ok {
		return "", err
	}
	return fmt.Sprint(v), nil
}

func (r *Rule) matches(c *ctx) (bool, error) {
	for k, want := range r.Where {
		v, ok, err := c.eval(k)
		if !strings.Contains(k, ":") && err == nil && !ok {
			v, ok, err = c.eval("attr:" + k)
		}
		if err != nil {
			return false, err
		}
		if !ok || (want != "*" && fmt.Sprint(v) != want) {
			return false, nil
		}
	}
	return true, nil
}

// Steps converts OTLP/JSON documents to steps, in time order. Spans that no
// rule matches are ignored; quint.step events already in the input are
// passed through unchanged, so converted and natively recorded steps mix.
func (rs *Rules) Steps(tds []otelio.TracesData) ([]qtrace.Step, error) {
	type pending struct {
		s   qtrace.Step
		rec string
	}
	var ps []pending
	for _, td := range tds {
		native, err := otelio.StepsFromTracesData(&td)
		if err != nil {
			return nil, err
		}
		for _, s := range native {
			ps = append(ps, pending{s: s})
		}
		for _, rsp := range td.ResourceSpans {
			res := otelio.Attrs(rsp.Resource.Attributes)
			for _, ss := range rsp.ScopeSpans {
				for i := range ss.Spans {
					sp := &ss.Spans[i]
					spanAttrs := otelio.Attrs(sp.Attributes)
					for ri := range rs.Rules {
						ru := &rs.Rules[ri]
						if ok, _ := path.Match(ru.Span, sp.Name); !ok {
							continue
						}
						type occ struct {
							attrs map[string]any
							t     time.Time
							inv   time.Time // a span's start: the operation's real-time interval
						}
						var occs []occ
						if ru.Event == "" {
							occs = []occ{{map[string]any{}, time.Unix(0, int64(sp.EndTimeUnixNano)), time.Unix(0, int64(sp.StartTimeUnixNano))}}
						} else {
							for _, ev := range sp.Events {
								if ev.Name == ru.Event {
									occs = append(occs, occ{attrs: otelio.Attrs(ev.Attributes), t: time.Unix(0, int64(ev.TimeUnixNano))})
								}
							}
						}
						matched := false
						for _, o := range occs {
							c := &ctx{res: res, span: spanAttrs, event: o.attrs, sp: sp}
							ok, err := ru.matches(c)
							if err != nil {
								return nil, fmt.Errorf("rule %s: %w", ru.Span, err)
							}
							if !ok {
								continue
							}
							matched = true
							if ru.Drop {
								continue
							}
							s, rec, err := rs.step(ru, c, o.t)
							if sp.StartTimeUnixNano > 0 && !o.inv.IsZero() {
								s.Invoked = o.inv
							}
							if err != nil {
								return nil, fmt.Errorf("span %s (%s), rule %s: %w", sp.SpanID, sp.Name, ru.Action, err)
							}
							ps = append(ps, pending{s, rec})
						}
						if matched {
							break // first matching rule wins
						}
					}
				}
			}
		}
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].s.Time.Before(ps[j].s.Time) })
	seq := map[string]uint64{}
	out := make([]qtrace.Step, len(ps))
	for i, p := range ps {
		if p.s.Seq == 0 { // converted: number densely per recorder
			seq[p.rec]++
			p.s.Seq = seq[p.rec]
			p.s.Recorder = p.rec
		}
		out[i] = p.s
	}
	return out, nil
}

// Unmatched is a span name no rule matched, and how many spans of it:
// an operation the model does not name, or one outside its context (a
// `drop: true` rule says so, and is a match).
type Unmatched struct {
	Name  string
	Count int
}

// Unmatched lists, by name, the spans of tds that no rule matches (as
// Steps matches them: a rule's span glob, its where filters and, with
// `event:`, an event of that name), which Steps ignores. A span carrying
// quint.step events is recorded natively and never listed. Each is an
// operation to map with a rule, or to drop as outside the model's context.
func (rs *Rules) Unmatched(tds []otelio.TracesData) ([]Unmatched, error) {
	counts := map[string]int{}
	for _, td := range tds {
		for _, rsp := range td.ResourceSpans {
			res := otelio.Attrs(rsp.Resource.Attributes)
			for _, ss := range rsp.ScopeSpans {
				for i := range ss.Spans {
					sp := &ss.Spans[i]
					if slices.ContainsFunc(sp.Events, func(e otelio.Event) bool { return e.Name == qtrace.EventName }) {
						continue
					}
					ok, err := rs.matchesAny(sp, res)
					if err != nil {
						return nil, err
					}
					if !ok {
						counts[sp.Name]++
					}
				}
			}
		}
	}
	var out []Unmatched
	for _, n := range slices.Sorted(maps.Keys(counts)) {
		out = append(out, Unmatched{n, counts[n]})
	}
	return out, nil
}

// matchesAny reports whether some rule matches the span (or, for an
// event rule, one of its events).
func (rs *Rules) matchesAny(sp *otelio.Span, res map[string]any) (bool, error) {
	spanAttrs := otelio.Attrs(sp.Attributes)
	for ri := range rs.Rules {
		ru := &rs.Rules[ri]
		if ok, _ := path.Match(ru.Span, sp.Name); !ok {
			continue
		}
		evs := []map[string]any{{}}
		if ru.Event != "" {
			evs = nil
			for _, ev := range sp.Events {
				if ev.Name == ru.Event {
					evs = append(evs, otelio.Attrs(ev.Attributes))
				}
			}
		}
		for _, e := range evs {
			ok, err := ru.matches(&ctx{res: res, span: spanAttrs, event: e, sp: sp})
			if err != nil {
				return false, fmt.Errorf("rule %s: %w", ru.Span, err)
			}
			if ok {
				return true, nil
			}
		}
	}
	return false, nil
}

func (rs *Rules) step(ru *Rule, c *ctx, t time.Time) (qtrace.Step, string, error) {
	s := qtrace.Step{Action: ru.Action, Time: t, Args: map[string]any{}, Obs: map[string]any{}, Order: map[string]int64{},
		Source: qtrace.SpanSource(c.sp.SpanID, c.sp.TraceID)}
	var err error
	rec, err := c.str(rs.Recorder, "")
	if err != nil {
		return s, "", err
	}
	if rec == "" {
		if rec, _ = c.str("resource:service.instance.id", ""); rec == "" {
			if rec, _ = c.str("resource:service.name", ""); rec == "" {
				rec = "convert"
			}
		}
	}
	pick := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	if s.Actor, err = c.str(pick(ru.Actor, rs.Actor), ""); err != nil {
		return s, "", err
	}
	if s.Process, err = c.str(pick(ru.Process, rs.Process), ""); err != nil {
		return s, "", err
	}
	if s.Thread, err = c.str(pick(ru.Thread, rs.Thread), ""); err != nil {
		return s, "", err
	}
	if s.Outcome, err = c.str(ru.Outcome, "status"); err != nil {
		return s, "", err
	}
	if s.Ref, err = c.str(pick(ru.Ref, rs.Ref), ""); err != nil {
		return s, "", err
	}
	if s.Ref == "" && ru.Ref == "" && rs.Ref == "" { // the span's own, never the resource's (every step's)
		if s.Ref, _ = c.str("event:"+qtrace.KeyRef, ""); s.Ref == "" {
			s.Ref, _ = c.str("span:"+qtrace.KeyRef, "")
		}
	}
	if c.sp.Status.IsError() {
		s.Error = c.sp.Status.Message
		if s.Error == "" {
			s.Error = "span status ERROR"
		}
	}
	if s.Actor == "" {
		s.Actor = "default"
	}
	if s.Process == "" {
		s.Process = rec
	}
	for k, e := range ru.Args {
		v, ok, err := c.eval(e)
		if err != nil {
			return s, "", fmt.Errorf("arg %s: %w", k, err)
		}
		if !ok {
			return s, "", fmt.Errorf("arg %s: %s is not on the span", k, e)
		}
		s.Args[k] = v
	}
	for k, e := range ru.Obs {
		v, ok, err := c.eval(e)
		if err != nil {
			return s, "", fmt.Errorf("obs %s: %w", k, err)
		}
		if ok {
			s.Obs[k] = v
		}
	}
	for k, e := range ru.Order {
		if !strings.HasSuffix(e, "|int") {
			e += "|int" // a clock is an int; "attr:x|int" says so already
		}
		v, ok, err := c.eval(e)
		if err != nil {
			return s, "", fmt.Errorf("order %s: %w", k, err)
		}
		if ok {
			s.Order[k] = v.(int64)
		}
	}
	return s, rec, nil
}
