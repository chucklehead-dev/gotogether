package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/chucklehead-dev/gotogether/validate"
)

// styles are the screen's. Colors are the terminal's sixteen (ANSI), so
// the terminal's theme, light or dark, chooses their shades; without
// colors (NO_COLOR) every style is plain and the markers (› the cursor,
// * a changed variable, ◂ the option the base run took) say it all.
type styles struct {
	title, crumb, section, changed, cursor, faint, pass, fail, taken, disabled, status lipgloss.Style
	help                                                                               help.Styles
}

func newStyles(noColor bool) styles {
	plain := lipgloss.NewStyle()
	if noColor {
		return styles{plain, plain, plain, plain, plain, plain, plain, plain, plain, plain, plain,
			help.Styles{ShortKey: plain, ShortDesc: plain, ShortSeparator: plain, Ellipsis: plain, FullKey: plain, FullDesc: plain, FullSeparator: plain}}
	}
	faint := plain.Faint(true)
	return styles{
		title:    plain.Bold(true).Foreground(lipgloss.Magenta),
		crumb:    plain.Foreground(lipgloss.Blue),
		section:  plain.Bold(true),
		changed:  plain.Bold(true).Foreground(lipgloss.Yellow),
		cursor:   plain.Reverse(true),
		faint:    faint,
		pass:     plain.Bold(true).Foreground(lipgloss.Green),
		fail:     plain.Bold(true).Foreground(lipgloss.Red),
		taken:    plain.Foreground(lipgloss.Cyan),
		disabled: plain.Foreground(lipgloss.Red),
		status:   plain.Italic(true),
		help: help.Styles{ShortKey: plain.Bold(true), ShortDesc: faint, ShortSeparator: faint, Ellipsis: faint,
			FullKey: plain.Bold(true), FullDesc: faint, FullSeparator: faint},
	}
}

// keyMap is the navigator's keys, as help shows them.
type keyMap struct {
	Up, Down, Take, Back, Fold, Fail, Save, Filter, Scroll, ScrollUp, Help, Quit key.Binding
}

func keys() keyMap {
	return keyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Take:     key.NewBinding(key.WithKeys("enter", "right", "l"), key.WithHelp("enter", "take")),
		Back:     key.NewBinding(key.WithKeys("backspace", "left", "h"), key.WithHelp("←/⌫", "back")),
		Fold:     key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "fold")),
		Fail:     key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "failing step")),
		Save:     key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Scroll:   key.NewBinding(key.WithKeys("pgdown", "space", "ctrl+d"), key.WithHelp("pgdn", "scroll down")),
		ScrollUp: key.NewBinding(key.WithKeys("pgup", "ctrl+u"), key.WithHelp("pgup", "scroll up")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp implements help.KeyMap.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Take, k.Back, k.Fold, k.Fail, k.Save, k.Filter, k.Help, k.Quit}
}

// FullHelp implements help.KeyMap.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.Take, k.Back}, {k.Fold, k.Fail, k.Save}, {k.Scroll, k.ScrollUp}, {k.Filter, k.Help, k.Quit}}
}

// A screen is a node's lines: its head (what it is and where), its
// branches (always in view), and its detail (state, explanations,
// steps), which scrolls.
type screen struct{ head, branches, detail []string }

// Render is the screen as text: View's content.
func (m *Model) Render() string {
	if m.quitting {
		return ""
	}
	c := m.cur()
	var sc screen
	switch {
	case c == nil:
		sc.head = []string{m.st.title.Render("gotogether-nav") + "  " + m.st.faint.Render(m.c.Title), "", "loading…"}
	case c.r != nil:
		sc = m.runScreen(c)
	default:
		sc = m.modelScreen(c)
	}
	var foot []string
	if m.filtering || m.filter.Value() != "" {
		foot = append(foot, m.filter.View())
	}
	switch {
	case m.busy:
		foot = append(foot, m.st.status.Render("working…"))
	case m.status != "":
		foot = append(foot, m.st.status.Render(m.status))
	}
	foot = append(foot, m.help.View(m.keys))
	var footLines []string
	for _, f := range foot {
		footLines = append(footLines, strings.Split(f, "\n")...)
	}
	head, detail := m.wrap(sc.head), m.wrap(sc.detail)
	var branches []string // a branch a line: the list's window counts lines
	for _, l := range sc.branches {
		branches = append(branches, ansi.Truncate(l, m.w, "…"))
	}
	lines := append(head, branches...)
	room := m.h - len(lines) - len(footLines) - 2
	if room < 3 {
		room = 3
	}
	if len(detail) > room {
		top := min(m.scroll, len(detail)-room+1)
		top = max(top, 0)
		m.scroll = top
		end := min(len(detail), top+room-1)
		detail = append(detail[top:end:end], m.st.faint.Render(fmt.Sprintf("── lines %d–%d of %d (pgup/pgdn scroll)", top+1, end, len(detail))))
	} else {
		m.scroll = 0
	}
	lines = append(lines, detail...)
	lines = append(lines, "")
	for _, l := range footLines {
		lines = append(lines, ansi.Truncate(l, m.w, "…"))
	}
	return strings.Join(lines, "\n")
}

