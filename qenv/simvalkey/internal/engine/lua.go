package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// These are harness resource limits, not simulated Valkey server responses.
const maxScriptBytes = 1 << 20
const maxScriptInstructions = 1_000_000

// gopher-lua checks Done once per VM instruction. Closing this stable channel
// at a fixed count bounds loops without a wall-clock timer or extra goroutine.
type instructionContext struct {
	done    chan struct{}
	count   int
	stopped bool
}

func (c *instructionContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *instructionContext) Done() <-chan struct{} {
	c.count++
	if c.count > maxScriptInstructions && !c.stopped {
		c.stopped = true
		close(c.done)
	}
	return c.done
}
func (c *instructionContext) Err() error {
	if c.stopped {
		return context.Canceled
	}
	return nil
}
func (c *instructionContext) Value(any) any { return nil }

func sha(src string) string { sum := sha1.Sum([]byte(src)); return hex.EncodeToString(sum[:]) }
func (d *DB) script(a []string) (any, error) {
	if len(a) < 1 {
		return nil, arity("SCRIPT")
	}
	switch strings.ToUpper(a[0]) {
	case "LOAD":
		if len(a) != 2 {
			return nil, arity("SCRIPT LOAD")
		}
		if len(a[1]) > maxScriptBytes {
			return nil, &Unsupported{Reason: "script exceeds 1 MiB source budget"}
		}
		l := lua.NewState(lua.Options{SkipOpenLibs: true})
		defer l.Close()
		if _, err := l.LoadString(a[1]); err != nil {
			return nil, Error("ERR Error compiling script: " + err.Error())
		}
		k := sha(a[1])
		d.scripts[k] = a[1]
		return k, nil
	case "EXISTS":
		if len(a) < 2 {
			return nil, arity("SCRIPT EXISTS")
		}
		out := make([]any, 0, len(a)-1)
		for _, k := range a[1:] {
			n := int64(0)
			if _, ok := d.scripts[k]; ok {
				n = 1
			}
			out = append(out, n)
		}
		return out, nil
	case "FLUSH":
		if len(a) > 2 {
			return nil, arity("SCRIPT FLUSH")
		}
		if len(a) == 2 && !strings.EqualFold(a[1], "SYNC") && !strings.EqualFold(a[1], "ASYNC") {
			return nil, Error("ERR syntax error")
		}
		d.ClearScripts()
		return "OK", nil
	default:
		return nil, &Unsupported{Reason: "SCRIPT subcommand " + a[0]}
	}
}

