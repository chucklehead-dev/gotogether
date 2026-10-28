package grist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qobs/qobshttp"
)

// Main is an application binary's entry point: func main() {
// grist.Main(app, opts...) }, the binary importing its database's driver
// (modernc.org/sqlite, github.com/jackc/pgx/v5/stdlib: grist imports
// none). It runs MainArgs on the process's arguments, stopping on SIGINT
// or SIGTERM, and exits 2 on a usage error, 1 on any other. A second
// signal, in the drain, ends the process at once (the signals' default).
func Main(app *App, opts ...ServeOption) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// the first signal starts the drain; the next is the signals' own
	// again: an operator's second Ctrl-C ends a 35 s drain, where it was
	// swallowed (relayed to a context already cancelled)
	context.AfterFunc(ctx, stop)
	err := MainArgs(ctx, app, os.Args[1:], os.Stdout, os.Stderr, opts...)
	stop()
	var usage *UsageError
	switch {
	case errors.As(err, &usage):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// A UsageError is a command line MainArgs cannot run.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// mainUsage is the commands' summary.
const mainUsage = `commands:
  catalog                     print the App's registry as JSON (App.Catalog)
  verify                      check the registry (App.Verify), as the node does at boot
  model [-dir DIR] [-check] [-init]   write the model's skeleton and the bindings (Generate) under DIR (default .)
      -check                           write nothing: fail if a generated file differs (CI's diff)
      -init                            also write the app's first model/<app>_rules.qnt and model/<app>.qnt (Starter) where there are none
  serve -db DB [flags]        serve the App's HTTP API (NewHandler) and run its node (App.Start) on DB
      -db sqlite:PATH | postgres:DSN   the store (grist/sqlstore; the binary imports the driver)
      -addr HOST:PORT                  where to listen (default 127.0.0.1:8080; :0 picks a port; on loopback in a pod, refused)
      -health-addr HOST:PORT           /livez, /readyz and /metrics on a side port (default :8081 in a pod, else off)
      -grace DURATION                  the orchestrator's kill deadline after SIGTERM: the drain is budgeted into it
      -tenant NAME                     create the tenant if it does not exist (repeatable)
      -steps PATH                      record every step to PATH (qobs.FileSink, PerStep), for validation;
                                       a directory: a file of its own per node and start, <app>.<node>.<incarnation>.jsonl
                                       (in a pod give a directory: a restart truncates a file, losing the earlier start's steps)
      -actor-header NAME               take the request's actor from header NAME (behind an authenticating proxy only)
      -poll DURATION                   how often the store reads other processes' appends (default 250ms)
      -dependency NAME=URL             call dependency NAME over HTTP at URL (grist.OverHTTP; repeatable)`

// MainArgs runs one of Main's commands, args the binary's arguments
// after its name:
//
//	catalog    App.Catalog as JSON on stdout
//	verify     App.Verify
//	model      Generate's files written under -dir (default .), or with -check
//	           compared with those there (an error naming each that differs);
//	           -init writes Starter's model/<app>_rules.qnt and model/<app>.qnt
//	           where there are none.
//	           It lists the holes the app's model has not filled
//	serve      the HTTP API (NewHandler, opts applied) on grist/sqlstore over -db
//	           (sqlite:PATH, postgres:DSN), the node (App.Start: relay, pollers,
//	           the tenant watcher) beside it, every step recorded to -steps
//	           (qobs.FileSink, PerStep: a new file each time; in a directory, a
//	           file per node and start) for validation with the app's bindings,
//	           under the recorder <app>/<node>/<incarnation> (Identity, from the
//	           environment or WithIdentity); /livez, /readyz and /metrics on
//	           -health-addr; until ctx ends, then the drain (Drain, WithDrain):
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
func MainArgs(ctx context.Context, app *App, args []string, stdout, stderr io.Writer, opts ...ServeOption) error {
	if len(args) == 0 {
		return &UsageError{Msg: fmt.Sprintf("usage: %s <command>\n%s", app.Name, mainUsage)}
	}
	switch args[0] {
	case "catalog":
		b, err := json.MarshalIndent(app.Catalog(), "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "%s\n", b)
		return err
	case "verify":
		if err := app.Verify(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s verifies\n", app.Name)
		return nil
	case "model":
		return modelMain(app, args[1:], stdout)
	case "serve":
		return serveMain(ctx, app, args[1:], stderr, opts)
	case "help", "-h", "-help", "--help":
		fmt.Fprintf(stdout, "usage: %s <command>\n%s\n", app.Name, mainUsage)
		return nil
	}
	return &UsageError{Msg: fmt.Sprintf("%s: unknown command %q\n%s", app.Name, args[0], mainUsage)}
}

// modelMain is the model command.
func modelMain(app *App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet(app.Name+" model", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", ".", "")
	check := fs.Bool("check", false, "")
	starter := fs.Bool("init", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return &UsageError{Msg: fmt.Sprintf("usage: %s model [-dir DIR] [-check] [-init]", app.Name)}
	}
	own := filepath.Join(*dir, modelFile(app.Name))
	rules := filepath.Join(*dir, rulesFile(app.Name))
	if *starter && !*check {
		// an old layout's model is moved by hand, never stubbed beside
		if b, err := readOwn(*dir, modelFile(app.Name)); err != nil {
			return err
		} else if err := oldLayout(app.Name, mustQuint(app.Name), b); err != nil {
			return err
		}
		files, err := Starter(app)
		if err != nil {
			return err
		}
		for _, rel := range []string{rulesFile(app.Name), modelFile(app.Name)} {
			path := filepath.Join(*dir, rel)
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, files[rel], 0o644); err != nil {
				return err
			}
			if rel == rulesFile(app.Name) {
				fmt.Fprintf(stdout, "wrote %s (every hole stubbed: write the rules)\n", path)
			} else {
				fmt.Fprintf(stdout, "wrote %s (no invariant yet: write them)\n", path)
			}
		}
	}
	g, err := Generate(app, *dir)
	if err != nil {
		return err
	}
	if *check {
		if d := g.Diff(*dir); len(d) > 0 {
			return fmt.Errorf("%s model: %s differ from what the catalog generates: regenerate with `%s model` and commit them", app.Name, strings.Join(d, ", "), app.Name)
		}
	} else if err := g.Write(*dir); err != nil {
		return err
	}
	if _, err := os.Stat(rules); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s model: there is no %s, the app's rules (every hole of the skeleton): `%s model -init` writes one with every hole stubbed", app.Name, rules, app.Name)
	}
	if _, err := os.Stat(own); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s model: there is no %s, the app's invariants (the modules its bindings name): `%s model -init` writes one with none yet", app.Name, own, app.Name)
	}
	if len(g.Missing) > 0 {
		var b strings.Builder
		for _, h := range g.Holes {
			if slices.Contains(g.Missing, h.Name) {
				fmt.Fprintf(&b, "\n%s  %s", comment("  ", h.Doc), h.Stub)
			}
		}
		return fmt.Errorf("%s model: %s does not define %s, which the skeleton calls; in module %sRules, for instance:%s",
			app.Name, rules, strings.Join(g.Missing, ", "), mustQuint(app.Name), b.String())
	}
	if !*check {
		fmt.Fprintf(stdout, "wrote %s under %s\n", strings.Join(g.paths(), ", "), *dir)
	}
	return nil
}

func mustQuint(name string) string { q, _ := QuintName(name); return q }

// names is a repeatable flag.
type names []string

func (n *names) String() string     { return strings.Join(*n, ",") }
func (n *names) Set(v string) error { *n = append(*n, v); return nil }

// openDB opens -db's database and its dialect.
func openDB(spec string) (*sql.DB, sqlstore.Dialect, error) {
	kind, dsn, ok := strings.Cut(spec, ":")
	if !ok || dsn == "" {
		return nil, sqlstore.Dialect{}, &UsageError{Msg: fmt.Sprintf("-db %q: give sqlite:PATH or postgres:DSN", spec)}
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
		return nil, sqlstore.Dialect{}, &UsageError{Msg: fmt.Sprintf("-db %q: unknown database %q: give sqlite:PATH or postgres:DSN", spec, kind)}
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
	actorHeader := fs.String("actor-header", "", "")
	poll := fs.Duration("poll", 0, "")
	healthAddr := fs.String("health-addr", "", "")
	grace := fs.Duration("grace", 0, "")
	var tenants, depURLs names
	fs.Var(&tenants, "tenant", "")
	fs.Var(&depURLs, "dependency", "")
	if err := fs.Parse(args); err != nil {
		return &UsageError{Msg: fmt.Sprintf("%s serve: %v\n%s", app.Name, err, mainUsage)}
	}
	if *dbSpec == "" || fs.NArg() > 0 {
		return &UsageError{Msg: fmt.Sprintf("%s serve: give -db (and only flags)\n%s", app.Name, mainUsage)}
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	orchestrator := onOrchestrator()
	if orchestrator && loopback(*addr) {
		// a Service's endpoints are the pod's IP: a server on loopback has
		// none, and every request to it is refused
		return &UsageError{Msg: fmt.Sprintf("%s serve: -addr %s listens on loopback, which no Service reaches (KUBERNETES_SERVICE_HOST is set: this is a pod): give -addr :8080", app.Name, *addr)}
	}
	if *grace < 0 {
		return &UsageError{Msg: fmt.Sprintf("%s serve: -grace %v: give the orchestrator's kill deadline (terminationGracePeriodSeconds), as -grace 30s", app.Name, *grace)}
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
			return &UsageError{Msg: fmt.Sprintf("-dependency %q: give NAME=URL", v)}
		case d == nil:
			return &UsageError{Msg: fmt.Sprintf("-dependency %q: %s has no dependency %q (it has %v)", v, app.Name, name, depNames(app))}
		}
		// checked here: a URL no request can be made to would leave every
		// call's outcome unknown (an at-most-once effect's grist.unknown)
		if pu, err := url.Parse(u); err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
			return &UsageError{Msg: fmt.Sprintf("-dependency %q: give NAME=URL, the URL http:// or https:// with a host (as http://bank.internal:8080)", v)}
		}
		overHTTP[d] = OverHTTP(u, nil)
	}
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
			return &UsageError{Msg: fmt.Sprintf("-tenant: %v", err)}
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
	if *actorHeader != "" {
		opts = append(opts, ActorFromHeader(*actorHeader))
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
	sched := newSchedule(drain, time.Now())
	if hs != nil {
		hs.draining.Store(true)
	}
	if d := time.Until(sched.delay(time.Now())); d > 0 {
		select {
		case <-time.After(d): //qenv:ok the real tier's drain, outside any simulation
		case err := <-done:
			return err
		}
	}
	// the shutdown's context keeps ctx's recorder (the flush below records
	// into it) but not its cancellation
	shut, cancel := context.WithDeadline(context.WithoutCancel(ctx), sched.shutdown(time.Now()))
	if err = srv.Shutdown(shut); err != nil {
		err = fmt.Errorf("%s: requests still in flight when the drain's shutdown ended (%w): their clients retry under their keys", app.Name, err)
	}
	cancel()
	// no request is left: once the processors and the relay are done with
	// what the requests committed, the run has settled, and a settle in
	// the file is what judges the bindings' eventual vals (qobs.Settled)
	until := sched.settle(time.Now())
	quiet, cancelQuiet := context.WithDeadline(context.WithoutCancel(ctx), until)
	if serr := settle(quiet, st, relay, n, time.Until(until)); serr != nil {
		fmt.Fprintf(stderr, "%s: no settle recorded (the steps file's eventual vals get no verdict): %v\n", app.Name, serr)
	}
	cancelQuiet()
	stopNode()
	// every committed step into the file before it closes: a row the
	// relay had not reached would otherwise be recorded only by the next
	// node's relay, into its own file. Its own time: a shutdown that used
	// its whole timeout draining slow requests leaves the flush its own
	flush, cancelFlush := context.WithDeadline(context.WithoutCancel(ctx), sched.flush(time.Now()))
	defer cancelFlush()
	if ferr := relay.Flush(flush); err == nil {
		err = ferr
	}
	return err
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
		ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
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
// take. With DrainDelay on an orchestrator they need 35 s, more than
// Kubernetes' default grace of 30 s: give the pod 45 s
// (terminationGracePeriodSeconds), or -grace to budget them into less.
const (
	ShutdownTimeout = 10 * time.Second
	SettleTimeout   = 10 * time.Second
	FlushTimeout    = 10 * time.Second
)

// settle waits for n to quiet and records a settle into ctx's recorder:
// every poller waiting with nothing after its checkpoint (an effect in
// flight is work in hand), the relay caught up (every committed step
// recorded, Behind 0) and no tenant's head moved across two looks 20 ms
// apart. It returns why not if ctx ends first or the relay failed. The
// settle is the last thing the file holds of the run: the node stops
// after it, with nothing left to do.
func settle(ctx context.Context, st Store, relay *Relay, n *node, bound time.Duration) error {
	var last map[string]Position
	for {
		if err := relay.Err(); err != nil {
			return err
		}
		heads, err := st.Heads(ctx)
		quiet := err == nil && n != nil && n.idle() && maps.Equal(heads, last)
		if quiet {
			behind, berr := relay.Behind(ctx)
			quiet = berr == nil && behind == 0
		}
		if quiet {
			qobs.Settled(ctx)
			return nil
		}
		last = heads
		select {
		case <-ctx.Done():
			return fmt.Errorf("the node did not quiet within %v (a processor still at work, or the relay behind)", bound.Round(time.Millisecond))
		case <-time.After(20 * time.Millisecond): //qenv:ok the real tier's binary, outside any simulation
		}
	}
}

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
