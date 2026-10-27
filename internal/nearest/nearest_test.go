package nearest

import "testing"

func TestName(t *testing.T) {
	for _, c := range []struct {
		name  string
		names []string
		want  string
	}{
		{"nonNegativ", []string{"underLimit", "nonNegative"}, "nonNegative"},
		{"withdrw", []string{"deposit", "withdraw"}, "withdraw"},
		{"Deposit", []string{"deposit"}, "deposit"},  // case is ignored
		{"zzz", []string{"deposit", "withdraw"}, ""}, // nothing near
		{"x", nil, ""},
		{"depsoit", []string{"deposit"}, "deposit"},            // a transposition is one edit
		{"p1", []string{"q2", "p"}, ""},                        // two edits of two bytes: unrelated
		{"put", []string{"pop", "get"}, ""},                    // two edits of three
		{"nonNegatve", []string{"nonNegative"}, "nonNegative"}, // long names take two
		{"withdrwa", []string{"withdraw"}, "withdraw"},
		{"a", []string{"b"}, ""}, // a whole name replaced
	} {
		if got := Name(c.name, c.names); got != c.want {
			t.Errorf("Name(%q, %v) = %q, want %q", c.name, c.names, got, c.want)
		}
	}
}

func TestDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"", "", 0}, {"a", "", 1}, {"", "ab", 2}, {"kitten", "sitting", 3}, {"flaw", "lawn", 2}, {"ab", "ba", 1}, {"ca", "abc", 3}} {
		if got := Distance(c.a, c.b); got != c.want {
			t.Errorf("Distance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
