// Package engine implements simvalkey's synchronous command and Lua semantics.
// Calls have no scheduling seams: the caller supplies one server-clock reading.
package engine

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Unsupported says the simulation profile cannot judge this operation. It is
// a harness refusal, never a server response the application may recover from.
type Unsupported struct{ Reason string }

func (e *Unsupported) Error() string { return "simvalkey: unsupported: " + e.Reason }

// Error is a Valkey server error, as opposed to an unavailable transport.
type Error string

func (e Error) Error() string { return string(e) }

const wrongType Error = "WRONGTYPE Operation against a key holding the wrong kind of value"

type entry struct {
	kind    byte
	text    string
	hash    map[string]string
	zset    map[string]float64
	expires time.Time
}

// DB is a single server's data and volatile script cache. Calls are serialized
// by the simulation, not by internal locks. Writes advances only
// for applied data-write commands, including writes before a Lua error.
type DB struct {
	data    map[string]*entry
	scripts map[string]string
	Writes  uint64
}

// New returns an empty server.
func New() *DB { return &DB{data: map[string]*entry{}, scripts: map[string]string{}} }

// Clone captures independent data, absolute expiry times, and script cache.
func (d *DB) Clone() *DB {
	out := New()
	out.Writes = d.Writes
	for k, e := range d.data {
		x := *e
		if e.hash != nil {
			x.hash = map[string]string{}
			for f, v := range e.hash {
				x.hash[f] = v
			}
		}
		if e.zset != nil {
			x.zset = map[string]float64{}
			for m, v := range e.zset {
				x.zset[m] = v
			}
		}
		out.data[k] = &x
	}
	for k, s := range d.scripts {
		out.scripts[k] = s
	}
	return out
}

// ClearScripts discards volatile cached scripts without changing data.
func (d *DB) ClearScripts() { clear(d.scripts) }

func (d *DB) expire(now time.Time) {
	for k, e := range d.data {
		if !e.expires.IsZero() && !now.Before(e.expires) {
			delete(d.data, k)
		}
	}
}

// Check validates the database's canonical representation after expiry.
func (d *DB) Check(now time.Time) error {
	d.expire(now)
	for k, e := range d.data {
		switch e.kind {
		case 's':
			if e.hash != nil || e.zset != nil {
				return fmt.Errorf("key %q: string has collection storage", k)
			}
		case 'h':
			if len(e.hash) == 0 || e.zset != nil {
				return fmt.Errorf("key %q: invalid hash storage", k)
			}
		case 'z':
			if len(e.zset) == 0 || e.hash != nil {
				return fmt.Errorf("key %q: invalid sorted-set storage", k)
			}
			for _, v := range e.zset {
				if math.IsNaN(v) {
					return fmt.Errorf("key %q: NaN score", k)
				}
			}
		default:
			return fmt.Errorf("key %q: unknown value kind %q", k, e.kind)
		}
	}
	return nil
}

func arity(cmd string) error {
	return Error("ERR wrong number of arguments for '" + strings.ToLower(cmd) + "' command")
}
func integer(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != s {
		return 0, Error("ERR value is not an integer or out of range")
	}
	return n, nil
}
func sum(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b || b < 0 && a < math.MinInt64-b {
		return 0, Error("ERR increment or decrement would overflow")
	}
	return a + b, nil
}
func (d *DB) typed(k string, kind byte, create bool) (*entry, error) {
	e := d.data[k]
	if e != nil && e.kind != kind {
		return nil, wrongType
	}
	if e == nil && create {
		e = &entry{kind: kind}
		if kind == 'h' {
			e.hash = map[string]string{}
		}
		if kind == 'z' {
			e.zset = map[string]float64{}
		}
		d.data[k] = e
	}
	return e, nil
}

