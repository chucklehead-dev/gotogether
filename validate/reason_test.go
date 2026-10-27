package validate

import "testing"

// A failing report's reason is its verdict's, else "unsettled" for a
// system that did not settle, else "fails".
func TestReportReason(t *testing.T) {
	t.Parallel()
	pass := &Result{Actor: "a", Conforms: Pass}
	fail := &Result{Actor: "b", Conforms: Fail, Failure: &Failure{Kind: "state", Step: nil}}
	for _, c := range []struct {
		r    *Report
		want string
	}{
		{&Report{Actors: []*Result{pass, fail}}, fail.Reason()},
		{&Report{Actors: []*Result{pass}, Unsettled: "tasks at work"}, "unsettled"},
		{&Report{Actors: []*Result{pass}}, "fails"},
		{&Report{}, "nothing was checked"},
	} {
		if got := c.r.Reason(); got != c.want || got == "" {
			t.Errorf("%+v: Reason %q, want %q", c.r, got, c.want)
		}
	}
}
