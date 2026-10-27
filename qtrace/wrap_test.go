package qtrace_test

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A step's malformed number is reported with its cause wrapped, so a
// caller tells a syntax error by errors.Is.
func TestFromAttributesWrapsItsCause(t *testing.T) {
	for _, attrs := range []map[string]any{
		{qtrace.KeyInvoked: "soon"},
		{qtrace.PrefixOrder + "lamport": "soon"},
		{qtrace.KeyLost: "suffix", qtrace.KeyLostBefore: "soon"},
	} {
		attrs[qtrace.KeyAction] = "deposit"
		_, err := qtrace.FromAttributes(time.Unix(0, 1), attrs)
		if !errors.Is(err, strconv.ErrSyntax) {
			t.Errorf("%v: %v, want a wrapped strconv.ErrSyntax", attrs, err)
		}
	}
}
