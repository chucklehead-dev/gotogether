package grist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"
	"github.com/chucklehead-dev/gotogether/grist/internal/gen"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qobs/qobshttp"
)

// Main is an application binary's entry point: func main() {
// grist.Main(app, opts...) }, the binary importing its database's driver
// (modernc.org/sqlite, github.com/jackc/pgx/v5/stdlib: grist imports
// none). It runs the command its arguments name, stopping on SIGINT or
// SIGTERM, and exits 2 on a usage error, 1 on any other. A second
// signal, in the drain, ends the process at once (the signals' default).
// The commands:
//
//	catalog    App.Catalog as JSON on stdout
//	verify     App.Verify and App.CheckUpcasters
//	model      the model's skeleton and bindings, generated from the App's catalog
//	           (docs/grist.md §3.2), written under -dir (default .), or with -check
//	           compared with those there (an error naming each that differs);
//	           -init writes the app's first model/<app>_rules.qnt (every hole
//	           stubbed) and model/<app>.qnt (no invariant yet)
//	           where there are none.
//	           It lists the holes the app's model has not filled
//	sizing     runs <app>-sizing, the app's sizing binary (gristload.Main), with
//	           the arguments after it: found beside this binary, else on PATH
//	           (qload, qmeta and gristsim stay out of the app's binary, which
//	           links none of them); its exit status is sizing's
//	serve      the HTTP API (NewHandler, opts applied) on grist/sqlstore over -db
//	           (sqlite:PATH, postgres:DSN), the node (App.Start: relay, pollers,
//	           the tenant watcher) beside it, every step recorded to -steps
//	           (qobs.FileSink, PerStep: a new file each time; in a directory, a
//	           file per node and start) for validation with the app's bindings,
//	           under the recorder <app>/<node>/<incarnation> (Identity, from the
//	           environment or WithIdentity); /livez, /readyz and /metrics on
//	           -health-addr; until SIGINT or SIGTERM, then the drain (Drain, WithDrain):
//	           /readyz fails and the server keeps serving for its Delay, then
//	           drains its requests (Shutdown) while the node keeps running,
//	           then, once its processors are idle and its relay caught up
//	           (Settle), a settle is recorded (qobs.Settled: the bindings'
//	           eventual vals are judged on the file), then the node stops and
//	           the relay records every committed step (Relay.Flush, Flush)
//	           before the file closes, the phases budgeted into -grace when
//	           given. A -dependency NAME=URL calls the App's dependency NAME at
//	           URL (OverHTTP), in place of what Dependencies or ServeDependency
//	           attach. With GOMEMLIMIT unset, the runtime's memory limit is 90%
//	           of the cgroup's memory.max
//
// serve writes its startup line to stderr ("<app>: node N, incarnation I;
// drain needs a grace of ...; GOMEMLIMIT ..."), then "<app> health on
// http://ADDR" when the side port is on, and "<app> serving on
// http://ADDR" once it listens.
func Main(app *App, opts ...ServeOption) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// the first signal starts the drain; the next is the signals' own
	// again: an operator's second Ctrl-C ends a 35 s drain, where it was
	// swallowed (relayed to a context already cancelled)
	context.AfterFunc(ctx, stop)
	err := mainArgs(ctx, app, os.Args[1:], os.Stdout, os.Stderr, opts...)
	stop()
	var usage *usageError
	var status exitStatus
	switch {
	case errors.As(err, &status):
		os.Exit(int(status))
	case errors.As(err, &usage):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// A usageError is a command line Main cannot run.
type usageError struct{ Msg string }

func (e *usageError) Error() string { return e.Msg }

// An exitStatus is a command's exit status, passed through (a helper
// binary's: sizing's).
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// mainUsage is the commands' summary.
const mainUsage = `commands:
  catalog [-diff DEPLOYED]    print the App's registry as JSON (App.Catalog)
      -diff DEPLOYED                   compare it with the deployed release's (its catalog's JSON): safe, readers first
                                       or breaking, each change with its fix; exit status 0, 3 (readers first) or 4 (breaking)
  verify                      check the registry (App.Verify), as the node does at boot, and every upcaster (App.CheckUpcasters)
  model [-dir DIR] [-check] [-init]   write the model's skeleton and the bindings under DIR (default .)
      -check                           write nothing: fail if a generated file differs (CI's diff)
      -init                            also write the app's first model/<app>_rules.qnt and model/<app>.qnt where there are none
  sizing [flags]              run <app>-sizing (gristload.Main: deploy/sizing.yaml from the app's steps), a binary of its own
  serve -db DB [flags]        serve the App's HTTP API (NewHandler) and run its node (App.Start) on DB
      -db sqlite:PATH | postgres:DSN   the store (grist/sqlstore; the binary imports the driver)
      -addr HOST:PORT                  where to listen (default 127.0.0.1:8080; :0 picks a port; on loopback in a pod, refused)
      -health-addr HOST:PORT           /livez, /readyz and /metrics on a side port (default :8081 in a pod, else off)
      -grace DURATION                  the orchestrator's kill deadline after SIGTERM: the drain is budgeted into it
      -tenant NAME                     create the tenant if it does not exist (repeatable)
      -steps PATH                      record every step to PATH (qobs.FileSink, PerStep), for validation;
                                       a directory: a file of its own per node and start, <app>.<node>.<incarnation>.jsonl
                                       (in a pod give a directory: a restart truncates a file, losing the earlier start's steps)
      -caller-header NAME              take the request's caller from header NAME (behind an authenticating proxy only)
      -poll DURATION                   how often the store reads other processes' appends (default 250ms)
      -dependency NAME=URL             call dependency NAME over HTTP at URL (grist.OverHTTP; repeatable)`

// mainArgs runs one of Main's commands, args the binary's arguments
// after its name (Main says what each does).
func mainArgs(ctx context.Context, app *App, args []string, stdout, stderr io.Writer, opts ...ServeOption) error {
	if len(args) == 0 {
		return &usageError{Msg: fmt.Sprintf("usage: %s <command>\n%s", app.Name, mainUsage)}
	}
	switch args[0] {
	case "catalog":
		return catalogMain(app, args[1:], stdout)
	case "verify":
		if err := app.Verify(); err != nil {
			return err
		}
		if err := app.CheckUpcasters(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s verifies\n", app.Name)
		return nil
	case "model":
		return modelMain(app, args[1:], stdout)
	case "serve":
		return serveMain(ctx, app, args[1:], stderr, opts)
	case "sizing":
		return helperMain(ctx, app, "sizing", args[1:], stdout, stderr)
	case "help", "-h", "-help", "--help":
		fmt.Fprintf(stdout, "usage: %s <command>\n%s\n", app.Name, mainUsage)
		return nil
	}
	return &usageError{Msg: fmt.Sprintf("%s: unknown command %q\n%s", app.Name, args[0], mainUsage)}
}

// helperMain runs the app's helper binary for command name,
// <app>-<name>, with args: the one beside the running binary, else the
// one on PATH, its output the command's and its exit status passed
// through (exitStatus). The tooling a helper links (sizing's qload,
// qmeta and gristsim) stays out of the app's binary, which an operator
// deploys (docs/grist-platform.md decision 4's pattern: a binary of its
// own, found by name, as gotogether nav finds gotogether-nav).
func helperMain(ctx context.Context, app *App, name string, args []string, stdout, stderr io.Writer) error {
	bin := app.Name + "-" + name
	path := ""
	if self, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(self), bin); isExecutable(p) {
			path = p
		}
	}
	if path == "" {
		p, err := exec.LookPath(bin)
		if err != nil {
			return fmt.Errorf("%s %s: no %s beside this binary or on PATH: %s is a binary of its own, so that this one links none of its tooling: "+
				"write cmd/%s/main.go as `func main() { gristload.Main(<your package>.App) }` (grist/gristload) and go install it, or go build -o it beside %s",
				app.Name, name, bin, bin, bin, app.Name)
		}
		path = p
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exitStatus(exit.ExitCode())
		}
		return fmt.Errorf("%s %s: %s: %w", app.Name, name, path, err)
	}
	return nil
}

