package actionmap_test

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qanalysis/actionmap"
)

// admit reads then reserves through the store's interface, two calls
// apart (reserve is the limiter's own helper), and the driver's method
// value audit reads twice: both are splits. done makes one round trip
// (Down takes no context, and audit.Log.Note is the module's own code,
// though only ./limit is loaded), init none, and Mem's own methods,
// behind the interface, are not counted again. outage's own release is
// the test's (a _test.go call), so it makes done's one round trip.
const fixture = "example.com/roundtripfixture/limit."

func TestRoundTrips(t *testing.T) {
	splits, err := actionmap.RoundTrips("../testdata/roundtrip", "./limit")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + actionmap.SplitReport(splits))
	got := map[string][]string{}
	for _, s := range splits {
		var calls []string
		for _, r := range s.Trips {
			calls = append(calls, strings.TrimPrefix(r.Call, fixture)+" in "+strings.TrimPrefix(r.In, fixture))
		}
		got[s.Action] = calls
	}
	want := map[string][]string{
		"admit": {"Store.Get in (Limiter).Admit", "Store.Reserve in (Limiter).reserve"},
		"audit": {"Store.Get in (Limiter).Audit", "Store.Get in (Limiter).Audit"},
	}
	if len(got) != len(want) {
		t.Fatalf("splits %v, want %v", got, want)
	}
	for a, calls := range want {
		if strings.Join(got[a], " ") != strings.Join(calls, " ") {
			t.Errorf("%s: %v, want %v", a, got[a], calls)
		}
	}
	if r := actionmap.SplitReport(splits); !strings.Contains(r, "admit (") || !strings.Contains(r, "one model step makes 2 round trips") {
		t.Errorf("report:\n%s", r)
	}
}
