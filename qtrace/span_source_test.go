package qtrace

import "testing"

func TestSpanSource(t *testing.T) {
	for _, tc := range []struct {
		traceID string
		want    string
	}{{"", "span span-id"}, {"trace-id", "span span-id trace trace-id"}} {
		t.Run(tc.want, func(t *testing.T) {
			if got := SpanSource("span-id", tc.traceID); got != tc.want {
				t.Fatalf("trace %q: got %q, want %q", tc.traceID, got, tc.want)
			}
		})
	}
}
