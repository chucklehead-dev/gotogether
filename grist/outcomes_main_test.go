package grist

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	_ "modernc.org/sqlite"
)

func TestOutcomesCommandReadOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	a := New("outcomes-cli")
	path := filepath.Join(t.TempDir(), "existing database.db")
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(path, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := sqlstore.Open(ctx, db, sqlstore.SQLite, sqlstore.Options{Name: a.Name})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTenant(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, "t", store.Batch{Events: []Event{{Type: "retired-trigger", Version: 1, Body: []byte(`{"value":1}`)}}}); err != nil {
		t.Fatal(err)
	}
	trigger, found, err := st.ReadEvent(ctx, "t", 1)
	if err != nil || !found {
		t.Fatalf("trigger: %v %v", found, err)
	}
	if _, err := st.Append(ctx, "t", store.Batch{Events: []Event{unknownEvent("retired-processor", trigger, "connection lost\nmay have applied"), failedEvent("retired-processor", trigger)}}); err != nil {
		t.Fatal(err)
	}
	var before struct{ head, steps int64 }
	if err := db.QueryRowContext(ctx, "SELECT head, steps FROM grist_heads WHERE tenant = 't'").Scan(&before.head, &before.steps); err != nil {
		t.Fatal(err)
	}
	// The read-only CLI must work without requiring WAL: inspection doesn't own
	// the writer's journal policy. Close the writer, then change the fixture mode.
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	plain, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	dsnFile := filepath.Join(t.TempDir(), "dsn")
	if err := os.WriteFile(dsnFile, []byte("sqlite:"+path+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"outcomes", "-db-file", dsnFile, "-tenant", "t", "-limit", "1", "-json"}
	if err := MainArgs(ctx, a, args, &out, &out); err != nil {
		t.Fatal(err)
	}
	var report OutcomeInspection
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json: %v: %s", err, out.String())
	}
	if len(report.Outcomes) != 1 || !report.HasMore || report.NextAfter != 2 || report.Outcomes[0].Trigger.ID != trigger.ID || report.Outcomes[0].Trigger.Status != TriggerRetained || report.Outcomes[0].ProcessorKnown || report.Outcomes[0].Error == nil || *report.Outcomes[0].Error != "connection lost\nmay have applied" {
		t.Fatalf("report: %+v", report)
	}
	out.Reset()
	if err := MainArgs(ctx, a, []string{"outcomes", "-db", "sqlite:" + path, "-tenant", "t", "-after", "2", "-processor", "retired-processor"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"independent_reads", "not registered", trigger.ID, "not stored", "retry is safe"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("text missing %q: %s", want, out.String())
		}
	}
	var head, steps int64
	if err := plain.QueryRowContext(ctx, "SELECT head, steps FROM grist_heads WHERE tenant = 't'").Scan(&head, &steps); err != nil {
		t.Fatal(err)
	}
	if head != before.head || steps != before.steps {
		t.Fatalf("inspection changed store: head %d -> %d steps %d -> %d", before.head, head, before.steps, steps)
	}
	var journal string
	if err := plain.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
		t.Fatalf("inspection changed journal mode: %q %v", journal, err)
	}
}

func TestOutcomesCommandRefusesInitialization(t *testing.T) {
	t.Parallel()
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-file", true: "empty-database"}[existing], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "must not initialize.db")
			if existing {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.PingContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			var out bytes.Buffer
			err := MainArgs(t.Context(), New("outcomes-cli"), []string{"outcomes", "-db", "sqlite:" + path, "-tenant", "t", "-json"}, &out, &out)
			if err == nil || out.Len() != 0 {
				t.Fatalf("initialization accepted/partial output: %v %s", err, out.String())
			}
			if !existing {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("inspection created file: %v", err)
				}
			} else {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var tables int
				if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name LIKE 'grist_%'").Scan(&tables); err != nil || tables != 0 {
					t.Fatalf("inspection initialized schema: tables=%d %v", tables, err)
				}
			}
		})
	}
}

func TestOutcomesCommandUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"outcomes"}, {"outcomes", "-tenant", "t", "-limit", "0"}, {"outcomes", "-tenant", "t", "-after", "-1"}, {"outcomes", "-tenant", "t", "-limit", "1001"}, {"outcomes", "-tenant", "t", "extra"}, {"outcomes", "-tenant", "t", "-db", "sqlite:a", "-db-file", "b"}} {
		var out bytes.Buffer
		err := MainArgs(context.Background(), New("outcomes-cli"), args, &out, &out)
		if !errors.As(err, new(*UsageError)) || out.Len() != 0 {
			t.Fatalf("args %v: want usage and no output, got %v %s", args, err, out.String())
		}
	}
}