// isExecutable reports whether path is a regular file someone may run.
func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// catalogMain is the catalog command: the catalog's JSON, or with -diff
// its comparison with a deployed release's (docs/grist-platform.md
// §3.9), the exit status saying how it may roll out.
func catalogMain(app *App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet(app.Name+" catalog", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	deployed := fs.String("diff", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return &usageError{Msg: fmt.Sprintf("usage: %s catalog [-diff DEPLOYED.json]\n%s", app.Name, mainUsage)}
	}
	b, err := json.MarshalIndent(app.Catalog(), "", "  ")
	if err != nil {
		return err
	}
	if *deployed == "" {
		_, err = fmt.Fprintf(stdout, "%s\n", b)
		return err
	}
	old, err := os.ReadFile(*deployed)
	if err != nil {
		return fmt.Errorf("%s catalog -diff: %w (write it with the deployed release's binary: `%s catalog > deployed.json`)", app.Name, err, app.Name)
	}
	d, err := catalog.CompareCatalogs(old, b)
	if err != nil {
		return fmt.Errorf("%s catalog -diff %s: %w", app.Name, *deployed, err)
	}
	fmt.Fprint(stdout, d)
	switch d.Verdict() {
	case catalog.ReadersFirst:
		return exitStatus(3)
	case catalog.Breaking:
		return exitStatus(4)
	}
	return nil
}

// modelMain is the model command (the generator's, grist/internal/gen).
func modelMain(app *App, args []string, stdout io.Writer) error {
	return gen.Command(app, args, stdout, func(msg string) error { return &usageError{Msg: msg} })
}

// names is a repeatable flag.
type names []string

func (n *names) String() string     { return strings.Join(*n, ",") }
func (n *names) Set(v string) error { *n = append(*n, v); return nil }

// openDB opens -db's database and its dialect.
func openDB(spec string) (*sql.DB, sqlstore.Dialect, error) {
	kind, dsn, ok := strings.Cut(spec, ":")
	if !ok || dsn == "" {
		return nil, sqlstore.Dialect{}, &usageError{Msg: fmt.Sprintf("-db %q: give sqlite:PATH or postgres:DSN", spec)}
	}
	var driver, imp string
	var dialect sqlstore.Dialect
	switch kind {
	case "sqlite":
		driver, dialect, imp = "sqlite", sqlstore.SQLite, "modernc.org/sqlite"
		dsn = sqlstore.SQLiteDSN(dsn, 5*time.Second)
	case "postgres", "postgresql":
		driver, dialect, imp = "pgx", sqlstore.Postgres, "github.com/jackc/pgx/v5/stdlib"
		if kind == "postgresql" || strings.HasPrefix(dsn, "//") {
			dsn = kind + ":" + dsn // a URL: postgres://...
		}
	default:
		return nil, sqlstore.Dialect{}, &usageError{Msg: fmt.Sprintf("-db %q: unknown database %q: give sqlite:PATH or postgres:DSN", spec, kind)}
	}
	registered := false
	for _, d := range sql.Drivers() {
		registered = registered || d == driver
	}
	if !registered {
		return nil, sqlstore.Dialect{}, fmt.Errorf("-db %s: no %q database/sql driver in this binary: import _ %q in its main package", kind, driver, imp)
	}
	db, err := sql.Open(driver, dsn)
	return db, dialect, err
}

func serveMain(ctx context.Context, app *App, args []string, stderr io.Writer, opts []ServeOption) error {
	fs := flag.NewFlagSet(app.Name+" serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbSpec := fs.String("db", "", "")
	addr := fs.String("addr", "127.0.0.1:8080", "")
	steps := fs.String("steps", "", "")
	callerHeader := fs.String("caller-header", "", "")
	poll := fs.Duration("poll", 0, "")
	healthAddr := fs.String("health-addr", "", "")
	grace := fs.Duration("grace", 0, "")
	var tenants, depURLs names
	fs.Var(&tenants, "tenant", "")
	fs.Var(&depURLs, "dependency", "")
	if err := fs.Parse(args); err != nil {
		return &usageError{Msg: fmt.Sprintf("%s serve: %v\n%s", app.Name, err, mainUsage)}
	}
	if *dbSpec == "" || fs.NArg() > 0 {
		return &usageError{Msg: fmt.Sprintf("%s serve: give -db (and only flags)\n%s", app.Name, mainUsage)}
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	orchestrator := onOrchestrator()
	if orchestrator && loopback(*addr) {
		// a Service's endpoints are the pod's IP: a server on loopback has
		// none, and every request to it is refused
		return &usageError{Msg: fmt.Sprintf("%s serve: -addr %s listens on loopback, which no Service reaches (KUBERNETES_SERVICE_HOST is set: this is a pod): give -addr :8080", app.Name, *addr)}
	}
	if *grace < 0 {
		return &usageError{Msg: fmt.Sprintf("%s serve: -grace %v: give the orchestrator's kill deadline (terminationGracePeriodSeconds), as -grace 30s", app.Name, *grace)}
	}
	if !set["health-addr"] && orchestrator {
		*healthAddr = ":8081"
	}
	if err := app.Verify(); err != nil {
		return err
	}
	overHTTP := map[*Dependency]Transport{}
	for _, v := range depURLs {
		name, u, ok := strings.Cut(v, "=")
		d := app.deps[name]
		switch {
		case !ok || u == "":
			return &usageError{Msg: fmt.Sprintf("-dependency %q: give NAME=URL", v)}
		case d == nil:
			return &usageError{Msg: fmt.Sprintf("-dependency %q: %s has no dependency %q (it has %v)", v, app.Name, name, depNames(app))}
		}
		// checked here: a URL no request can be made to would leave every
		// call's outcome unknown (an at-most-once effect's grist.unknown)
		if pu, err := url.Parse(u); err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
			return &usageError{Msg: fmt.Sprintf("-dependency %q: give NAME=URL, the URL http:// or https:// with a host (as http://bank.internal:8080)", v)}
		}
		overHTTP[d] = OverHTTP(u, nil)
	}
	// the signal's context, for Terminating in the node's and the
	// requests' contexts, which keep ctx's values but not its cancellation
	ctx = context.WithValue(ctx, signalKey{}, ctx)
	var cfg serveConfig
	for _, o := range opts {
		if o.apply != nil {
			o.apply(&cfg)
		}
	}
	id := NewIdentity()
	if cfg.identity != nil {
		id = cfg.identity.resolve(id)
	}
	drain := cfg.drain
	if set["grace"] {
		drain.Grace = *grace
	}
	drain = drain.withDefaults(orchestrator)
	mem := setMemoryLimit(cgroupMemoryMax)
	// the startup line: who this node is, what its drain needs of the
	// orchestrator's grace, and its memory limit
	fmt.Fprintf(stderr, "%s: %s; %s; %s\n", app.Name, id, drain.line(), mem)
	var hs *health
	if *healthAddr != "" {
		// first, before the store: /livez answers while the store is down,
		// /readyz says not yet
		hs = &health{app: app}
		hln, err := net.Listen("tcp", *healthAddr)
		if err != nil {
			return err
		}
		hsrv := &http.Server{Handler: hs, ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second}
		go hsrv.Serve(hln) //qenv:ok the real tier's health server, outside any simulation
		defer hsrv.Close() // last: live, and not ready, through the drain
		fmt.Fprintf(stderr, "%s health on http://%s (/livez, /readyz, /metrics)\n", app.Name, hln.Addr())
	}
	db, dialect, err := openDB(*dbSpec)
	if err != nil {
		return err
	}
	defer db.Close()
	st, err := sqlstore.Open(ctx, db, dialect, sqlstore.Options{Name: app.Name, Poll: *poll})
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if err := CheckTenant(t); err != nil {
			return &usageError{Msg: fmt.Sprintf("-tenant: %v", err)}
		}
		if err := st.CreateTenant(ctx, t); err != nil {
			return err
		}
	}
	var sink qobs.Sink
	// <app>/<node>/<incarnation>: never the pid, which is 1 in every
	// container (two pods' files would number one recorder twice)
	recorderID := id.recorderID(app.Name)
	if *steps != "" {
		path := *steps
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			// a directory (a volume the pods share): a file per incarnation
			path = filepath.Join(path, strings.ReplaceAll(recorderID, "/", ".")+".jsonl")
		} else if orchestrator {
			// a restarted container opens the file afresh: the steps of its
			// earlier start, which a merge needs, are gone
			fmt.Fprintf(stderr, "%s: warning: -steps %s is a file: a restart of this container truncates it, losing the earlier start's steps; in a pod give -steps a directory (a file per start)\n", app.Name, *steps)
		}
		fsink, err := qobs.FileSink(path, qobs.PerStep)
		if err != nil {
			return err
		}
		defer fsink.Close() // shared by the recorders below (the node's, each served dependency's): closed once, by its owner
		sink = fsink
		ctx = qobs.WithRecorder(ctx, qobs.NewRecorder(sink, qobs.WithID(recorderID)))
	}
	// the node outlives the signal: its processors, relay and in-flight
	// effects keep running while the server drains its requests (whose
	// commands they handle), and stop after it (stopNode below); a node
	// stopped at the signal cancelled an at-most-once effect's ctx in
	// flight, losing its outcome after the payment was made
	nodeCtx, stopNode := context.WithCancel(context.WithoutCancel(ctx))
	if cfg.deps != nil {
		nodeCtx = cfg.deps(nodeCtx)
	}
	defer stopNode()
	for _, sd := range cfg.served {
		if overHTTP[sd.d] != nil {
			continue // called where -dependency says
		}
		url, stopDep, err := serveLoopback(sd, sink, recorderID)
		if err != nil {
			return err
		}
		// deferred: stopped after serve stops the node, whose effects in
		// flight call it until then
		defer stopDep()
		fmt.Fprintf(stderr, "%s: dependency %s served on %s\n", app.Name, sd.d.name, url)
		nodeCtx = sd.d.With(nodeCtx, OverHTTP(url, nil))
	}
	for d, t := range overHTTP {
		nodeCtx = d.With(nodeCtx, t)
	}
	if *callerHeader != "" {
		opts = append(opts, CallerFromHeader(*callerHeader))
	}
	h, err := NewHandler(app, st, opts...)
	if err != nil {
		return err
	}
	relay, err := NewRelay(st)
	if err != nil {
		return err
	}
	var n *node
	if err := app.Start(nodeCtx, st, WithRelay(relay), StartOption{apply: func(c *startConfig) { c.onNode = func(x *node) { n = x } }}); err != nil {
		return err
	}
	if hs != nil {
		h = hs.count(h)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	if hs != nil {
		// ready once the API listens: the store answered (Start read its
		// tenants), the node runs, and a request the Service sends is not
		// refused (a probe between Start and the listen saw a ready pod
		// whose API port refused connections)
		hs.started(st, relay, n)
	}
	srv := newMainServer(ctx, h)
	fmt.Fprintf(stderr, "%s serving on http://%s\n", app.Name, ln.Addr())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }() //qenv:ok the real tier's server, outside any simulation
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	// the drain (Drain): not ready at once, serving through the router's
	// lag, then each phase bounded, within -grace when given
	if hs != nil {
		hs.draining.Store(true)
	}
	return runDrain(ctx, drain, mainServer{srv: srv, done: done}, st, relay, n, stopNode, true,
		func(msg string) { fmt.Fprintln(stderr, msg) })
}

