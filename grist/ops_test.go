package grist_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// serve -db-file reads the store's DSN from a file (a mounted Secret: not
// in ps or the pod spec), trimmed; an empty or missing file, or both
// flags, are refused naming the fix.
func TestMainDBFile(t *testing.T) {
	dir := t.TempDir()
	dsnFile := filepath.Join(dir, "dsn")
	if err := os.WriteFile(dsnFile, []byte("sqlite:"+filepath.Join(dir, "app.db")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var errOut syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- grist.MainArgs(ctx, apiApp(), []string{"serve", "-db-file", dsnFile, "-addr", "127.0.0.1:0", "-tenant", "t1", "-caller-header", "X-Caller"}, &errOut, &errOut)
	}()
	url := waitServing(t, &errOut, done)
	c := &client.Client{App: apiApp(), URL: url, CallerHeader: "X-Caller"}
	if _, err := c.Command(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"a"}`)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut.String(), "sqlite:") {
		t.Errorf("serve printed the DSN it read from -db-file:\n%s", errOut.String())
	}
	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, []byte("\n"), 0o600)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-db-file", filepath.Join(dir, "missing")}, "-db-file"},
		{[]string{"-db-file", empty}, "is empty"},
		{[]string{"-db-file", dsnFile, "-db", "sqlite:x.db"}, "not both"},
	} {
		err := grist.MainArgs(context.Background(), apiApp(), append([]string{"serve"}, tc.args...), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("serve %v: %v, want an error saying %q", tc.args, err, tc.want)
		}
	}
}

// waitServing waits for serve's "serving on" line and returns its URL.
func waitServing(t *testing.T, errOut *syncBuffer, done <-chan error) string {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("serve did not start: %s", errOut.String())
		}
		select {
		case err := <-done:
			t.Fatalf("serve ended at start: %v\n%s", err, errOut.String())
		default:
		}
		if _, after, ok := strings.Cut(errOut.String(), "serving on "); ok {
			url, _, _ := strings.Cut(after, "\n")
			return strings.TrimSpace(url)
		}
	}
}

// lifecycleLines are serve's structured lifecycle log lines (JSON
// objects, one a line) in out, by their msg.
func lifecycleLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil && m["msg"] != nil {
			lines = append(lines, m)
		}
	}
	return lines
}

// serve logs its lifecycle as JSON lines, each with the node's identity:
// starting (with the drain's budget), serving, draining, each phase of
// the drain (delay, shutdown, settle, flush) with how long it took, and
// stopped; the drain logged nothing before (a silent 3.8 s exit).
func TestMainLifecycleLogs(t *testing.T) {
	t.Setenv("GRIST_NODE", "pod-7")
	ctx, cancel := context.WithCancel(context.Background())
	errOut, _, done := serving(t, ctx, apiApp(), t.TempDir(), []string{"-addr", "127.0.0.1:0"}, grist.WithDrain(grist.Drain{Delay: -1}))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, l := range lifecycleLines(t, errOut.String()) {
		msg, _ := l["msg"].(string)
		if p, ok := l["phase"].(string); ok {
			msg += "." + p
		}
		msgs = append(msgs, msg)
		if l["app"] != "names" || l["node"] != "pod-7" || l["incarnation"] == "" || l["incarnation"] == nil {
			t.Errorf("a lifecycle line without the node's identity: %v", l)
		}
	}
	want := []string{"starting", "serving", "draining", "drain.delay", "drain.shutdown", "drain.settle", "drain.flush", "stopped"}
	if strings.Join(msgs, " ") != strings.Join(want, " ") {
		t.Errorf("lifecycle lines %v, want %v:\n%s", msgs, want, errOut.String())
	}
}

// /metrics counts the API's requests by kind, block and status code (an
// error is its code: 422 a refusal, 404 an unknown block, counted as
// other) and their durations as a histogram, beside the Go runtime's
// gauges and the build's info.
func TestMainRequestMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errOut, url, done := serving(t, ctx, apiApp(), t.TempDir(), []string{"-addr", "127.0.0.1:0", "-health-addr", "127.0.0.1:0"}, grist.WithDrain(grist.Drain{Delay: -1}))
	c := &client.Client{App: apiApp(), URL: url, CallerHeader: "X-Caller"}
	for i, name := range []string{"a", "a", "b"} {
		c.Command(ctx, grist.Envelope{ID: "k" + string(rune('1'+i)), Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"`+name+`"}`))
	}
	c.Query(ctx, grist.Envelope{Tenant: "t1", Caller: "editor"}, "names", []byte(`{"name":"a"}`))
	get(t, url+"/t/t1/commands/nothing")
	_, m := get(t, healthURL(t, errOut)+"/metrics")
	for _, want := range []string{
		`grist_requests_total{kind="command",block="name",code="200"} 2`,
		`grist_requests_total{kind="command",block="name",code="422"} 1`,
		`grist_requests_total{kind="query",block="names",code="200"} 1`,
		`grist_requests_total{kind="other",block="other",code="`,
		`grist_request_duration_seconds_count{kind="command",block="name"} 3`,
		`grist_request_duration_seconds_bucket{kind="command",block="name",le="+Inf"} 3`,
		"# TYPE grist_request_duration_seconds histogram",
		"go_goroutines ",
		"go_memstats_heap_alloc_bytes ",
		`grist_build_info{path="`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("/metrics has no %q:\n%s", want, m)
		}
	}
	cancel()
	<-done
}

