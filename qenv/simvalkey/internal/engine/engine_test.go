package engine

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

var epoch = time.Date(2026, 11, 14, 12, 0, 0, 123456000, time.UTC)

func command(t *testing.T, d *DB, now time.Time, args ...string) any {
	t.Helper()
	v, err := d.Exec(now, args)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return v
}
func equal(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

func TestExpiryHashAndIntegerBoundaries(t *testing.T) {
	d := New()
	equal(t, command(t, d, epoch, "SET", "a", "value", "PX", "100"), "OK")
	equal(t, command(t, d, epoch, "PTTL", "a"), int64(100))
	equal(t, command(t, d, epoch.Add(99*time.Millisecond), "GET", "a"), "value")
	equal(t, command(t, d, epoch.Add(100*time.Millisecond), "GET", "a"), nil)
	equal(t, command(t, d, epoch.Add(time.Second), "PTTL", "a"), int64(-2))
	equal(t, command(t, d, epoch, "HSET", "h", "b", "2", "a", "1"), int64(2))
	equal(t, command(t, d, epoch, "HKEYS", "h"), []any{"a", "b"})
	equal(t, command(t, d, epoch, "HGETALL", "h"), []any{"a", "1", "b", "2"})
	equal(t, command(t, d, epoch, "HINCRBY", "h", "a", "-2"), int64(-1))
	command(t, d, epoch, "SET", "max", strconv.FormatInt(math.MaxInt64, 10))
	before := d.Writes
	if _, err := d.Exec(epoch, []string{"INCR", "max"}); err == nil {
		t.Fatal("integer overflow succeeded")
	}
	if d.Writes != before {
		t.Fatal("failed increment counted as a write")
	}
	equal(t, command(t, d, epoch, "GET", "max"), strconv.FormatInt(math.MaxInt64, 10))
	if _, err := d.Exec(epoch, []string{"HGET", "max", "a"}); err != wrongType {
		t.Fatalf("wrong type: %v", err)
	}
	command(t, d, epoch, "HDEL", "h", "a", "b")
	equal(t, command(t, d, epoch, "EXISTS", "h"), int64(0))
	if err := d.Check(epoch); err != nil {
		t.Fatal(err)
	}
}

func TestSortedSetsTieOrderAndExclusiveBounds(t *testing.T) {
	d := New()
	equal(t, command(t, d, epoch, "ZADD", "z", "2", "z", "1", "b", "1", "a", "3", "c"), int64(4))
	equal(t, command(t, d, epoch, "ZRANGE", "z", "0", "-1"), []any{"a", "b", "z", "c"})
	equal(t, command(t, d, epoch, "ZRANGE", "z", "-2", "-1", "WITHSCORES"), []any{"z", "2", "c", "3"})
	equal(t, command(t, d, epoch, "ZREMRANGEBYSCORE", "z", "(1", "2"), int64(1))
	equal(t, command(t, d, epoch, "ZREMRANGEBYSCORE", "z", "-inf", "1"), int64(2))
	equal(t, command(t, d, epoch, "ZRANGE", "z", "0", "-1"), []any{"c"})
	before := d.Writes
	if _, err := d.Exec(epoch, []string{"ZADD", "other", "1", "ok", "NaN", "bad"}); err == nil {
		t.Fatal("NaN score accepted")
	}
	equal(t, command(t, d, epoch, "EXISTS", "other"), int64(0))
	if d.Writes != before {
		t.Fatal("invalid multi-score command partially applied")
	}
	for _, a := range [][]string{{"ZADD", "z", "NX", "1", "x"}, {"ZRANGE", "z", "0", "1", "REV"}} {
		if _, err := d.Exec(epoch, a); !isUnsupported(err) {
			t.Fatalf("%v not refused as unsupported: %v", a, err)
		}
	}
}

func isUnsupported(err error) bool { var u *Unsupported; return errors.As(err, &u) }

func TestRealLuaCacheAndPartialErrorWrites(t *testing.T) {
	d := New()
	src := `redis.call('HINCRBY', KEYS[1], 'n', ARGV[1]); return {redis.call('HGET', KEYS[1], 'n'), redis.call('TIME')[2]}`
	if _, err := d.Exec(epoch, []string{"EVALSHA", sha(src), "1", "h", "2"}); err == nil || !strings.HasPrefix(err.Error(), "NOSCRIPT") {
		t.Fatalf("missing cache: %v", err)
	}
	equal(t, command(t, d, epoch, "EVAL", src, "1", "h", "2"), []any{"2", "123456"})
	equal(t, command(t, d, epoch, "EVALSHA", sha(src), "1", "h", "3"), []any{"5", "123456"})
	copy := d.Clone()
	copy.ClearScripts()
	equal(t, command(t, d, epoch, "SCRIPT", "EXISTS", sha(src)), []any{int64(1)})
	equal(t, command(t, copy, epoch, "SCRIPT", "EXISTS", sha(src)), []any{int64(0)})
	command(t, copy, epoch, "HSET", "h", "n", "99")
	equal(t, command(t, d, epoch, "HGET", "h", "n"), "5")

	failure := `redis.call('HINCRBY', KEYS[1], 'n', 7); error('after-write')`
	if _, err := d.Exec(epoch, []string{"EVAL", failure, "1", "h"}); err == nil || !strings.Contains(err.Error(), "after-write") {
		t.Fatalf("runtime error: %v", err)
	}
	equal(t, command(t, d, epoch, "HGET", "h", "n"), "12")
	if d.Writes != 3 {
		t.Fatalf("writes=%d; want three mutating commands including failed script prefix", d.Writes)
	}
	// Redis pcall catches a genuine server error, but a harness refusal may not
	// be turned into an ordinary script reply and silently permit a test pass.
	equal(t, command(t, d, epoch, "EVAL", `local r=redis.pcall('HGET',KEYS[1],'n'); if type(r)=='table' then return r.err end; return false`, "1", "stringkey"), nil)
	command(t, d, epoch, "SET", "stringkey", "x")
	result := command(t, d, epoch, "EVAL", `local r=redis.pcall('HGET',KEYS[1],'n'); if type(r)=='table' then return r.err end; return false`, "1", "stringkey")
	if text, ok := result.(string); !ok || !strings.HasPrefix(text, "WRONGTYPE") {
		t.Fatalf("pcall error reply=%#v", result)
	}
	_, err := d.Exec(epoch, []string{"EVAL", `local ok,r=pcall(function() return redis.call('BLPOP','q',0) end); return ok`, "0"})
	if !isUnsupported(err) {
		t.Fatalf("unsupported command hidden by Lua pcall: %v", err)
	}
	d.ClearScripts()
	if _, err := d.Exec(epoch, []string{"EVALSHA", sha(src), "1", "h", "2"}); err == nil || !strings.HasPrefix(err.Error(), "NOSCRIPT") {
		t.Fatal("script cache did not clear")
	}
	equal(t, command(t, d, epoch, "HGET", "h", "n"), "12")
}

func TestLuaScriptIsOneAtomicServerTimeAndClonesAreIndependent(t *testing.T) {
	d := New()
	src := `local t=redis.call('TIME'); local now=tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000)
 local function take(k,call,ttl)
  redis.call('ZREMRANGEBYSCORE',k,'-inf',now)
  if redis.call('ZCARD',k)>=1 then return false end
  redis.call('ZADD',k,now+ttl,call);redis.call('PEXPIRE',k,ttl);return true
 end
 return take(KEYS[1],ARGV[1],tonumber(ARGV[2]))`
	equal(t, command(t, d, epoch, "EVAL", src, "1", "slots", "a", "100"), int64(1))
	equal(t, command(t, d, epoch, "EVAL", src, "1", "slots", "b", "100"), nil)
	clone := d.Clone()
	equal(t, command(t, d, epoch.Add(100*time.Millisecond), "EVAL", src, "1", "slots", "c", "100"), int64(1))
	equal(t, command(t, clone, epoch, "ZRANGE", "slots", "0", "-1"), []any{"a"})
	equal(t, command(t, d, epoch.Add(100*time.Millisecond), "ZRANGE", "slots", "0", "-1"), []any{"c"})
	equal(t, command(t, clone, epoch.Add(100*time.Millisecond), "ZRANGE", "slots", "0", "-1"), []any{})
	if err := d.Check(epoch.Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
}

func TestLuaResourceAndUnsupportedLibraryRefusals(t *testing.T) {
	d := New()
	for _, src := range []string{`while true do end`, `return math.random()`, `return redis.setresp(3)`, `return cjson.encode({x=1})`, `return tostring({})`} {
		if _, err := d.Exec(epoch, []string{"EVAL", src, "0"}); !isUnsupported(err) {
			t.Fatalf("%s: %v; want harness refusal", src, err)
		}
	}
	if _, err := d.Exec(epoch, []string{"NO_SUCH_COMMAND"}); !isUnsupported(err) {
		t.Fatalf("unknown command: %v", err)
	}
	// A normal script error stays a server error, not a harness refusal.
	_, err := d.Exec(epoch, []string{"EVAL", `error('ordinary')`, "0"})
	var server Error
	if !errors.As(err, &server) {
		t.Fatalf("ordinary script error type: %T %v", err, err)
	}
}

func TestLuaGlobalAssignmentIsUnsupported(t *testing.T) {
	d := New()
	for _, src := range []string{
		`foo=1; return foo`,
		`math={}; return 1`,
		`local function f() foo=1 end; f(); return foo`,
		`_G['foo']=1; return 1`,
		`rawset(_G,'foo',1); return 1`,
		`local f=loadstring('foo=1; return foo'); return f()`,
	} {
		if _, err := d.Exec(epoch, []string{"EVAL", src, "0"}); !isUnsupported(err) {
			t.Fatalf("global write %s: %T %v; want harness refusal", src, err, err)
		}
	}
	// Ordinary local variables and tables remain usable: refusing global writes
	// must not turn normal Lua assignment or an input array into a false refusal.
	equal(t, command(t, d, epoch, "EVAL", `local t={}; t.foo=1; table.insert(t,2); local n=0; for _,v in ipairs(t) do n=n+v end; return {t.foo,n,#ARGV}`, "0", "a", "b"), []any{int64(1), int64(2), int64(2)})
}

func TestLuaReadonlyLibraryMutationIsUnsupported(t *testing.T) {
	d := New()
	for _, src := range []string{
		`math.floor=function(x) return 99 end; return math.floor(1.1)`,
		`local m=math; m.floor=function(x) return 99 end; return 1`,
		`local ok=pcall(function() math.floor=nil end); return ok`,
		`rawset(math,'floor',nil); return 1`,
		`table.insert(math,1); return 1`,
		`setmetatable(math,{}); return 1`,
		`getmetatable('').__index.foo=1; return 1`,
		`('a').__index.foo=1; return 1`,
		`redis.call=function() return 1 end; return redis.call('INCR','n')`,
	} {
		if _, err := d.Exec(epoch, []string{"EVAL", src, "0"}); !isUnsupported(err) {
			t.Fatalf("library write %s: %T %v; want harness refusal", src, err, err)
		}
	}
	equal(t, command(t, d, epoch, "EVAL", `return {math.floor(1.9),string.len('abc')}`, "0"), []any{int64(1), int64(3)})
}
