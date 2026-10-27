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
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Main is an application binary's entry point: func main() {
// grist.Main(app, opts...) }, the binary importing its database's driver
// (modernc.org/sqlite, github.com/jackc/pgx/v5/stdlib: grist imports
// none). It runs MainArgs on the process's arguments, stopping on SIGINT
// or SIGTERM, and exits 2 on a usage error, 1 on any other.
func Main(app *App, opts ...ServeOption) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
      -init                            also write a first model/<app>.qnt (Starter) if there is none
  serve -db DB [flags]        serve the App's HTTP API (NewHandler) and run its node (App.Start) on DB
      -db sqlite:PATH | postgres:DSN   the store (grist/sqlstore; the binary imports the driver)
      -addr HOST:PORT                  where to listen (default 127.0.0.1:8080; :0 picks a port)
      -tenant NAME                     create the tenant if it does not exist (repeatable)
      -steps PATH                      record every step to PATH (qobs.FileSink, PerStep), for validation
      -actor-header NAME               take the request's actor from header NAME (behind an authenticating proxy only)
      -poll DURATION                   how often the store reads other processes' appends (default 250ms)`

// MainArgs runs one of Main's commands, args the binary's arguments
// after its name:
//
//	catalog    App.Catalog as JSON on stdout
//	verify     App.Verify
//	model      Generate's files written under -dir (default .), or with -check
//	           compared with those there (an error naming each that differs);
//	           -init writes a Starter model/<app>.qnt where there is none.
//	           It lists the holes the app's model has not filled
//	serve      the HTTP API (NewHandler, opts applied) on grist/sqlstore over -db
//	           (sqlite:PATH, postgres:DSN), the node (App.Start: relay, pollers,
//	           the tenant watcher) beside it, every step recorded to -steps
//	           (qobs.FileSink, PerStep: a new file each time) for validation with
//	           the app's bindings; until ctx ends, then a graceful shutdown: the
//	           server drains its requests (ShutdownTimeout) while the node keeps
//	           running, then, once its processors are idle and its relay caught
//	           up (SettleTimeout), a settle is recorded (qobs.Settled: the
//	           bindings' eventual vals are judged on the file), then the node
//	           stops and the relay records every committed step (Relay.Flush,
//	           FlushTimeout) before the file closes
//
// serve writes "<app> serving on http://ADDR" to stderr once it listens.
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
	if *starter && !*check {
		if _, err := os.Stat(own); errors.Is(err, os.ErrNotExist) {
			b, err := Starter(app)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(own, b, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "wrote %s (every hole stubbed: write the rules)\n", own)
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
	if len(g.Missing) > 0 {
		var b strings.Builder
		for _, h := range g.Holes {
			if slices.Contains(g.Missing, h.Name) {
				fmt.Fprintf(&b, "\n%s  %s", comment("  ", h.Doc), h.Stub)
			}
		}
		return fmt.Errorf("%s model: %s does not define %s, which the skeleton calls; in module %sRules, for instance:%s",
			app.Name, own, strings.Join(g.Missing, ", "), mustQuint(app.Name), b.String())
	}
	if !*check {
		fmt.Fprintf(stdout, "wrote %d files under %s\n", len(g.Files), *dir)
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
	var tenants names
	fs.Var(&tenants, "tenant", "")
	if err := fs.Parse(args); err != nil {
		return &UsageError{Msg: fmt.Sprintf("%s serve: %v\n%s", app.Name, err, mainUsage)}
	}
	if *dbSpec == "" || fs.NArg() > 0 {
		return &UsageError{Msg: fmt.Sprintf("%s serve: give -db (and only flags)\n%s", app.Name, mainUsage)}
	}
	if err := app.Verify(); err != nil {
		return err
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
	if *steps != "" {
		fsink, err := qobs.FileSink(*steps, qobs.PerStep)
		if err != nil {
			return err
		}
		defer fsink.Close()
		sink = fsink
		ctx = qobs.WithRecorder(ctx, qobs.NewRecorder(sink, qobs.WithID(fmt.Sprintf("%s-%d", app.Name, os.Getpid()))))
	}
	var cfg serveConfig
	for _, o := range opts {
		if o.apply != nil {
			o.apply(&cfg)
		}
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
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
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
	// the shutdown's context keeps ctx's recorder (the flush below records
	// into it) but not its cancellation
	shut, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	err = srv.Shutdown(shut)
	cancel()
	// no request is left: once the processors and the relay are done with
	// what the requests committed, the run has settled, and a settle in
	// the file is what judges the bindings' eventual vals (qobs.Settled)
	quiet, cancelQuiet := context.WithTimeout(context.WithoutCancel(ctx), SettleTimeout)
	if serr := settle(quiet, st, relay, n); serr != nil {
		fmt.Fprintf(stderr, "%s: no settle recorded (the steps file's eventual vals get no verdict): %v\n", app.Name, serr)
	}
	cancelQuiet()
	stopNode()
	// every committed step into the file before it closes: a row the
	// relay had not reached would otherwise be recorded only by the next
	// node's relay, into its own file. Its own time: a shutdown that used
	// its whole timeout draining slow requests leaves the flush its own
	flush, cancelFlush := context.WithTimeout(context.WithoutCancel(ctx), FlushTimeout)
	defer cancelFlush()
	if ferr := relay.Flush(flush); err == nil {
		err = ferr
	}
	return err
}

// Main's server's bounds: how long a graceful shutdown waits for the
// requests in flight, then how long for the node to quiet before it
// records a settle, and then how long the relay's flush may take.
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
func settle(ctx context.Context, st Store, relay *Relay, n *node) error {
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
			return fmt.Errorf("the node did not quiet within %v (a processor still at work, or the relay behind)", SettleTimeout)
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
