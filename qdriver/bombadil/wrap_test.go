package bombadil

import (
	"encoding/json"
	"errors"
	"testing"
)

// A malformed entry is reported with the decoder's error wrapped.
func TestMalformedEntryWrapsTheDecodersError(t *testing.T) {
	_, err := read(t, "{not json\n", line(2000, nil, page("", "")))
	if se, ok := errors.AsType[*json.SyntaxError](err); !ok || se.Offset == 0 {
		t.Errorf("%v, want a wrapped *json.SyntaxError", err)
	}
}