// wrap wraps long lines (an explanation's condition is read whole), a
// continuation indented past its line's own indent.
func (m *Model) wrap(ls []string) []string {
	var out []string
	for _, l := range ls {
		if ansi.StringWidth(l) <= m.w {
			out = append(out, l)
			continue
		}
		plain := ansi.Strip(l)
		pad := strings.Repeat(" ", len(plain)-len(strings.TrimLeft(plain, " "))+2)
		for i, w := range strings.Split(ansi.Wrap(l, m.w-len(pad), ""), "\n") {
			if i > 0 {
				w = pad + strings.TrimLeft(w, " ")
			}
			out = append(out, w)
		}
	}
	return out
}

// crumbs are the path from the first node, each node's label.
func (m *Model) crumbs() string {
	var parts []string
	for i, e := range m.stack {
		l := e.label
		if i == len(m.stack)-1 {
			l = m.st.section.Render(l)
		} else {
			l = m.st.crumb.Render(l)
		}
		parts = append(parts, l)
	}
	return strings.Join(parts, m.st.faint.Render(" › "))
}

// branchLines are the visible branches, windowed around the cursor to
// at most room lines, with mark(i) after each.
func (m *Model) branchLines(c *entry, room int, mark func(i int) string) []string {
	vis := m.visible()
	if len(vis) == 0 {
		if m.filter.Value() != "" {
			return []string{m.st.faint.Render("  no branch matches the filter")}
		}
		return []string{m.st.faint.Render("  none")}
	}
	lo, hi := 0, len(vis)
	if hi > room {
		lo = min(max(0, c.cursor-room/2), len(vis)-room)
		hi = lo + room
	}
	var out []string
	if lo > 0 {
		out = append(out, m.st.faint.Render(fmt.Sprintf("  … %d above", lo)))
	}
	for k := lo; k < hi; k++ {
		b := vis[k]
		line := "  " + b.label
		if k == c.cursor {
			line = m.st.cursor.Render("› " + b.label)
		}
		out = append(out, line+mark(b.i))
	}
	if hi < len(vis) {
		out = append(out, m.st.faint.Render(fmt.Sprintf("  … %d below", len(vis)-hi)))
	}
	return out
}

func (m *Model) modelScreen(c *entry) screen {
	n := c.m
	st := m.st
	var sc screen
	sc.head = []string{st.title.Render("gotogether-nav") + "  " + c.title + st.faint.Render("  (from "+n.Start+")")}
	sc.head = append(sc.head, st.faint.Render("path: ")+m.crumbs())
	if n.Folded > 0 {
		sc.head = append(sc.head, st.faint.Render(fmt.Sprintf("folded %d transitions of single branches", n.Folded)))
	}
	sc.branches = append(sc.branches, "", st.section.Render(fmt.Sprintf("branches (%d)", len(n.Branches))))
	sc.branches = append(sc.branches, m.branchLines(c, max(3, m.h/4), func(i int) string {
		e := n.Branches[i]
		var picks []string
		for _, p := range e.Picks {
			picks = append(picks, p.Name+" = "+p.Value)
		}
		if len(picks) == 0 || len(e.Args) > 0 { // an action's arguments say its picks
			return ""
		}
		return st.faint.Render("  picks " + strings.Join(picks, ", "))
	})...)
	var out []string
	if c.why != "" {
		out = append(out, "", st.section.Render("the run's next call, which the model rejects"))
		for l := range strings.SplitSeq(strings.TrimRight(c.why, "\n"), "\n") {
			out = append(out, m.whyLine("  "+l))
		}
	}
	out = append(out, "", st.section.Render("state")+st.faint.Render("  (* changed by the last move)"))
	for _, v := range n.State {
		if slices.Contains(n.Changed, v.Name) {
			out = append(out, st.changed.Render("* "+v.Name+" = "+v.Value))
		} else {
			out = append(out, "  "+v.Name+" = "+v.Value)
		}
	}
	if len(n.Invariants) > 0 {
		out = append(out, "", st.section.Render("invariants"))
		for _, v := range n.Invariants {
			val := v.Value
			if val == "false" {
				val = st.fail.Render(val)
			}
			out = append(out, "  "+v.Name+" = "+val)
		}
	}
	if len(n.Disabled) > 0 {
		out = append(out, "", st.section.Render("disabled"))
		for _, d := range n.Disabled {
			out = append(out, "  "+st.disabled.Render(d.Action))
			if d.Why != nil {
				for l := range strings.SplitSeq(strings.TrimRight(d.Why.String("    "), "\n"), "\n") {
					out = append(out, m.whyLine(l))
				}
			}
		}
	}
	sc.detail = out
	return sc
}

