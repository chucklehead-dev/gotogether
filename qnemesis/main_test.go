package qnemesis

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// TestMain runs the test binary as a child process when QNEMESIS_CHILD
// says so: the system the nemesis kills, pauses and restarts.
func TestMain(m *testing.M) {
	switch os.Getenv("QNEMESIS_CHILD") {
	case "server":
		serve()
		return
	case "sleep":
		time.Sleep(time.Hour)
		return
	}
	os.Exit(m.Run())
}

// serve is a tiny server recording its steps to a PerStep log of its
// incarnation's (QNEMESIS_DIR/<name>.<incarnation>.jsonl): a start step,
// an inc step per GET /inc (its count since the incarnation started),
// ticks when QNEMESIS_TICK is set (a duration; 0: as fast as it can), and
// a torn line on GET /tear. It writes its address to <name>.<inc>.addr.
func serve() {
	dir, name, inc := os.Getenv("QNEMESIS_DIR"), os.Getenv("QNEMESIS_NAME"), os.Getenv("QNEMESIS_INC")
	base := filepath.Join(dir, name+"."+inc)
	sink, err := qobs.FileSink(base+".jsonl", qobs.PerStep)
	if err != nil {
		panic(err)
	}
	rec := qobs.NewRecorder(sink, qobs.WithID(name+"-"+inc), qobs.WithProcess(name+"#"+inc))
	ctx := context.Background()
	if os.Getenv("QNEMESIS_GRANDCHILD") != "" {
		// a worker in the incarnation's process group: a crash must take it too
		exe, _ := os.Executable()
		gc := exec.Command(exe, "-test.run=^$")
		gc.Env = append(os.Environ(), "QNEMESIS_CHILD=sleep")
		if err := gc.Start(); err != nil {
			panic(err)
		}
		writeFile(base+".grandchild", strconv.Itoa(gc.Process.Pid))
	}
	addr := os.Getenv("QNEMESIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}
	rec.Record(ctx, "start", nil, "node", name)
	var count atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/inc", func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		rec.Record(ctx, "inc", nil, "node", name, "obs.count", n)
		fmt.Fprint(w, n)
	})
	mux.HandleFunc("/tear", func(w http.ResponseWriter, r *http.Request) {
		// half a step, as a writer killed mid-line leaves it
		f, err := os.OpenFile(base+".jsonl", os.O_APPEND|os.O_WRONLY, 0)
		if err == nil {
			_, _ = f.WriteString(`{"quint.action":"inc","quint.se`)
			f.Close()
		}
		fmt.Fprint(w, "torn")
	})
	if d, ok := os.LookupEnv("QNEMESIS_TICK"); ok {
		every, _ := time.ParseDuration(d)
		go func() {
			for {
				rec.Record(ctx, "tick", nil, "node", name)
				if every > 0 {
					time.Sleep(every)
				}
			}
		}()
	}
	writeFile(base+".addr", l.Addr().String())
	_ = http.Serve(l, mux)
}

// writeFile writes atomically: a reader polling for it never sees half.
func writeFile(path, s string) {
	if err := os.WriteFile(path+".tmp", []byte(s), 0o644); err != nil {
		panic(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		panic(err)
	}
}

// child is a Process running serve, killed when the test ends.
func child(t *testing.T, dir, name string, env ...string) *Process {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &Process{Name: name, Cmd: func(inc int) *exec.Cmd {
		cmd := exec.Command(exe, "-test.run=^$")
		cmd.Env = append(os.Environ(), append([]string{"QNEMESIS_CHILD=server", "QNEMESIS_DIR=" + dir, "QNEMESIS_NAME=" + name, "QNEMESIS_INC=" + strconv.Itoa(inc)}, env...)...)
		cmd.Stderr = os.Stderr
		return cmd
	}}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// addrOf waits for the incarnation's address.
func addrOf(t *testing.T, dir, name string, inc int) string {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("%s.%d.addr", name, inc))
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil {
			return string(b)
		}
	}
	t.Fatalf("%s incarnation %d never wrote its address", name, inc)
	return ""
}

// get GETs a path, with a timeout, on a fresh connection.
func get(addr, path string, timeout time.Duration) (string, error) {
	c := &http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := c.Get("http://" + addr + path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b := make([]byte, 64)
	n, _ := resp.Body.Read(b)
	return string(b[:n]), nil
}

// readLog reads a step log, a torn tail allowed.
func readLog(t *testing.T, path string) ([]qtrace.Step, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return qtrace.ReadJSONL(f)
}

// nemesisRec is a nemesis recorder on a PerStep log in dir.
func nemesisRec(t *testing.T, dir string) (*qobs.Recorder, string) {
	t.Helper()
	path := filepath.Join(dir, "nemesis.jsonl")
	rec, sink, err := NewRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	return rec, path
}