// mainServer is Main's HTTP server as a drain's.
type mainServer struct {
	srv  *http.Server
	done <-chan error // Serve's end
}

func (m mainServer) keep(ctx context.Context, until time.Time) error {
	if d := time.Until(until); d > 0 { //qenv:ok the real tier's drain, outside any simulation
		select {
		case <-time.After(d): //qenv:ok the real tier's drain, outside any simulation
		case err := <-m.done:
			return err
		}
	}
	return nil
}

func (m mainServer) shutdown(ctx context.Context, until time.Time) error {
	// the shutdown's context keeps ctx's recorder (the flush records into
	// it) but not its cancellation
	shut, cancel := phase(ctx, until)
	defer cancel()
	if err := m.srv.Shutdown(shut); err != nil {
		return fmt.Errorf("%w: %w", errShutdownCut, err)
	}
	return nil
}

// loopback reports whether addr (HOST:PORT) listens on a loopback
// address only.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// serveLoopback serves sd behind ServeCalls on a loopback port, its site's
// records into sink under a recorder of their own (none without a sink),
// and returns its URL and how to stop it.
func serveLoopback(sd servedDep, sink qobs.Sink, recorderID string) (string, func(), error) {
	h := ServeCalls(sd.d, sd.h)
	if sink != nil {
		h = qobshttp.Handler(h, qobs.NewRecorder(sink, qobs.WithID(recorderID+"/"+sd.d.name)))
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln) //qenv:ok the real tier's dependency, outside any simulation
	return "http://" + ln.Addr().String(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		srv.Shutdown(ctx)
	}, nil
}

