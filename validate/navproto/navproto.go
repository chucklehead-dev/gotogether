// Package navproto is the run navigation protocol (DESIGN §58, §63):
// the lines between a test binary serving its exploration's runs
// (GOTOGETHER_NAV=serve, qtest) and a navigator (gotogether nav,
// gotogether-nav), each built against its own version of gotogether, so
// its own package, versioned (Protocol) and with no dependency on what
// judges runs.
package navproto

import (
	"fmt"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
	"github.com/chucklehead-dev/gotogether/validate/receipt"

	"github.com/chucklehead-dev/gotogether/internal/formats"
)

// A NamedValue is a named value, as a Quint literal.
type NamedValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Node is a node of qtest's run navigator (DESIGN §58), as `gotogether
// nav -run ... -json` prints it (the test binary writes it; its types are
// here so that the CLI and gotogether-nav, which a test package does not
// build, read it): ID is "<schedule>@<k>", the choices before k fixed.
type Node struct {
	// Receipt describes the whole nominal run, independently of Model cursor states.
	Receipt receipt.Summary `json:"receipt"`
	// Diagnostics describe the nominal whole run at its judgment boundary, not Model.
	Diagnostics []diagnostic.Result `json:"diagnostics,omitempty"`
	// Protocol is the navigation protocol of the binary that wrote the
	// node (Protocol)
	Protocol string `json:"protocol"`
	Run      string `json:"run"`
	ID       string `json:"id"`
	Schedule string `json:"schedule"`
	At       int    `json:"at"`
	// Path is the choices before the node in words: "after 4 choices:
	// client ran 4 times", with the faults made
	Path string `json:"path,omitempty"`
	// Choice is the choice the node is before; nil when the run made
	// none there (it ended after At choices)
	Choice   *Choice  `json:"choice,omitempty"`
	Branches []Branch `json:"branches"`
	// Steps are the steps recorded before the choice, Tasks the
	// simulation's tasks then (Sim.Tasks), Elapsed its virtual time
	Steps   []string `json:"steps"`
	Tasks   []string `json:"tasks,omitempty"`
	Elapsed string   `json:"elapsed,omitempty"`
	// Model is each actor's model state after Steps (the Go evaluator's
	// replay of its plan), or why there is none (a step the model
	// rejects: the explanation)
	Model []ActorState `json:"model,omitempty"`
	// Folded is how many choices the node advanced through to reach At,
	// asked to fold (Request.Fold): choices that were no decision
	Folded int `json:"folded,omitempty"`
	// Saved is the corpus entry the node's run was saved to
	// (GOTOGETHER_NAV_SAVE), Notes what else is to know
	Saved string   `json:"saved,omitempty"`
	Notes []string `json:"notes,omitempty"`
}

// A Choice is the choice a run node is before: its kind, how many
// options, and their names (the option the base run took, Taken).
type Choice struct {
	Kind    string   `json:"kind"`
	N       int      `json:"n"`
	Options []string `json:"options,omitempty"`
	Taken   int      `json:"taken"`
}

// A Branch is an option of a run node's choice and the node taking
// it reaches.
type Branch struct {
	Option int    `json:"option"`
	Name   string `json:"name,omitempty"`
	Child  string `json:"child"`
}

// An ActorState is an actor's model state at a run node: its variables,
// or why the evaluator has none (Error).
type ActorState struct {
	// Diagnostics describe this cursor state only and assert no verdict.
	Diagnostics []diagnostic.Result `json:"diagnostics,omitempty"`
	Actor       string              `json:"actor"`
	State       []NamedValue        `json:"state,omitempty"`
	Error       string              `json:"error,omitempty"`
}

// Protocol is the run navigation protocol this version speaks: the
// Request, Response, Session and Node lines between a test
// binary (GOTOGETHER_NAV) and a navigator (gotogether nav,
// gotogether-nav), each built against its own version of gotogether. A
// session's first line and a node name it.
const Protocol = "gotogether.nav/2"

// CheckProtocol refuses a protocol a session or node names that this
// version does not speak, with the fix (a later one: upgrade the
// navigator; an earlier one: rebuild the test binary). An absent protocol is an error.
func CheckProtocol(p string) error {
	if _, err := formats.Nav.Check(p); err != nil {
		return fmt.Errorf("the test binary's navigation protocol: %w", err)
	}
	return nil
}

// A Request asks a run navigation session (GOTOGETHER_NAV=serve: the
// test binary reads one a line) for a node: Node as GOTOGETHER_NAV names
// one ("" or start, fail, @k, <schedule>@k), then the options of Follow
// and Take taken in turn (as GOTOGETHER_NAV_FOLLOW); Fold advances it
// through the choices that are no decision (one option, or nominal where
// the base run's are too) to the next that is; Save keeps its run in the
// exploration's corpus.
type Request struct {
	Node   string `json:"node,omitempty"`
	Follow []int  `json:"follow,omitempty"`
	Take   *int   `json:"take,omitempty"`
	Fold   bool   `json:"fold,omitempty"`
	Save   bool   `json:"save,omitempty"`
}

// A Response is a session's line: first its Session, before any
// request, then for each request its Node or the Error that it named
// none (the session goes on).
type Response struct {
	Session *Session `json:"session,omitempty"`
	Node    *Node    `json:"node,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// A Session names the run a session navigates and how many choices
// its base run made (but entropy); Notes say what to know of every node
// (the base run drew entropy, or did not replay).
type Session struct {
	// Protocol is the session's navigation protocol (Protocol): a navigator refuses one it does
	// not speak (CheckProtocol)
	Protocol string   `json:"protocol"`
	Run      string   `json:"run"`
	Choices  int      `json:"choices"`
	Notes    []string `json:"notes,omitempty"`
}
