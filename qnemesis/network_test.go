package qnemesis

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
)

// toxiproxy starts a toxiproxy-server for the test, or skips it.
func toxiproxy(t *testing.T) *Toxiproxy {
	t.Helper()
	bin, err := exec.LookPath("toxiproxy-server")
	if err != nil {
		t.Skip("toxiproxy-server not on PATH (go install github.com/Shopify/toxiproxy/v2/cmd/server@latest, renamed toxiproxy-server)")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command(bin, "-host", "127.0.0.1", "-port", fmt.Sprint(port))
	srv := &Process{Name: "toxiproxy", Cmd: func(int) *exec.Cmd { return cmd }}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	tp := &Toxiproxy{URL: fmt.Sprintf("http://127.0.0.1:%d", port)}
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if resp, err := http.Get(tp.URL + "/version"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("toxiproxy-server did not start")
		}
	}
	t.Cleanup(func() { _ = tp.Close() })
	return tp
}

// timed GETs and says how long it took.
func timed(addr string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	got, err := get(addr, "/", timeout)
	if err == nil && got != "ok" {
		err = fmt.Errorf("got %q", got)
	}
	return time.Since(start), err
}

// Toxiproxy's link cuts and heals (the same address again), delays
// responses and loses them, and clears; Close deletes its proxies.
func TestToxiproxy(t *testing.T) {
	t.Parallel()
	tp := toxiproxy(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer up.Close()
	if _, err := tp.Link("client.1", "server", "x"); err == nil || !strings.Contains(err.Error(), "cannot contain a dot") {
		t.Errorf("a name with a dot: %v", err)
	}
	addr, err := tp.Link("client", "server", strings.TrimPrefix(up.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if got := tp.Pairs(); len(got) != 1 || got[0] != [2]string{"client", "server"} {
		t.Fatalf("pairs %v", got)
	}
	ctx := context.Background()
	if _, err := timed(addr, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := tp.Cut(ctx, "server", "client"); err != nil {
		t.Fatal(err)
	}
	if _, err := timed(addr, time.Second); err == nil {
		t.Fatal("a cut link answered")
	}
	if err := tp.Heal(ctx, "client", "server"); err != nil {
		t.Fatal(err)
	}
	if _, err := timed(addr, 2*time.Second); err != nil {
		t.Fatalf("after the heal: %v", err)
	}
	if err := tp.Latency(ctx, "client", "server", 300*time.Millisecond, 0); err != nil {
		t.Fatal(err)
	}
	if d, err := timed(addr, 5*time.Second); err != nil || d < 250*time.Millisecond {
		t.Fatalf("with 300ms latency: %v, %v", d, err)
	}
	if err := tp.Loss(ctx, "client", "server", 1, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := timed(addr, 5*time.Second); err == nil {
		t.Fatal("a response lost was answered")
	}
	if err := tp.Clear(ctx, "client", "server"); err != nil {
		t.Fatal(err)
	}
	if d, err := timed(addr, 5*time.Second); err != nil || d > 250*time.Millisecond {
		t.Fatalf("cleared: %v, %v", d, err)
	}
	if err := tp.Cut(ctx, "client", "nobody"); err == nil || !strings.Contains(err.Error(), "Toxiproxy.Link") {
		t.Errorf("a cut of no link: %v", err)
	}
	if err := tp.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := timed(addr, time.Second); err == nil {
		t.Fatal("a closed link answered")
	}
}

// The nemesis over toxiproxy: an isolate cuts every link of a name,
// recorded once in effect, and Stop heals them.
func TestNemesisOverToxiproxy(t *testing.T) {
	t.Parallel()
	tp := toxiproxy(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer up.Close()
	host := strings.TrimPrefix(up.URL, "http://")
	a1, err := tp.Link("c1", "server", host)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := tp.Link("c2", "server", host)
	if err != nil {
		t.Fatal(err)
	}
	mem := &qobs.MemorySink{}
	n := &Nemesis{Net: tp, Rec: qobs.NewRecorder(mem, qobs.WithID("nemesis"))}
	ctx := context.Background()
	if err := n.Run(ctx, Schedule{{Kind: "nemesis.isolate.server"}}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{a1, a2} {
		if _, err := timed(a, time.Second); err == nil {
			t.Fatal("an isolated server answered")
		}
	}
	if err := n.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{a1, a2} {
		if _, err := timed(a, 2*time.Second); err != nil {
			t.Fatalf("after Stop: %v", err)
		}
	}
	var got []string
	for _, s := range mem.Steps() {
		got = append(got, fmt.Sprintf("%s %v", s.Action, s.Args))
	}
	if want := "isolate map[node:server] heal map[a:c1 b:server] heal map[a:c2 b:server]"; strings.Join(got, " ") != want {
		t.Fatalf("recorded %s;\nwant %s", strings.Join(got, " "), want)
	}
}
