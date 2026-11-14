//go:build valkeyreal

package simvalkey

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/valkey-io/valkey-go"
)

type observed struct {
	Value any
	Code  string
}

func observe(t *testing.T, args []string, r valkey.ValkeyResult) observed {
	t.Helper()
	v, err := r.ToAny()
	if valkey.IsValkeyNil(err) {
		return observed{Code: "nil"}
	}
	if err != nil {
		if _, ok := errors.AsType[*valkey.ValkeyError](err); !ok {
			t.Errorf("transport failure for%v: %v", args, err)
			return observed{Code: "transport_failure"}
		}
		text := strings.TrimPrefix(err.Error(), "ERR ")
		if strings.EqualFold(args[0], "EVAL") && strings.Contains(args[1], "after write") && !strings.Contains(text, "after write") {
			t.Fatalf("unrelated Lua error: %v", err)
		}
		first := strings.Fields(text)[0]
		code := "ERR"
		if first == strings.ToUpper(first) && !strings.Contains(first, ":") {
			code = first
		}
		return observed{Code: code}
	}
	if strings.EqualFold(args[0], "HGETALL") {
		a := v.([]any)
		m := map[string]any{}
		for i := 0; i < len(a); i += 2 {
			m[a[i].(string)] = a[i+1]
		}
		v = m
	}
	if strings.EqualFold(args[0], "HKEYS") {
		a := v.([]any)
		slices.SortFunc(a, func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
		v = a
	}
	if strings.EqualFold(args[0], "TIME") {
		a := v.([]any)
		if len(a) != 2 {
			t.Fatalf("TIME=%v", v)
		}
		seconds, e := strconv.ParseInt(a[0].(string), 10, 64)
		_ = seconds
		if e != nil {
			t.Fatal(e)
		}
		micro, e := strconv.ParseInt(a[1].(string), 10, 64)
		if e != nil || micro < 0 || micro >= 1_000_000 {
			t.Fatalf("TIME=%v", v)
		}
		v = "server-time-shape"
	}
	return observed{Value: v}
}

// This native tier checks the supported RESP2 command/script profile. Time
// readings have independent clocks, and unordered hash fields are compared
// as maps. Replication/transport faults are tested separately in simulation.
func TestRealContract(t *testing.T) {
	addr := os.Getenv("GOTOGETHER_VALKEY_ADDR")
	if addr == "" {
		t.Fatal("set GOTOGETHER_VALKEY_ADDR to a disposable Valkey server")
	}
	real, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}, DisableCache: true, AlwaysRESP2: true})
	if err != nil {
		t.Fatal(err)
	}
	defer real.Close()
	script := `redis.call('SET',KEYS[1],'written'); error('after write')`
	commands := [][]string{

		{"PING"}, {"TIME"}, {"GET", "sv:k"}, {"SET", "sv:k", "a"}, {"SET", "sv:k", "b", "NX"}, {"GET", "sv:k"},
		{"SET", "sv:k", "c", "XX", "PX", "100000"}, {"GET", "sv:k"}, {"PEXPIRE", "sv:k", "0"}, {"GET", "sv:k"}, {"PTTL", "sv:k"},
		{"INCR", "sv:n"}, {"INCRBY", "sv:n", "-2"}, {"GET", "sv:n"}, {"SET", "sv:n", "9223372036854775807"}, {"INCR", "sv:n"}, {"GET", "sv:n"},
		{"HSET", "sv:h", "b", "2", "a", "1"}, {"HGETALL", "sv:h"}, {"HKEYS", "sv:h"}, {"HINCRBY", "sv:h", "a", "4"}, {"HGET", "sv:h", "a"}, {"HLEN", "sv:h"}, {"HDEL", "sv:h", "a", "b"}, {"EXISTS", "sv:h"},
		{"ZADD", "sv:z", "1", "b", "1", "a", "2", "c"}, {"ZRANGE", "sv:z", "0", "-1"}, {"ZRANGE", "sv:z", "0", "-1", "WITHSCORES"},
		{"ZREMRANGEBYSCORE", "sv:z", "(1", "2"}, {"ZRANGE", "sv:z", "0", "-1"}, {"ZREM", "sv:z", "a", "b"}, {"EXISTS", "sv:z"},
		{"HGET", "sv:n", "x"}, {"ZADD", "sv:z", "nan", "bad"}, {"EXISTS", "sv:z"},
		{"EVAL", "return {redis.call('INCR',KEYS[1]),false,'tail'}", "1", "sv:eval"},
		{"EVAL", script, "1", "sv:err"}, {"GET", "sv:err"},
		{"EVALSHA", strings.Repeat("0", 40), "0"},
	}
	if e := real.Do(t.Context(), real.B().Arbitrary("DEL", "sv:k", "sv:n", "sv:h", "sv:z", "sv:err", "sv:eval").Build()).Error(); e != nil {
		t.Fatal(e)
	}
	want := make([]observed, len(commands))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for i, args := range commands {
		want[i] = observe(t, args, real.Do(ctx, real.B().Arbitrary(args...).Build()))
	}
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	v := New(s, "contract")
	done := false
	s.Spawn("compare", func(ctx context.Context) {
		c := v.Client()
		for i, args := range commands {
			got := observe(t, args, c.Do(ctx, c.B().Arbitrary(args...).Build()))
			if !reflect.DeepEqual(got, want[i]) {
				t.Errorf("command%d %v\n simulated=%#v\n real=%#v", i, args, got, want[i])
			}
		}
		done = true
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("comparison did not finish")
	}
	t.Logf("compared %d command/script outcomes with %s", len(commands), addr)
}
