// Package tui is gotogether-nav's interactive navigator (DESIGN §58): a
// bubbletea model over a model's navigator (validate.Navigator) or a
// run's navigation session (validate.RunRequest in, validate.RunNode
// out). Its state (the nodes visited, the cursor, the filter) changes in
// Update alone and View renders it, so tests drive it with key messages
// and compare the screens it renders, without a terminal.
package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/chucklehead-dev/gotogether/validate"
)

// A ModelNav is a model's navigator: *validate.Navigator.
type ModelNav interface {
	Node(path ...string) (*validate.Node, error)
	Fold(path ...string) (*validate.Node, error)
}

// A RunNav answers a run's nodes: a navigation session.
type RunNav interface {
	Node(req validate.RunRequest) (*validate.RunNode, error)
}

// Config is what the navigator opens: a model (Model, from Path, folded
// with Fold) or a run (Run, at its node Node, then Follow, folded with
// Fold).
type Config struct {
	// Title names what is navigated, in the header
	Title string

	Model ModelNav
	Path  []string
	// Failing is the navigator at a recorded run's failing step (the
	// state before the first call the model rejects), its title and why
	// the model rejects the call (the explanation): g; nil when the
	// model was opened without a run
	Failing func() (nav ModelNav, title, why string, err error)
	// Why is the opening node's rejected call, explained (a run opened
	// at its failing step)
	Why string

	Run    RunNav
	Node   string
	Follow []int

	Fold bool
	// NoColor renders without colors (NO_COLOR, or a terminal without
	// them): markers alone tell the cursor and the changed variables
	NoColor bool
}

// An entry is a node visited: a model's (its navigator and path) or a
// run's, the label its crumb shows, and the cursor it had.
type entry struct {
	label string
	nav   ModelNav
	path  []string
	title string
	m     *validate.Node
	r     *validate.RunNode
	// why is the recorded call the model rejects from a model's node
	why    string
	cursor int
}

// Model is the navigator's state.
type Model struct {
	c      Config
	stack  []entry
	st     styles
	keys   keyMap
	help   help.Model
	filter textinput.Model
	// filtering: the filter's input has the keys
	filtering bool
	w, h      int
	// scroll is the first line of the node's detail in view
	scroll   int
	status   string
	busy     bool
	quitting bool
}

// New is the navigator c opens; Init loads its first node.
func New(c Config) *Model {
	m := &Model{c: c, st: newStyles(c.NoColor), keys: keys(), w: 100, h: 40}
	m.help = help.New()
	m.help.Styles = m.st.help
	m.filter = textinput.New()
	m.filter.Prompt = "/"
	m.filter.Placeholder = "filter branches"
	return m
}

// A loaded message is a node fetched (or why none was), to push.
type loaded struct {
	e      entry
	err    error
	status string
	// noPush: the node only says something (a save), the stack stays
	noPush bool
}

// Init implements tea.Model: the first node.
func (m *Model) Init() tea.Cmd {
	m.busy = true
	c := m.c
	if c.Run != nil {
		label := c.Node
		if label == "" {
			label = "start"
		}
		return m.runCmd(label, validate.RunRequest{Node: c.Node, Follow: c.Follow, Fold: c.Fold})
	}
	return func() tea.Msg {
		e := entry{label: "init", nav: c.Model, path: c.Path, title: c.Title, why: c.Why}
		if c.Why != "" {
			e.label = "failing step"
		}
		var err error
		if c.Fold {
			e.m, err = c.Model.Fold(c.Path...)
		} else {
			e.m, err = c.Model.Node(c.Path...)
		}
		if err == nil {
			e.path = pathOf(e.m)
		}
		return loaded{e: e, err: err}
	}
}

// pathOf is a model node's path from its navigator's start: its id's
// transitions.
func pathOf(n *validate.Node) []string {
	if n.ID == "" {
		return nil
	}
	return validate.SplitPath(n.ID)
}

// runCmd fetches a run's node.
func (m *Model) runCmd(label string, req validate.RunRequest) tea.Cmd {
	run := m.c.Run
	return func() tea.Msg {
		n, err := run.Node(req)
		l := loaded{e: entry{label: label, r: n}, err: err}
		if err == nil && req.Fold {
			if n.Folded == 0 {
				l.noPush, l.status = true, "nothing to fold: this choice is a decision of the run"
			}
		}
		if err == nil && req.Save {
			l.noPush = true
			switch {
			case n.Saved != "":
				l.status = "saved to the corpus: " + n.Saved
			default:
				var why []string
				for _, note := range n.Notes {
					if strings.HasPrefix(note, "not saved") {
						why = append(why, note)
					}
				}
				l.status = strings.Join(why, "; ")
				if l.status == "" {
					l.status = "not saved"
				}
			}
		}
		return l
	}
}

// modelCmd fetches a model's node: path from nav's start, folded.
func modelCmd(label string, nav ModelNav, title string, path []string, fold bool) tea.Cmd {
	return func() tea.Msg {
		e := entry{label: label, nav: nav, title: title}
		var err error
		if fold {
			e.m, err = nav.Fold(path...)
		} else {
			e.m, err = nav.Node(path...)
		}
		l := loaded{e: e, err: err}
		if err == nil {
			l.e.path = pathOf(e.m)
			if fold && e.m.Folded == 0 {
				l.noPush, l.status = true, "nothing to fold: this node has several branches, or none"
			}
		}
		return l
	}
}