// Exec executes one command at the supplied server time. Results are nil, a
// string, int64, or []any; unsupported commands/options fail explicitly.
func (d *DB) Exec(now time.Time, args []string) (any, error) {
	d.expire(now)
	if len(args) == 0 {
		return nil, Error("ERR empty command")
	}
	cmd := strings.ToUpper(args[0])
	a := args[1:]
	switch cmd {
	case "PING":
		if len(a) == 0 {
			return "PONG", nil
		}
		if len(a) == 1 {
			return a[0], nil
		}
		return nil, arity(cmd)
	case "TIME":
		if len(a) != 0 {
			return nil, arity(cmd)
		}
		return []any{strconv.FormatInt(now.Unix(), 10), strconv.FormatInt(int64(now.Nanosecond()/1000), 10)}, nil
	case "GET":
		if len(a) != 1 {
			return nil, arity(cmd)
		}
		e, err := d.typed(a[0], 's', false)
		if err != nil || e == nil {
			return nil, err
		}
		return e.text, nil
	case "SET":
		return d.set(now, a)
	case "INCR", "INCRBY":
		if cmd == "INCR" && len(a) != 1 || cmd == "INCRBY" && len(a) != 2 {
			return nil, arity(cmd)
		}
		inc := int64(1)
		var err error
		if cmd == "INCRBY" {
			inc, err = integer(a[1])
			if err != nil {
				return nil, err
			}
		}
		e, err := d.typed(a[0], 's', false)
		if err != nil {
			return nil, err
		}
		n := int64(0)
		if e != nil {
			n, err = integer(e.text)
			if err != nil {
				return nil, err
			}
		}
		n, err = sum(n, inc)
		if err != nil {
			return nil, err
		}
		if e == nil {
			e, _ = d.typed(a[0], 's', true)
		}
		e.text = strconv.FormatInt(n, 10)
		d.Writes++
		return n, nil
	case "DEL", "EXISTS":
		if len(a) == 0 {
			return nil, arity(cmd)
		}
		n := int64(0)
		for _, k := range a {
			if _, ok := d.data[k]; ok {
				n++
				if cmd == "DEL" {
					delete(d.data, k)
				}
			}
		}
		if cmd == "DEL" && n > 0 {
			d.Writes++
		}
		return n, nil
	case "PEXPIRE":
		if len(a) != 2 {
			return nil, arity(cmd)
		}
		ms, err := integer(a[1])
		if err != nil {
			return nil, err
		}
		if d.data[a[0]] == nil {
			return int64(0), nil
		}
		if ms <= 0 {
			delete(d.data, a[0])
			d.Writes++
			return int64(1), nil
		}
		at, err := sum(now.UnixMilli(), ms)
		if err != nil {
			return nil, Error("ERR invalid expire time in 'pexpire' command")
		}
		d.data[a[0]].expires = time.UnixMilli(at)
		d.Writes++
		return int64(1), nil
	case "PTTL":
		if len(a) != 1 {
			return nil, arity(cmd)
		}
		e := d.data[a[0]]
		if e == nil {
			return int64(-2), nil
		}
		if e.expires.IsZero() {
			return int64(-1), nil
		}
		return e.expires.UnixMilli() - now.UnixMilli(), nil
	case "HGET", "HGETALL", "HSET", "HINCRBY", "HDEL", "HLEN", "HKEYS":
		return d.hash(cmd, a)
	case "ZADD", "ZREM", "ZRANGE", "ZCARD", "ZREMRANGEBYSCORE", "ZSCORE":
		return d.sorted(cmd, a)
	case "EVAL", "EVALSHA":
		return d.eval(now, cmd, a)
	case "SCRIPT":
		return d.script(a)
	default:
		return nil, &Unsupported{Reason: "command " + strings.ToLower(cmd)}
	}
}