func (d *DB) eval(now time.Time, cmd string, a []string) (any, error) {
	if len(a) < 2 {
		return nil, arity(cmd)
	}
	n, err := integer(a[1])
	if err != nil {
		return nil, err
	}
	if n < 0 {
		return nil, Error("ERR Number of keys can't be negative")
	}
	if n > int64(len(a)-2) {
		return nil, Error("ERR Number of keys can't be greater than number of args")
	}
	src := a[0]
	if cmd == "EVALSHA" {
		var ok bool
		src, ok = d.scripts[a[0]]
		if !ok {
			return nil, Error("NOSCRIPT No matching script. Please use EVAL.")
		}
	}
	if len(src) > maxScriptBytes {
		return nil, &Unsupported{Reason: "script exceeds 1 MiB source budget"}
	}
	l := lua.NewState(lua.Options{SkipOpenLibs: true, CallStackSize: 256, RegistrySize: 1024 * 20, RegistryMaxSize: 1024 * 100, RegistryGrowStep: 1024})
	defer l.Close()
	for _, lib := range []struct {
		name string
		open lua.LGFunction
	}{{lua.BaseLibName, lua.OpenBase}, {lua.TabLibName, lua.OpenTable}, {lua.StringLibName, lua.OpenString}, {lua.MathLibName, lua.OpenMath}} {
		if err := l.CallByParam(lua.P{Fn: l.NewFunction(lib.open), NRet: 0, Protect: true}, lua.LString(lib.name)); err != nil {
			return nil, &Unsupported{Reason: "initializing Lua sandbox: " + err.Error()}
		}
	}
	var refused *Unsupported
	refuse := func(reason string) int {
		refused = &Unsupported{Reason: reason}
		l.RaiseError("%s", refused.Error())
		return 0
	}
	for _, name := range []string{"dofile", "loadfile", "print", "collectgarbage", "newproxy"} {
		fnName := name
		l.SetGlobal(name, l.NewFunction(func(*lua.LState) int { return refuse("Lua " + fnName + " outside the sandbox profile") }))
	}
	originalToString := l.GetGlobal("tostring")
	l.SetGlobal("tostring", l.NewFunction(func(l *lua.LState) int {
		v := l.CheckAny(1)
		switch v.Type() {
		case lua.LTTable, lua.LTFunction, lua.LTThread, lua.LTUserData:
			return refuse("Lua tostring of identity-bearing values")
		}
		l.Push(originalToString)
		l.Push(v)
		l.Call(1, 1)
		return 1
	}))
	// These are real Valkey libraries, but not implemented by this bounded
	// profile. Access must refuse the harness, not fabricate a server error.
	for _, name := range []string{"cjson", "cmsgpack", "bit", "struct"} {
		library := name
		t, meta := l.NewTable(), l.NewTable()
		meta.RawSetString("__index", l.NewFunction(func(l *lua.LState) int { return refuse("Lua " + library + " library") }))
		l.SetMetatable(t, meta)
		l.SetGlobal(name, t)
	}
	mathlib := l.GetGlobal("math").(*lua.LTable)
	for _, name := range []string{"random", "randomseed"} {
		mathlib.RawSetString(name, l.NewFunction(func(*lua.LState) int {
			return refuse("Lua math.random/randomseed: use a modelled choice before calling the script")
		}))
	}
	// Dumped bytecode is neither needed for EVAL nor part of this profile.
	l.GetGlobal("string").(*lua.LTable).RawSetString("dump", l.NewFunction(func(*lua.LState) int { return refuse("Lua string.dump") }))
	redis := l.NewTable()
	for _, name := range []string{"setresp", "replicate_commands", "set_repl", "log", "acl_check_cmd", "breakpoint", "debug"} {
		redis.RawSetString(name, l.NewFunction(func(*lua.LState) int { return refuse("Lua redis auxiliary API outside simvalkey command profile") }))
	}
	call := func(protected bool) lua.LGFunction {
		return func(l *lua.LState) int {
			args := make([]string, l.GetTop())
			for i := range args {
				switch v := l.Get(i + 1).(type) {
				case lua.LString:
					args[i] = string(v)
				case lua.LNumber:
					if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
						l.RaiseError("ERR Lua redis lib command arguments must be strings or integers")
						return 0
					}
					args[i] = strconv.FormatFloat(float64(v), 'f', -1, 64)
				default:
					l.RaiseError("ERR Lua redis lib command arguments must be strings or integers")
					return 0
				}
			}
			if len(args) > 0 && (strings.EqualFold(args[0], "EVAL") || strings.EqualFold(args[0], "EVALSHA") || strings.EqualFold(args[0], "SCRIPT")) {
				err = Error("ERR This Redis command is not allowed from script")
			} else {
				var result any
				result, err = d.Exec(now, args)
				if err == nil {
					var value lua.LValue
					// Status replies become {ok=...}, as Valkey's Lua bridge specifies.
					if len(args) > 0 && (strings.EqualFold(args[0], "SET") || strings.EqualFold(args[0], "PING") && len(args) == 1) && result != nil {
						t := l.NewTable()
						t.RawSetString("ok", lua.LString(result.(string)))
						value = t
					} else {
						value = toLua(l, result)
					}
					l.Push(value)
					return 1
				}
			}
			if u, ok := err.(*Unsupported); ok {
				refused = u
				l.RaiseError("%s", u.Error())
				return 0
			}
			if protected {
				t := l.NewTable()
				t.RawSetString("err", lua.LString(err.Error()))
				l.Push(t)
				return 1
			}
			l.RaiseError("%s", err.Error())
			return 0
		}
	}
	redisMeta := l.NewTable()
	redisMeta.RawSetString("__index", l.NewFunction(func(l *lua.LState) int { return refuse("Lua redis." + l.CheckString(2)) }))
	l.SetMetatable(redis, redisMeta)
	redis.RawSetString("sha1hex", l.NewFunction(func(l *lua.LState) int { l.Push(lua.LString(sha(l.CheckString(1)))); return 1 }))
	redis.RawSetString("call", l.NewFunction(call(false)))
	redis.RawSetString("pcall", l.NewFunction(call(true)))
	redis.RawSetString("status_reply", l.NewFunction(func(l *lua.LState) int {
		t := l.NewTable()
		t.RawSetString("ok", lua.LString(l.CheckString(1)))
		l.Push(t)
		return 1
	}))
	redis.RawSetString("error_reply", l.NewFunction(func(l *lua.LState) int {
		t := l.NewTable()
		t.RawSetString("err", lua.LString(l.CheckString(1)))
		l.Push(t)
		return 1
	}))
	l.SetGlobal("redis", redis)
	keys, argv := l.NewTable(), l.NewTable()
	for _, v := range a[2 : 2+n] {
		keys.Append(lua.LString(v))
	}
	for _, v := range a[2+n:] {
		argv.Append(lua.LString(v))
	}
	l.SetGlobal("KEYS", keys)
	l.SetGlobal("ARGV", argv)
	fn, err := l.LoadString(src)
	if err != nil {
		return nil, Error("ERR Error compiling script: " + err.Error())
	}
	if globalWrites(fn.Proto) {
		return nil, &Unsupported{Reason: "Lua global assignment outside the sandbox profile"}
	}
	protectLua(l, fn, refuse)
	d.scripts[sha(src)] = src
	guard := &instructionContext{done: make(chan struct{})}
	l.SetContext(guard)
	err = l.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true})
	if refused != nil {
		return nil, refused
	}
	if guard.stopped {
		return nil, &Unsupported{Reason: fmt.Sprintf("script exceeded %d VM instruction checks", maxScriptInstructions)}
	}
	if err != nil {
		if strings.Contains(err.Error(), "registry overflow") || strings.Contains(err.Error(), "stack overflow") {
			return nil, &Unsupported{Reason: "Lua VM stack/registry resource limit"}
		}
		return nil, Error("ERR Error running script: " + err.Error())
	}
	return fromLua(l.Get(-1), 0)
}