// depNames are app's dependencies' names, in registration order.
func depNames(app *App) []string {
	var out []string
	for _, d := range app.depOrder {
		out = append(out, d.name)
	}
	return out
}

// Main's drain's default bounds (Drain): how long a graceful shutdown
// waits for the requests in flight, then how long for the node to quiet
// before it records a settle, and then how long the relay's flush may
// take. With the drain's delay (5 s) on an orchestrator they need 35 s, more than
// Kubernetes' default grace of 30 s: give the pod 45 s
// (terminationGracePeriodSeconds), or -grace to budget them into less.
const (
	shutdownTimeout = 10 * time.Second
	settleTimeout   = 10 * time.Second
	flushTimeout    = 10 * time.Second
)

// newMainServer is serve's http.Server for h: requests run with ctx's
// values (its recorder, so a query's step and a refusal's on-return step
// reach the node's file) but not its cancellation, and every phase of a
// connection is bounded, so a slow or idle client holds no connection
// for ever: its headers in 10 s, its whole request in 1 m (a body
// trickled past it is cut), its answer written within 2 m of the
// request's start (a command's transient retries take well under it), an
// idle keep-alive connection kept 2 m, headers at most 64 KiB (the body
// is the handler's MaxBody).
func newMainServer(ctx context.Context, h http.Handler) *http.Server {
	return &http.Server{Handler: h, BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, WriteTimeout: 2 * time.Minute,
		IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
}
