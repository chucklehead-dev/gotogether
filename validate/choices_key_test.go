package validate

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
)

func TestChoicesKeyIncludesEffectiveSearchLimit(t *testing.T) {
	c := &Checker{Binding: &binding.Binding{}}
	defaultKey := c.Key(nil)
	c.Options.ChoiceLimit = DefaultChoiceLimit
	if c.Key(nil) != defaultKey {
		t.Fatal("implicit and explicit default limits have different cache keys")
	}
	c.Options.ChoiceLimit = 1
	if c.Key(nil) == defaultKey {
		t.Fatal("an exhausted search can share a cached verdict with a larger search")
	}
}
