package qtrace

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
	"time"
)

// HistorySVG draws a client history: a lane per recorder (a client), each
// completed call a bar from its invocation to its completion, labelled
// with the call, what it observed and its number in order (the order a
// search judged, Linearize's: an empty order numbers nothing). A call
// left pending (invoked, never completed) is a dashed bar to the
// history's end, numbered if order assumes it took effect. failedAt is
// the index in order of the step the check failed at (-1: none), drawn
// red: where to look first in a history no order explains.
func HistorySVG(recorded, order []Step, failedAt int) []byte {
	traces, _ := Reconstruct(recorded)
	type bar struct {
		lane          string
		from, to      time.Time
		label         string
		pending, fail bool
		num           int
	}
	// a call's number in the order, by what it is (the order re-sequences
	// steps onto a recorder of its own)
	id := func(s *Step) string {
		return fmt.Sprintf("%s|%v|%v|%d", s.Action, s.Args, s.Obs, s.Time.UnixNano())
	}
	num, failed := map[string]int{}, ""
	for i := range order {
		s := order[i]
		if s.Ambiguous {
			s.Obs = nil // a pending call's assumed effect: matched by its invocation
		}
		num[id(&s)] = i + 1
		if i == failedAt {
			failed = id(&s)
		}
	}
	var bars []bar
	var t0, t1 time.Time
	widen := func(t time.Time) {
		if t.IsZero() {
			return
		}
		if t0.IsZero() || t.Before(t0) {
			t0 = t
		}
		if t.After(t1) {
			t1 = t
		}
	}
	for _, tr := range traces {
		for i := range tr.Steps {
			s := &tr.Steps[i]
			from := s.Invoked
			if from.IsZero() {
				from = s.Time
			}
			widen(from)
			widen(s.Time)
			label := s.String()
			if len(s.Obs) > 0 {
				label += " → " + obsString(s.Obs)
			}
			bars = append(bars, bar{lane: s.Recorder, from: from, to: s.Time, label: label, num: num[id(s)], fail: failed != "" && id(s) == failed})
		}
		for i := range tr.Pending {
			p := tr.Pending[i]
			widen(p.Time)
			q := p
			q.Obs = nil
			bars = append(bars, bar{lane: p.Recorder, from: p.Time, label: p.String() + " (pending)", pending: true, num: num[id(&q)], fail: failed != "" && id(&q) == failed})
		}
	}
	lanes := map[string]int{}
	var names []string
	for _, b := range bars {
		if _, ok := lanes[b.lane]; !ok {
			lanes[b.lane] = 0
			names = append(names, b.lane)
		}
	}
	sort.Strings(names)
	for i, n := range names {
		lanes[n] = i
	}
	const left, width, laneH, top = 140, 900, 34, 30
	span := t1.Sub(t0)
	if span <= 0 {
		span = time.Millisecond
	}
	end := t1.Add(span / 20) // a pending call runs past the last completion
	x := func(t time.Time) float64 {
		return left + float64(t.Sub(t0))/float64(end.Sub(t0))*(width-left-10)
	}
	var w bytes.Buffer
	height := top + laneH*len(names) + 20
	fmt.Fprintf(&w, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" font-family="sans-serif" font-size="11">`+"\n", width, height)
	fmt.Fprintf(&w, `<rect width="100%%" height="100%%" fill="#fff"/>`+"\n")
	fmt.Fprintf(&w, `<text x="8" y="18" font-size="12">history: %d calls over %s; numbers are the order judged; red is where it failed</text>`+"\n", len(bars), t1.Sub(t0))
	for i, n := range names {
		y := top + laneH*i
		fmt.Fprintf(&w, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#ddd"/>`+"\n", left, y+laneH-2, width-10, y+laneH-2)
		fmt.Fprintf(&w, `<text x="8" y="%d">%s</text>`+"\n", y+laneH/2+4, esc(n))
	}
	for _, b := range bars {
		y := top + laneH*lanes[b.lane] + 6
		x0 := x(b.from)
		x1 := x(end)
		if !b.pending {
			x1 = x(b.to)
		}
		if x1-x0 < 3 {
			x1 = x0 + 3
		}
		fill, stroke, dash := "#cfe0f5", "#4a78b0", ""
		if b.pending {
			fill, stroke, dash = "#f3f3f3", "#888", ` stroke-dasharray="4 3"`
		}
		if b.fail {
			fill, stroke = "#f6b5b5", "#c0392b"
		}
		fmt.Fprintf(&w, `<rect x="%.1f" y="%d" width="%.1f" height="16" rx="2" fill="%s" stroke="%s"%s/>`+"\n", x0, y, x1-x0, fill, stroke, dash)
		label := b.label
		if b.num > 0 {
			label = fmt.Sprintf("#%d %s", b.num, label)
		}
		fmt.Fprintf(&w, `<text x="%.1f" y="%d"><title>%s</title>%s</text>`+"\n", x0+3, y+12, esc(label), esc(clip(label, int((x1-x0)/6)+12)))
	}
	w.WriteString("</svg>\n")
	return w.Bytes()
}

// obsString is a step's observations, sorted, as label text.
func obsString(obs map[string]any) string {
	keys := make([]string, 0, len(obs))
	for k := range obs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, obs[k])
	}
	return strings.Join(parts, ", ")
}

// clip shortens s to about n characters (the bar's width; its title has
// all of it).
func clip(s string, n int) string {
	r := []rune(s)
	if n < 4 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
