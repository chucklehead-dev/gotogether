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
	"os"
	"os/signal"
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
//	serve      the HTTP API (NewHandler, opts applied) on grist/sqlstore over -db
//	           (sqlite:PATH, postgres:DSN), the node (App.Start: relay, pollers,
//	           the tenant watcher) beside it, every step recorded to -steps
//	           (qobs.FileSink, PerStep: a new file each time) for validation with
//	           the app's bindings; until ctx ends, then a graceful shutdown that
//	           records every committed step (Relay.Flush) before closing the file
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
	case "serve":
		return serveMain(ctx, app, args[1:], stderr, opts)
	case "help", "-h", "-help", "--help":
		fmt.Fprintf(stdout, "usage: %s <command>\n%s\n", app.Name, mainUsage)
		return nil
	}
	return &UsageError{Msg: fmt.Sprintf("%s: unknown command %q\n%s", app.Name, args[0], mainUsage)}
}

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
	nodeCtx, stopNode := context.WithCancel(ctx)
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
	if err := app.Start(nodeCtx, st, WithRelay(relay)); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	// requests run with the node's context (its recorder), so a query's
	// step and a refusal's on-return step reach the same file
	srv := &http.Server{Handler: h, BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
		ReadHeaderTimeout: 10 * time.Second}
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
	shut, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err = srv.Shutdown(shut)
	stopNode()
	// every committed step into the file before it closes: a row the
	// relay had not reached would otherwise be recorded only by the next
	// node's relay, into its own file
	if ferr := relay.Flush(shut); err == nil {
		err = ferr
	}
	return err
}