func (d *DB) set(now time.Time, a []string) (any, error) {
	if len(a) < 2 {
		return nil, arity("SET")
	}
	nx, xx, keep := false, false, false
	var expiry time.Time
	for i := 2; i < len(a); i++ {
		switch strings.ToUpper(a[i]) {
		case "NX":
			if nx || xx {
				return nil, Error("ERR syntax error")
			}
			nx = true
		case "XX":
			if nx || xx {
				return nil, Error("ERR syntax error")
			}
			xx = true
		case "KEEPTTL":
			if keep || !expiry.IsZero() {
				return nil, Error("ERR syntax error")
			}
			keep = true
		case "PX", "EX":
			if keep || !expiry.IsZero() || i+1 >= len(a) {
				return nil, Error("ERR syntax error")
			}
			n, err := integer(a[i+1])
			if err != nil {
				return nil, err
			}
			if n <= 0 {
				return nil, Error("ERR invalid expire time in 'set' command")
			}
			if strings.EqualFold(a[i], "EX") {
				if n > math.MaxInt64/1000 {
					return nil, Error("ERR invalid expire time in 'set' command")
				}
				n *= 1000
			}
			at, err := sum(now.UnixMilli(), n)
			if err != nil {
				return nil, Error("ERR invalid expire time in 'set' command")
			}
			expiry = time.UnixMilli(at)
			i++
		default:
			return nil, &Unsupported{Reason: "SET option " + a[i]}
		}
	}
	old := d.data[a[0]]
	if nx && old != nil || xx && old == nil {
		return nil, nil
	}
	if keep && old != nil {
		expiry = old.expires
	}
	d.data[a[0]] = &entry{kind: 's', text: a[1], expires: expiry}
	d.Writes++
	return "OK", nil
}

func (d *DB) hash(cmd string, a []string) (any, error) {
	want := 1
	switch cmd {
	case "HGET":
		want = 2
	case "HINCRBY":
		want = 3
	case "HDEL":
		if len(a) < 2 {
			return nil, arity(cmd)
		}
	case "HSET":
		if len(a) < 3 || len(a)%2 != 1 {
			return nil, arity(cmd)
		}
	}
	if cmd != "HDEL" && cmd != "HSET" && len(a) != want {
		return nil, arity(cmd)
	}
	if len(a) == 0 {
		return nil, arity(cmd)
	}
	e, err := d.typed(a[0], 'h', false)
	if err != nil {
		return nil, err
	}
	switch cmd {
	case "HGET":
		if e == nil {
			return nil, nil
		}
		v, ok := e.hash[a[1]]
		if !ok {
			return nil, nil
		}
		return v, nil
	case "HLEN":
		if e == nil {
			return int64(0), nil
		}
		return int64(len(e.hash)), nil
	case "HKEYS", "HGETALL":
		out := []any{}
		if e == nil {
			return out, nil
		}
		keys := make([]string, 0, len(e.hash))
		for k := range e.hash {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			out = append(out, k)
			if cmd == "HGETALL" {
				out = append(out, e.hash[k])
			}
		}
		return out, nil
	case "HSET":
		if e == nil {
			e, _ = d.typed(a[0], 'h', true)
		}
		n := int64(0)
		for i := 1; i < len(a); i += 2 {
			if _, ok := e.hash[a[i]]; !ok {
				n++
			}
			e.hash[a[i]] = a[i+1]
		}
		d.Writes++
		return n, nil
	case "HDEL":
		n := int64(0)
		if e == nil {
			return n, nil
		}
		for _, k := range a[1:] {
			if _, ok := e.hash[k]; ok {
				delete(e.hash, k)
				n++
			}
		}
		if n > 0 {
			d.Writes++
		}
		if len(e.hash) == 0 {
			delete(d.data, a[0])
		}
		return n, nil
	case "HINCRBY":
		inc, err := integer(a[2])
		if err != nil {
			return nil, err
		}
		n := int64(0)
		if e != nil {
			if v, ok := e.hash[a[1]]; ok {
				n, err = integer(v)
				if err != nil {
					return nil, Error("ERR hash value is not an integer")
				}
			}
		}
		n, err = sum(n, inc)
		if err != nil {
			return nil, err
		}
		if e == nil {
			e, _ = d.typed(a[0], 'h', true)
		}
		e.hash[a[1]] = strconv.FormatInt(n, 10)
		d.Writes++
		return n, nil
	}
	panic("unhandled hash command")
}

// SameData compares replicated data after expiry, excluding volatile scripts
// and diagnostic mutation counters. It is used only for contract checks.
func (d *DB) SameData(other *DB, now time.Time) bool {
	d.expire(now)
	other.expire(now)
	return reflect.DeepEqual(d.data, other.data)
}
