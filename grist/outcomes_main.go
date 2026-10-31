package grist

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
)

func outcomesMain(ctx context.Context, app *App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet(app.Name+" outcomes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	spec, file := fs.String("db", "", ""), fs.String("db-file", "", "")
	tenant, processor := fs.String("tenant", "", ""), fs.String("processor", "", "")
	after, limit := fs.Int64("after", 0, ""), fs.Int("limit", 100, "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *tenant == "" || *after < 0 || *limit < 1 || *limit > 1000 {
		return &usageError{Msg: fmt.Sprintf("usage: %s outcomes (-db DB | -db-file PATH) -tenant T [-processor P] [-after N] [-limit 1..1000] [-json]\n%s", app.Name, mainUsage)}
	}
	if err := CheckTenant(*tenant); err != nil {
		return err
	}
	if err := dbFromFile(app, "outcomes", spec, *file); err != nil {
		return err
	}
	if *spec == "" {
		return &usageError{Msg: fmt.Sprintf("%s outcomes: give -db or -db-file\n%s", app.Name, mainUsage)}
	}
	db, dialect, err := openOutcomeDB(*spec)
	if err != nil {
		return err
	}
	defer db.Close()
	st, err := sqlstore.OpenReadOnly(ctx, db, dialect, sqlstore.Options{Name: app.Name})
	if err != nil {
		return fmt.Errorf("%s outcomes: open existing current schema read-only: %w", app.Name, err)
	}
	report, err := app.InspectOutcomes(ctx, st, *tenant, OutcomeOptions{After: Position(*after), Limit: *limit, Processor: *processor})
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(stdout).Encode(report)
	}
	return writeOutcomes(stdout, report)
}

// openOutcomeDB never initializes SQLite or sets its persistent journal mode.
// The CLI accepts a filename, escaped as a URI so user text cannot override ro.
func openOutcomeDB(spec string) (*sql.DB, sqlstore.Dialect, error) {
	kind, path, ok := strings.Cut(spec, ":")
	if !ok || kind != "sqlite" {
		return openDB(spec)
	}
	if path == "" {
		return nil, sqlstore.Dialect{}, &usageError{Msg: "outcomes: give sqlite:PATH naming an existing database"}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, sqlstore.Dialect{}, fmt.Errorf("outcomes: SQLite path: %w", err)
	}
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := url.Values{"mode": []string{"ro"}, "_pragma": []string{"busy_timeout(5000)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, sqlstore.Dialect{}, fmt.Errorf("outcomes: open SQLite reader (import _ %q in the binary): %w", "modernc.org/sqlite", err)
	}
	return db, sqlstore.SQLite, nil
}

func writeOutcomes(w io.Writer, r *OutcomeInspection) error {
	var out bytes.Buffer
	fmt.Fprintf(&out, "%s tenant %s: stored failed/unknown outcomes after %d, read through %d\n", strconv.Quote(r.App), strconv.Quote(r.Tenant), r.After, r.ReadThrough)
	fmt.Fprintln(&out, "coverage: stored_failed_unknown; observations: independent_reads (no atomic snapshot)")
	for _, limitation := range r.Limitations {
		fmt.Fprintln(&out, limitation)
	}
	if len(r.Outcomes) == 0 {
		fmt.Fprintln(&out, "No stored failed/unknown outcomes on this page.")
	} else {
		table := tabwriter.NewWriter(&out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "OUTCOME (POS@ID)\tTYPE\tPROCESSOR\tTRIGGER (POS@ORIGINAL ID)\tOBSERVED STATUS\tOBSERVED ID/TYPE\tDERIVED EFFECT KEY\tSTORED ERROR")
		for _, o := range r.Outcomes {
			known := ""
			if !o.ProcessorKnown {
				known = " (not registered)"
			}
			observed := "-"
			if o.Trigger.ObservedID != "" {
				observed = o.Trigger.ObservedID + "/" + strconv.Quote(o.Trigger.ObservedType)
			}
			storedError := "not stored"
			if o.Error != nil {
				storedError = strconv.Quote(*o.Error)
			}
			fmt.Fprintf(table, "%d@%s\t%s\t%s%s\t%d@%s\t%s\t%s\t%s\t%s\n", o.Position, o.ID, o.Type, strconv.Quote(o.Processor), known, o.Trigger.Position, o.Trigger.ID, o.Trigger.Status, observed, strconv.Quote(o.EffectKey), storedError)
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}
	if r.HasMore {
		fmt.Fprintf(&out, "More stored outcomes: continue with -after %d (history may change across pages).\n", r.NextAfter)
	}
	_, err := w.Write(out.Bytes())
	return err
}