func (m *Model) runScreen(c *entry) screen {
	n := c.r
	st := m.st
	var sc screen
	verdict := st.pass.Render(n.Verdict)
	if n.Verdict != "pass" {
		verdict = st.fail.Render(n.Verdict)
	}
	sc.head = []string{st.title.Render("gotogether-nav") + "  " + n.Run + "  " + st.faint.Render("node "+n.ID)}
	sc.head = append(sc.head, st.faint.Render("path: ")+m.crumbs())
	at := fmt.Sprintf("after %d choices", n.At)
	if n.Elapsed != "" {
		at += ", at " + n.Elapsed
	}
	if n.Folded > 0 {
		at += fmt.Sprintf(", %d folded", n.Folded)
	}
	sc.head = append(sc.head, st.section.Render("verdict ")+verdict+st.faint.Render("  (the run this node makes, nominal after it; "+at+")"))
	if ch := n.Choice; ch != nil {
		sc.branches = append(sc.branches, "", st.section.Render(fmt.Sprintf("next choice: %s (%d options)", ch.Kind, ch.N)))
		sc.branches = append(sc.branches, m.branchLines(c, max(3, m.h/4), func(i int) string {
			b := n.Branches[i]
			s := st.faint.Render("  → " + b.Child)
			if ch.Taken > 0 && b.Option == ch.Taken { // 0 is also "off the base run's path"
				s += st.taken.Render("  ◂ the run took")
			}
			return s
		})...)
	} else {
		sc.branches = append(sc.branches, "", st.faint.Render("the run made no more choices: its end"))
	}
	var out []string
	steps := n.Steps
	out = append(out, "", st.section.Render(fmt.Sprintf("steps so far (%d)", len(steps))))
	const shown = 8
	if len(steps) > shown {
		out = append(out, st.faint.Render(fmt.Sprintf("  … %d before", len(steps)-shown)))
		steps = steps[len(steps)-shown:]
	}
	for _, s := range steps {
		out = append(out, "  "+s)
	}
	var parent *validate.RunNode
	if len(m.stack) > 1 {
		parent = m.stack[len(m.stack)-2].r
	}
	for _, a := range n.Model {
		hint := ""
		if parent != nil && a.Error == "" {
			hint = st.faint.Render("  (* changed since the last node)")
		}
		out = append(out, "", st.section.Render("model ("+a.Actor+")")+hint)
		if a.Error != "" {
			out = append(out, st.disabled.Render("  the model rejects a step so far:"))
			for l := range strings.SplitSeq(strings.TrimRight(tidyWhy(a.Error), "\n "), "\n") {
				out = append(out, m.whyLine("  "+l))
			}
			continue
		}
		before := map[string]string{}
		if parent != nil {
			for _, p := range parent.Model {
				if p.Actor == a.Actor {
					for _, v := range p.State {
						before[v.Name] = v.Value
					}
				}
			}
		}
		for _, v := range a.State {
			if was, ok := before[v.Name]; parent != nil && (!ok || was != v.Value) {
				out = append(out, st.changed.Render("* "+v.Name+" = "+v.Value))
			} else {
				out = append(out, "  "+v.Name+" = "+v.Value)
			}
		}
	}
	if len(n.Tasks) > 0 {
		out = append(out, "", st.section.Render("tasks"))
		for _, t := range n.Tasks {
			out = append(out, "  "+t)
		}
	}
	if n.Saved != "" {
		out = append(out, "", "saved to the corpus: "+n.Saved)
	}
	if len(n.Notes) > 0 {
		out = append(out, "")
	}
	for _, note := range n.Notes {
		out = append(out, st.faint.Render("note: "+note))
	}
	sc.detail = out
	return sc
}

// whyLine is a line of an explanation: grist's reading stands out, the
// conditions are quieter.
func (m *Model) whyLine(l string) string {
	t := strings.TrimSpace(l)
	switch {
	case strings.HasPrefix(t, "grist: "):
		return m.st.changed.Render(l)
	case strings.Contains(t, " is false"):
		return l
	}
	return m.st.faint.Render(l)
}
