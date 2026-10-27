package binding

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

func TestJSONCollections(t *testing.T) {
	for _, c := range []struct{ in, kind, want string }{
		{`[3,1]`, "Set", "Set(3, 1)"},
		{`[[0,5],[1,7]]`, "List", "[(0, 5), (1, 7)]"},
		{`[]`, "List", "[]"},
		{`{"n2":4,"n1":3}`, "Map", `Map("n1" -> 3, "n2" -> 4)`},
		{`["a",true]`, "Set", `Set("a", true)`},
		// Quint has no escapes: a tab, a backslash, a newline as they are
		{`["a\tb","c\\d","e\nf"]`, "List", "[\"a\tb\", \"c\\d\", \"e\nf\"]"},
	} {
		got, err := jsonQuint(qstr(c.in), c.kind)
		if err != nil || got != c.want {
			t.Errorf("%s as %s: %q %v, want %q", c.in, c.kind, got, err, c.want)
		}
	}
	if _, err := jsonQuint(qstr(`nope`), "Set"); err == nil {
		t.Error("non-JSON accepted")
	}
	for _, c := range []struct{ in, kind, at string }{
		{`["say \"hi\""]`, "List", "at [0]"},
		{`[["a", "b\"c"]]`, "Set", "at [0][1]"},
		{`{"k":"v\""}`, "Map", `at ["k"]`},
		{`{"k\"":1}`, "Map", "at a key"},
	} {
		if _, err := jsonQuint(qstr(c.in), c.kind); err == nil || !strings.Contains(err.Error(), c.at+": ") || !strings.Contains(err.Error(), "a Quint string cannot") {
			t.Errorf("%s as %s: %v, want a quote refused %s", c.in, c.kind, err, c.at)
		}
	}
}

// A recorded string prints as Quint writes it: its characters between
// quotes, with no escapes (Go's %q wrote "a\tb" for a tab, which Quint
// reads as a backslash and a t, and \" for a quote, which ends Quint's
// literal). One with a double quote has no Quint literal: printing it
// fails, naming the field; toQuint refuses it, naming the path.
func TestQuintStringFields(t *testing.T) {
	s := &qtrace.Step{Action: "say", Args: map[string]any{"msg": "a\tb\\c\nd"}, Obs: map[string]any{"said": `say "hi"`}}
	data := stepData(s, s.Args, 1, "ok")
	if got, err := render(`{{.msg}}`, data, nil); err != nil || got != "\"a\tb\\c\nd\"" {
		t.Errorf("a tab, a backslash, a newline: %q, %v", got, err)
	}
	_, err := render(`said == {{.obs.said}}`, data, nil)
	if err == nil || !strings.Contains(err.Error(), "the step's field obs.said") || !strings.Contains(err.Error(), `"say \"hi\"" holds a double quote, which a Quint string cannot`) {
		t.Errorf("a quote printed: %v", err)
	}
	if _, err := toQuint(nil, "str", data["obs"].(map[string]any)["said"]); err == nil || !strings.Contains(err.Error(), "at the value: ") {
		t.Errorf("toQuint of a quote: %v", err)
	}
	if got, err := toQuint(nil, "List[str]", qstr(`["a\tb"]`)); err != nil || got != "[\"a\tb\"]" {
		t.Errorf("toQuint of a tab: %q, %v", got, err)
	}
	if got, err := toQuint(nil, "str", data["msg"]); err != nil || got != "\"a\tb\\c\nd\"" {
		t.Errorf("toQuint of a str: %q, %v", got, err)
	}
}