func toLua(l *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LFalse
	case string:
		return lua.LString(x)
	case int64:
		return lua.LNumber(x)
	case []any:
		t := l.NewTable()
		for _, v := range x {
			t.Append(toLua(l, v))
		}
		return t
	default:
		panic(fmt.Sprintf("unexpected command result %T", v))
	}
}
func fromLua(v lua.LValue, depth int) (any, error) {
	if depth > 64 {
		return nil, &Unsupported{Reason: "Lua reply exceeds 64 table nesting levels"}
	}
	switch x := v.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		if !bool(x) {
			return nil, nil
		}
		return int64(1), nil
	case lua.LString:
		return string(x), nil
	case lua.LNumber:
		n := float64(x)
		if math.IsNaN(n) || math.IsInf(n, 0) || n >= math.Exp2(63) || n < -math.Exp2(63) {
			return nil, &Unsupported{Reason: "Lua reply number is outside signed 64-bit range"}
		}
		return int64(n), nil
	case *lua.LTable:
		if e := x.RawGetString("err"); e != lua.LNil {
			if s, ok := e.(lua.LString); ok {
				return nil, Error(s)
			}
		}
		if s := x.RawGetString("ok"); s != lua.LNil {
			if text, ok := s.(lua.LString); ok {
				return string(text), nil
			}
		}
		out := []any{}
		for i := 1; ; i++ {
			v := x.RawGetInt(i)
			if v == lua.LNil {
				break
			}
			r, err := fromLua(v, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, nil
	default:
		return nil, &Unsupported{Reason: fmt.Sprintf("Lua reply type %s", v.Type())}
	}
}

// globalWrites inspects compiled instructions, including nested functions. The
// pinned gopher-lua VM encodes its opcode in the high six bits (vm.go), so this
// does not guess about Lua source text or confuse local assignments with globals.
func globalWrites(p *lua.FunctionProto) bool {
	for _, inst := range p.Code {
		if int(inst>>26) == lua.OP_SETGLOBAL {
			return true
		}
	}
	for _, fn := range p.FunctionPrototypes {
		if globalWrites(fn) {
			return true
		}
	}
	return false
}

// protectLua keeps the function environment and sandbox libraries read-only.
// Proxies are empty so __newindex sees updates to existing names too. Raw and
// metatable APIs cannot expose their backing tables or bypass the write guard.
// KEYS/ARGV stay ordinary per-call arrays: Lua code may transform local input.
func protectLua(l *lua.LState, fn *lua.LFunction, refuse func(string) int) {
	backing := l.G.Global
	protected := map[*lua.LTable]bool{}
	proxies := map[*lua.LTable]*lua.LTable{}
	proxy := func(t *lua.LTable, label string) *lua.LTable {
		out, meta := l.NewTable(), l.NewTable()
		protected[out] = true
		proxies[t] = out
		meta.RawSetString("__index", l.NewFunction(func(l *lua.LState) int {
			value := l.GetTable(t, l.CheckAny(2))
			if tb, ok := value.(*lua.LTable); ok {
				if p, known := proxies[tb]; known {
					value = p
				}
			}
			l.Push(value)
			return 1
		}))
		meta.RawSetString("__newindex", l.NewFunction(func(*lua.LState) int { return refuse("Lua " + label + " mutation") }))
		meta.RawSetString("__metatable", lua.LString("simvalkey read-only"))
		l.SetMetatable(out, meta)
		return out
	}
	// getfenv of a Go library function would expose the original global table;
	// getmetatable of a string would expose gopher-lua's string library table.
	for _, name := range []string{"getfenv", "setfenv", "getmetatable", "setmetatable", "rawset"} {
		api := name
		backing.RawSetString(name, l.NewFunction(func(*lua.LState) int { return refuse("Lua " + api + " outside the sandbox profile") }))
	}
	for _, name := range []string{"rawget", "next", "pairs", "ipairs"} {
		original := backing.RawGetString(name)
		api := name
		backing.RawSetString(name, l.NewFunction(func(l *lua.LState) int {
			if t, ok := l.Get(1).(*lua.LTable); ok && protected[t] {
				return refuse("Lua " + api + " on a read-only sandbox table")
			}
			return luaForward(l, original)
		}))
	}
	// Native table mutators use raw writes, which bypass __newindex. Refuse
	// every table-library operation on a protected library; ordinary script
	// tables and input arrays keep their original operations.
	tables := backing.RawGetString("table").(*lua.LTable)
	tables.ForEach(func(k, v lua.LValue) {
		if f, ok := v.(*lua.LFunction); ok {
			tables.RawSet(k, l.NewFunction(func(l *lua.LState) int {
				if t, ok := l.Get(1).(*lua.LTable); ok && protected[t] {
					return refuse("Lua table operation on a read-only sandbox table")
				}
				return luaForward(l, f)
			}))
		}
	})
	for _, name := range []string{"math", "string", "table", "redis", "cjson", "cmsgpack", "bit", "struct"} {
		if t, ok := backing.RawGetString(name).(*lua.LTable); ok {
			p := proxy(t, "read-only library "+name)
			backing.RawSetString(name, p)
			if name == "string" {
				t.RawSetString("__index", p)
			}
		}
	}
	env := proxy(backing, "global environment")
	backing.RawSetString("_G", env)
	fn.Env = env
	// loadstring's later functions must get the protected environment too.
	l.Env = env
}

func luaForward(l *lua.LState, fn lua.LValue) int {
	args := l.GetTop()
	l.Push(fn)
	for i := 1; i <= args; i++ {
		l.Push(l.Get(i))
	}
	l.Call(args, lua.MultRet)
	return l.GetTop() - args
}
