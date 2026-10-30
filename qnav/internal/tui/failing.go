package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
)

// Failing is the navigator at a recorded run's failing step: the state
// after the most calls the model accepts, and why it rejects the next
// (the navigator's own explanation, without its first line's advice).
func Failing(ch *validate.Checker, start validate.NavStart) (ModelNav, string, string, error) {
	start.At = -1
	whole, rejected := ch.Nav(start)
	if rejected == nil {
		report, err := ch.Check(context.Background(), start.Steps)
		if err != nil {
			return nil, "", "", err
		}
		for _, actor := range report.Actors {
			if start.Actor != "" && start.Actor != actor.Actor {
				continue
			}
			var details []diagnostic.Result
			for _, ab := range actor.Absent {
				if ab.Cause == validate.Defect && ab.Diagnostic != nil {
					details = append(details, *ab.Diagnostic)
				}
			}
			if len(details) > 0 {
				return terminalDiagnosticNav{whole, details}, "terminal state · failed completion property", "The recorded history conforms; " + actor.Reason() + ".", nil
			}
		}
		return nil, "", "", errors.New("the model accepts every call of the run: no failing step")
	}
	var last *validate.Navigator
	for k := 0; ; k++ {
		start.At = k
		n, err := ch.Nav(start)
		if err != nil {
			if last == nil {
				return nil, "", "", err
			}
			return last, fmt.Sprintf("the state before call %d, which the model rejects", k), tidyWhy(rejected.Error()), nil
		}
		last = n
	}
}

// tidyWhy is the navigator's error for a call the model rejects, as the
// navigator shows it: the call and why, without the advice the command
// line gives (which call to open the run at: the navigator is there).
func tidyWhy(s string) string {
	s = strings.TrimPrefix(s, "nav: ")
	if i := strings.Index(s, " is not a transition"); i >= 0 {
		if j := strings.Index(s, "\n"); j > i {
			s = s[:i] + s[j:]
		}
	}
	return s
}

// A terminal diagnostic stays labeled as fixed-history evidence when the user
// explores other model branches from the failure boundary.
type terminalDiagnosticNav struct {
	ModelNav
	details []diagnostic.Result
}

func (n terminalDiagnosticNav) Node(path ...string) (*validate.Node, error) {
	node, err := n.ModelNav.Node(path...)
	return n.attach(node, err)
}
func (n terminalDiagnosticNav) Fold(path ...string) (*validate.Node, error) {
	node, err := n.ModelNav.Fold(path...)
	return n.attach(node, err)
}
func (n terminalDiagnosticNav) attach(node *validate.Node, err error) (*validate.Node, error) {
	if err != nil {
		return nil, err
	}
	copy := *node
	copy.Diagnostics = append(slices.Clone(node.Diagnostics), n.details...)
	return &copy, nil
}
