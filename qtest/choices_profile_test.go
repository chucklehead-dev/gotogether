package qtest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
)

func TestChoicesValidationProfileRetainsEffectiveLimit(t *testing.T) {
	for _, limit := range []int{0, 7} {
		p := validationProfile(settings{Options: validate.Options{ChoiceLimit: limit}})
		want := limit
		if want == 0 {
			want = validate.DefaultChoiceLimit
		}
		if p.ChoiceLimit != want {
			t.Fatalf("effective limit: %+v", p)
		}
		b, err := json.Marshal(p)
		if err != nil || !strings.Contains(string(b), `"choiceLimit":`) {
			t.Fatalf("profile JSON: %s %v", b, err)
		}
	}
}

func TestChoicesResolvedValidationNormalizesPolicy(t *testing.T) {
	cleanProfileEnv(t)
	c := settings{}
	if err := resolveValidation(&c); err != nil {
		t.Fatal(err)
	}
	if c.Options.ChoiceLimit != validate.DefaultChoiceLimit || validationProfile(c).ChoiceLimit != c.Options.ChoiceLimit {
		t.Fatalf("resolved options and profile disagree: %+v", c.Options)
	}
	c = settings{Options: validate.Options{ChoiceLimit: -1}}
	if err := resolveValidation(&c); err == nil {
		t.Fatal("invalid search policy accepted before execution")
	}
}