func (m *Model) cur() *entry {
	if len(m.stack) == 0 {
		return nil
	}
	return &m.stack[len(m.stack)-1]
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		return m, nil
	case loaded:
		m.busy = false
		if msg.err != nil {
			m.status = "error: " + msg.err.Error()
			if len(m.stack) == 0 { // nothing to show: the first node failed
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}
		m.status = msg.status
		if !msg.noPush {
			if c := m.cur(); c != nil && msg.e.m != nil && c.m != nil && c.m.ID == msg.e.m.ID && c.nav == msg.e.nav && msg.e.m.Folded == 0 {
				return m, nil // the same node again
			}
			if r := msg.e.r; r != nil && r.Choice != nil && r.Choice.Taken < len(r.Branches) {
				msg.e.cursor = r.Choice.Taken // the base run's option: following the run is enter, enter, ...
			}
			m.stack = append(m.stack, msg.e)
			m.clearFilter()
			m.scroll = 0
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	if m.filtering {
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) clearFilter() {
	m.filtering = false
	m.filter.SetValue("")
	m.filter.Blur()
}

// key handles a key press.
func (m *Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch msg.String() {
		case "enter":
			m.filtering = false
			m.filter.Blur()
			m.clampCursor()
			return m, nil
		case "esc":
			m.clearFilter()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.clampCursor()
		return m, cmd
	}
	k := m.keys
	switch {
	case key.Matches(msg, k.Quit):
		m.quitting = true
		return m, tea.Quit
	case key.Matches(msg, k.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	}
	c := m.cur()
	if c == nil || m.busy {
		return m, nil
	}
	switch {
	case key.Matches(msg, k.Scroll):
		m.scroll += max(1, m.h/2)
		return m, nil
	case key.Matches(msg, k.ScrollUp):
		m.scroll = max(0, m.scroll-max(1, m.h/2))
		return m, nil
	case key.Matches(msg, k.Up):
		if c.cursor > 0 {
			c.cursor--
		}
	case key.Matches(msg, k.Down):
		if c.cursor < len(m.visible())-1 {
			c.cursor++
		}
	case key.Matches(msg, k.Back):
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
			m.status, m.scroll = "", 0
		} else {
			m.status = "at the first node: nothing to go back to"
		}
	case key.Matches(msg, k.Take):
		return m, m.take()
	case key.Matches(msg, k.Fold):
		m.busy = true
		if c.r != nil {
			return m, m.runCmd("fold", validate.RunRequest{Node: c.r.ID, Fold: true})
		}
		return m, modelCmd("fold", c.nav, c.title, c.path, true)
	case key.Matches(msg, k.Fail):
		return m, m.fail()
	case key.Matches(msg, k.Save):
		if c.r == nil {
			m.status = "a model's node has no run to save; its path: -follow '" + strings.Join(c.path, ",") + "'"
			return m, nil
		}
		m.busy = true
		return m, m.runCmd("save", validate.RunRequest{Node: c.r.ID, Save: true})
	case key.Matches(msg, k.Filter):
		m.filtering = true
		return m, m.filter.Focus()
	case msg.String() == "esc":
		m.clearFilter()
	}
	return m, nil
}

func (m *Model) clampCursor() {
	c := m.cur()
	if c == nil {
		return
	}
	if n := len(m.visible()); c.cursor >= n {
		c.cursor = max(0, n-1)
	}
}

// A branch is a node's branch as the list shows it.
type branch struct {
	i     int // its index among the node's branches
	label string
}

// branches are the current node's branches.
func (m *Model) branches() []branch {
	c := m.cur()
	var out []branch
	switch {
	case c == nil:
	case c.m != nil:
		for i, e := range c.m.Branches {
			out = append(out, branch{i, e.Take})
		}
	case c.r != nil:
		for i, b := range c.r.Branches {
			out = append(out, branch{i, fmt.Sprintf("%d %s", b.Option, b.Name)})
		}
	}
	return out
}

// visible are the branches the filter keeps.
func (m *Model) visible() []branch {
	f := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	all := m.branches()
	if f == "" {
		return all
	}
	var out []branch
	for _, b := range all {
		if strings.Contains(strings.ToLower(b.label), f) {
			out = append(out, b)
		}
	}
	return out
}

// take takes the branch under the cursor.
func (m *Model) take() tea.Cmd {
	c := m.cur()
	vis := m.visible()
	if c.cursor >= len(vis) {
		m.status = "no branch to take here"
		return nil
	}
	b := vis[c.cursor]
	m.busy = true
	if c.r != nil {
		br := c.r.Branches[b.i]
		label := strings.TrimSpace(fmt.Sprintf("%d %s", br.Option, br.Name))
		return m.runCmd(label, validate.RunRequest{Node: br.Child})
	}
	e := c.m.Branches[b.i]
	return modelCmd(e.Take, c.nav, c.title, append(append([]string{}, c.path...), e.Take), false)
}

// fail opens the failing step: a run's fail node, or a model's state
// before the first call of its run the model rejects.
func (m *Model) fail() tea.Cmd {
	c := m.cur()
	if c.r != nil {
		m.busy = true
		return m.runCmd("fail", validate.RunRequest{Node: "fail"})
	}
	if m.c.Failing == nil {
		m.status = "no failing step: the model was opened without a run (-run steps.jsonl)"
		return nil
	}
	m.busy = true
	failing := m.c.Failing
	return func() tea.Msg {
		nav, title, why, err := failing()
		if err != nil {
			return loaded{err: err}
		}
		n, err := nav.Node()
		return loaded{e: entry{label: "failing step", nav: nav, title: title, m: n, why: why}, err: err}
	}
}

// View implements tea.Model: the screen, in the alternate screen.
func (m *Model) View() tea.View {
	v := tea.NewView(m.Render())
	v.AltScreen = true
	v.WindowTitle = "gotogether-nav"
	return v
}

// Status is the last thing the navigator said (an error, a save): what
// the program prints when it ends on an error.
func (m *Model) Status() string { return m.status }
