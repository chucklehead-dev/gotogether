package engine

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
)

type scored struct {
	member string
	score  float64
}

func ordered(e *entry) []scored {
	out := []scored{}
	if e == nil {
		return out
	}
	for k, v := range e.zset {
		out = append(out, scored{k, v})
	}
	slices.SortFunc(out, func(a, b scored) int {
		if n := cmp.Compare(a.score, b.score); n != 0 {
			return n
		}
		return strings.Compare(a.member, b.member)
	})
	return out
}
func score(s string) (float64, error) {
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) {
		return 0, Error("ERR value is not a valid float")
	}
	if n == 0 {
		n = 0
	}
	return n, nil
}

type endpoint struct {
	value     float64
	exclusive bool
}

func boundary(s string) (endpoint, error) {
	x := endpoint{}
	if strings.HasPrefix(s, "(") {
		x.exclusive = true
		s = s[1:]
	}
	n, err := score(s)
	x.value = n
	return x, err
}
func (d *DB) sorted(cmd string, a []string) (any, error) {
	switch cmd {
	case "ZADD":
		// Reject options as unsupported, not a server error. Parse every score
		// before mutation: malformed later pairs must leave the whole command out.
		if len(a) < 2 {
			return nil, arity(cmd)
		}
		for _, option := range []string{"NX", "XX", "GT", "LT", "CH", "INCR"} {
			if strings.EqualFold(a[1], option) {
				return nil, &Unsupported{Reason: "ZADD option " + a[1]}
			}
		}
		if len(a) < 3 || len(a)%2 != 1 {
			return nil, arity(cmd)
		}
		ps := make([]scored, 0, len(a)/2)
		for i := 1; i < len(a); i += 2 {
			n, err := score(a[i])
			if err != nil {
				return nil, err
			}
			ps = append(ps, scored{a[i+1], n})
		}
		e, err := d.typed(a[0], 'z', true)
		if err != nil {
			return nil, err
		}
		added := int64(0)
		changed := false
		for _, p := range ps {
			old, ok := e.zset[p.member]
			if !ok {
				added++
			}
			if !ok || old != p.score {
				changed = true
			}
			e.zset[p.member] = p.score
		}
		if changed {
			d.Writes++
		}
		return added, nil
	case "ZREM":
		if len(a) < 2 {
			return nil, arity(cmd)
		}
		e, err := d.typed(a[0], 'z', false)
		if err != nil || e == nil {
			return int64(0), err
		}
		removed := int64(0)
		for _, m := range a[1:] {
			if _, ok := e.zset[m]; ok {
				removed++
				delete(e.zset, m)
			}
		}
		if len(e.zset) == 0 {
			delete(d.data, a[0])
		}
		if removed > 0 {
			d.Writes++
		}
		return removed, nil
	case "ZCARD":
		if len(a) != 1 {
			return nil, arity(cmd)
		}
		e, err := d.typed(a[0], 'z', false)
		if err != nil || e == nil {
			return int64(0), err
		}
		return int64(len(e.zset)), nil
	case "ZSCORE":
		if len(a) != 2 {
			return nil, arity(cmd)
		}
		e, err := d.typed(a[0], 'z', false)
		if err != nil || e == nil {
			return nil, err
		}
		n, ok := e.zset[a[1]]
		if !ok {
			return nil, nil
		}
		return strconv.FormatFloat(n, 'g', -1, 64), nil
	case "ZRANGE":
		if len(a) < 3 {
			return nil, arity(cmd)
		}
		if len(a) > 4 || len(a) == 4 && !strings.EqualFold(a[3], "WITHSCORES") {
			return nil, &Unsupported{Reason: "ZRANGE options"}
		}
		lo, err := integer(a[1])
		if err != nil {
			return nil, err
		}
		hi, err := integer(a[2])
		if err != nil {
			return nil, err
		}
		e, err := d.typed(a[0], 'z', false)
		if err != nil {
			return nil, err
		}
		ps := ordered(e)
		n := int64(len(ps))
		if lo < 0 {
			lo = n + lo
		}
		if hi < 0 {
			hi = n + hi
		}
		lo = max(lo, 0)
		hi = min(hi, n-1)
		out := []any{}
		for i := lo; i <= hi && i < n; i++ {
			out = append(out, ps[i].member)
			if len(a) == 4 {
				out = append(out, strconv.FormatFloat(ps[i].score, 'g', -1, 64))
			}
		}
		return out, nil
	case "ZREMRANGEBYSCORE":
		if len(a) != 3 {
			return nil, arity(cmd)
		}
		lo, err := boundary(a[1])
		if err != nil {
			return nil, err
		}
		hi, err := boundary(a[2])
		if err != nil {
			return nil, err
		}
		e, err := d.typed(a[0], 'z', false)
		if err != nil || e == nil {
			return int64(0), err
		}
		removed := int64(0)
		for m, n := range e.zset {
			if (n > lo.value || n == lo.value && !lo.exclusive) && (n < hi.value || n == hi.value && !hi.exclusive) {
				delete(e.zset, m)
				removed++
			}
		}
		if len(e.zset) == 0 {
			delete(d.data, a[0])
		}
		if removed > 0 {
			d.Writes++
		}
		return removed, nil
	}
	panic("unhandled sorted-set command")
}
