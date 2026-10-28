package gristtest

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/internal/calls"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// NoClientErrors: no client saw an error the application could have
// avoided. Every client call (actor <tenant>/clients) was answered (none
// left pending with its tries spent), none answered an error, and no try
// of one was refused, no server taking it: an endpoint a router still
// sent requests to after its pod shut its listener, an empty view, a
// server shut down while it was still routed to (the step's refused
// field, which gristsim's Cluster and grist/client count). Each such try
// is retried under the call's key, so the call's answer is right; what
// the claim says is that its clients never had to (a drain's delay
// longer than the router's lag, a disruption budget). A refusal the
// model explains is no error (Conforms judges those). The run must be
// judged with the clients' binding (System.WithBinding): a report with
// no client actor misses it, naming the fix. As a claim alone it is about
// every run.
func NoClientErrors() qtest.Expectation {
	return qtest.Satisfies("no client call refused, erring or left unanswered", func(r *validate.Report) error {
		if err := judgesClients(r); err != nil {
			return err
		}
		return clientError(r)
	})
}

// ClientErrors: some client call was refused a try, answered an error or
// left unanswered (NoClientErrors' negation): what a mutant that makes
// its clients retry shows in some run, Some(ClientErrors()). A report
// with no client actor misses it too, naming the fix.
func ClientErrors() qtest.Expectation {
	return qtest.Satisfies("some client call refused, erring or left unanswered", func(r *validate.Report) error {
		if err := judgesClients(r); err != nil {
			return err
		}
		if clientError(r) == nil {
			return errors.New("every client call was answered at its first try that a server took")
		}
		return nil
	})
}

// judgesClients is why r has no verdict on its clients: no client actor.
func judgesClients(r *validate.Report) error {
	for _, a := range r.Actors {
		if strings.HasSuffix(a.Actor, "/clients") {
			return nil
		}
	}
	return errors.New("the report judges no client's calls (actor <tenant>/clients): judge the run with the clients' binding (System.WithBinding)")
}

// clientError is r's first client call refused a try, erring or left
// unanswered, or nil.
func clientError(r *validate.Report) error {
	for _, a := range r.Actors {
		if !strings.HasSuffix(a.Actor, "/clients") {
			continue
		}
		for _, p := range a.Pending {
			return fmt.Errorf("%s left unanswered (its tries spent)", callName(p))
		}
		for _, c := range a.Calls {
			switch s := c.Step; {
			case s == nil:
			case s.Error != "":
				return fmt.Errorf("%s answered an error: %s", callName(*s), s.Error)
			case refused(*s) > 0:
				return fmt.Errorf("%s had %d tries refused, no server taking them (a router's endpoint gone or none, a server shut down while still routed to)", callName(*s), refused(*s))
			}
		}
	}
	return nil
}

// callName names a client call's step: its action and id.
func callName(s qtrace.Step) string {
	if id, ok := s.Args["id"]; ok {
		return fmt.Sprintf("%s %v", s.Action, id)
	}
	return s.Action
}

// refused is a client call's count of refused tries (calls.RefusedField):
// an int in process, a float64 read back from JSON.
func refused(s qtrace.Step) int {
	switch v := s.Args[calls.RefusedField].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}