// steps -export writes the store's committed steps as JSONL from the
// step log, as a relay records them: a pod's lost steps file rebuilt.
func TestMainStepsExport(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	_, url, done := serving(t, ctx, apiApp(), dir, []string{"-addr", "127.0.0.1:0"}, grist.WithDrain(grist.Drain{Delay: -1}))
	c := &client.Client{App: apiApp(), URL: url, CallerHeader: "X-Caller"}
	for i, name := range []string{"a", "a", "b"} {
		c.Command(ctx, grist.Envelope{ID: "k" + string(rune('1'+i)), Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"`+name+`"}`))
	}
	cancel()
	<-done
	var out bytes.Buffer
	db := "sqlite:" + filepath.Join(dir, "app.db")
	if err := grist.MainArgs(context.Background(), apiApp(), []string{"steps", "-export", "-db", db}, &out, &out); err != nil {
		t.Fatal(err)
	}
	steps, err := qtrace.ReadJSONL(&out)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range steps {
		got = append(got, s.Action+":"+s.Outcome+":"+s.Recorder)
	}
	want := "name:ok:grist/names/t1 name:taken:grist/names/t1 name:ok:grist/names/t1"
	if strings.Join(got, " ") != want {
		t.Errorf("steps -export: %v, want %s", got, want)
	}
	out.Reset()
	if err := grist.MainArgs(context.Background(), apiApp(), []string{"steps", "-export", "-db", db, "-after", "2"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if steps, _ := qtrace.ReadJSONL(&out); len(steps) != 1 {
		t.Errorf("steps -export -after 2: %d steps, want the third commit's alone", len(steps))
	}
}

// A log that goes backwards under a running node (a restore made with SQL
// on its SQLite file: every row after the first commit deleted) is seen
// (ErrRewound): its relay records gristRewound, /metrics says
// grist_rewound 1 for the tenant, the lifecycle log has the error;
// commands go on; `resume` lets the processors go on, recorded as
// gristResumed. prune deletes old step rows and markers, the last row of
// a tenant kept.
func TestMainRewindResumePrune(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	steps := filepath.Join(dir, "steps.jsonl")
	errOut, url, done := serving(t, ctx, apiApp(), dir, []string{"-addr", "127.0.0.1:0", "-health-addr", "127.0.0.1:0", "-steps", steps}, grist.WithDrain(grist.Drain{Delay: -1}))
	c := &client.Client{App: apiApp(), URL: url, CallerHeader: "X-Caller"}
	for i, name := range []string{"a", "b", "c"} {
		if _, err := c.Command(ctx, grist.Envelope{ID: "k" + string(rune('1'+i)), Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"`+name+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	hu := healthURL(t, errOut)
	time.Sleep(100 * time.Millisecond) // the relay records the three
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(filepath.Join(dir, "app.db"), 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{`DELETE FROM grist_events WHERE pos > 1`, `DELETE FROM grist_index WHERE pos > 1`, `DELETE FROM grist_markers WHERE pos > 1`,
		`DELETE FROM grist_steps WHERE seq > 1`, `UPDATE grist_heads SET head = 1, steps = 1`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, m := get(t, hu+"/metrics"); strings.Contains(m, `grist_rewound{tenant="t1"} 1`) {
			break
		}
		if time.Now().After(deadline) {
			_, m := get(t, hu+"/metrics")
			t.Fatalf("no grist_rewound 1 after the restore:\n%s\n%s", m, errOut.String())
		}
	}
	if !regexp.MustCompile(`"msg":"rewound".*"tenant":"t1"`).MatchString(errOut.String()) {
		t.Errorf("no rewound line in the lifecycle log:\n%s", errOut.String())
	}
	if _, err := c.Command(ctx, grist.Envelope{ID: "k4", Tenant: "t1", Caller: "editor"}, "name", []byte(`{"name":"d"}`)); err != nil {
		t.Errorf("a command after the rewind: %v (commands go on)", err)
	}
	var out bytes.Buffer
	dbFlag := "sqlite:" + filepath.Join(dir, "app.db")
	if err := grist.MainArgs(ctx, apiApp(), []string{"resume", "-db", dbFlag, "-tenant", "t1"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, m := get(t, hu+"/metrics"); strings.Contains(m, `grist_rewound{tenant="t1"} 0`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still rewound after resume: %s", out.String())
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, s := range readFile(t, steps) {
		if strings.HasPrefix(s.Action, "grist") {
			actions = append(actions, s.Action)
		}
	}
	if strings.Join(actions, " ") != "gristRewound gristResumed" {
		t.Errorf("the steps file's grist steps: %v, want the rewind and the resume", actions)
	}
	// prune: everything older than now, but each tenant's last step row
	out.Reset()
	if err := grist.MainArgs(context.Background(), apiApp(), []string{"prune", "-db", dbFlag, "-steps", "1ns", "-markers", "1ns"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	var rows, markers int
	db.QueryRow(`SELECT COUNT(*) FROM grist_steps`).Scan(&rows)
	db.QueryRow(`SELECT COUNT(*) FROM grist_markers`).Scan(&markers)
	if rows != 1 || markers != 0 || !strings.Contains(out.String(), "pruned") {
		t.Errorf("after prune: %d step rows, %d markers (%s); want the tenant's last row and no marker", rows, markers, out.String())
	}
	if err := grist.MainArgs(context.Background(), apiApp(), []string{"prune", "-db", dbFlag}, &out, &out); !errors.As(err, new(*grist.UsageError)) {
		t.Errorf("prune with no horizon: %v, want a usage error", err)
	}
}
