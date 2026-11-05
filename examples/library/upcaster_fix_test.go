package library

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// Each read runs in a fresh process, so neither the old nor the fixed App can
// carry an in-memory read-model state into another phase. The only changed
// production source between binaries is the existing dropsCopies upcaster;
// both receive identical public snapshot/audit/memo settings through an overlay.
func TestUpcasterFixRefoldsPersistedSnapshots(t *testing.T) {
	dir := t.TempDir()
	appPath, err := filepath.Abs("app.go")
	if err != nil {
		t.Fatal(err)
	}
	appSource, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	old := `grist.New("library")`
	if strings.Count(string(appSource), old) != 1 {
		t.Fatal("library App declaration changed: update the rehearsal's settings overlay")
	}
	configured := filepath.Join(dir, "app.go")
	settings := `grist.New("library", grist.Snapshots(4), grist.AuditSnapshots(0), grist.Memo(0))`
	if err := os.WriteFile(configured, []byte(strings.Replace(string(appSource), old, settings, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	upcastPath, err := filepath.Abs("upcast.go")
	if err != nil {
		t.Fatal(err)
	}
	mutantPath, err := filepath.Abs("testdata/mutants/dropsCopies/upcast.go")
	if err != nil {
		t.Fatal(err)
	}
	build := func(name string, buggy bool) string {
		t.Helper()
		replacements := map[string]string{appPath: configured}
		if buggy {
			replacements[upcastPath] = mutantPath
		}
		overlay := filepath.Join(dir, name+"-overlay.json")
		data, err := json.Marshal(map[string]any{"Replace": replacements})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(overlay, data, 0600); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(dir, name+".test")
		cmd := exec.CommandContext(t.Context(), "go", "test", "-c", "-overlay", overlay, "-o", binary, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, out)
		}
		return binary
	}
	buggy, fixed := build("buggy", true), build("fixed", false)
	database := filepath.Join(dir, "library.db")
	run := func(binary, phase, mode string) upcasterRead {
		t.Helper()
		report := filepath.Join(dir, phase+".json")
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestLibraryUpcasterProcess$", "--", mode, database, report)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", phase, err, out)
		}
		data, err := os.ReadFile(report)
		if err != nil {
			t.Fatal(err)
		}
		var result upcasterRead
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	seeded := run(buggy, "buggy-seed", "seed")
	cachedBuggy := run(buggy, "buggy-reopen", "read")
	corrected := run(fixed, "fixed-reopen", "read")
	cachedFixed := run(fixed, "fixed-reopen-again", "read")
	if seeded.Shelf.Copies != 0 || seeded.SnapshotState.Copies != 0 || seeded.Stats.SnapshotMisses != 1 || seeded.Stats.Folded != 8 {
		t.Fatalf("buggy upcaster did not persist its wrong snapshot: %+v", seeded)
	}
	for name, r := range map[string]upcasterRead{"unchanged buggy": cachedBuggy, "unchanged fixed": cachedFixed} {
		if r.Stats.SnapshotHits != 1 || r.Stats.SnapshotMisses != 0 || r.Stats.Folded != 0 {
			t.Fatalf("%s did not reuse its persisted snapshot: %+v", name, r)
		}
	}
	if cachedBuggy.Shelf.Copies != 0 || cachedBuggy.Fingerprint != seeded.Fingerprint {
		t.Fatalf("unchanged buggy release changed: %+v", cachedBuggy)
	}
	if corrected.Fingerprint == seeded.Fingerprint || corrected.Stats.SnapshotMisses != 1 || corrected.Stats.SnapshotHits != 0 || corrected.Stats.Folded != 8 || corrected.Shelf.Copies != 16 || corrected.SnapshotState.Copies != 16 {
		t.Fatalf("fixed upcaster reused the wrong snapshot instead of refolding: %+v", corrected)
	}
	if cachedFixed.Shelf.Copies != 16 || cachedFixed.Fingerprint != corrected.Fingerprint {
		t.Fatalf("fixed release did not retain its new snapshot: %+v", cachedFixed)
	}
	for name, r := range map[string]upcasterRead{"seed": seeded, "buggy reopen": cachedBuggy, "fixed reopen": corrected, "fixed cached": cachedFixed} {
		if r.RowsHash != seeded.RowsHash || r.Rows != 8 || r.Head != 8 || r.QueryPosition != 8 || r.EventVersion != 2 || r.ReadModelVersion != 1 || r.StoredVersion != 1 || r.Stats.MemoHits != 0 || r.Stats.Audits != 0 {
			t.Fatalf("%s changed history/versions or used a memo/audit: %+v", name, r)
		}
	}
	t.Logf("persisted v1 rows unchanged (%s); buggy snapshot %s -> fixed %s; fixed copies=16, repeated fresh reads hit their own snapshots", seeded.RowsHash, seeded.Fingerprint, corrected.Fingerprint)
}

// upcasterRead is private test-process evidence, not a public report format.
type upcasterProbeBook struct {
	Book string `json:"book"`
}

type upcasterRead struct {
	Shelf, SnapshotState                                Shelf
	Fingerprint, RowsHash                               string
	Rows, EventVersion, ReadModelVersion, StoredVersion int
	Head, QueryPosition                                 grist.Position
	Stats                                               grist.ReadStats
}

// TestLibraryUpcasterProcess is selected only by the rehearsal's isolated child
// binaries. Its temporary query reads the real Books fold without adding a
// public application block or changing the normal process's generated catalog.
func TestLibraryUpcasterProcess(t *testing.T) {
	at := slices.Index(os.Args, "--")
	if at < 0 {
		return
	}
	args := os.Args[at+1:]
	if len(args) != 3 || (args[0] != "seed" && args[0] != "read") {
		t.Fatal("upcaster helper requires seed|read DATABASE REPORT")
	}
	ctx := t.Context()
	db, err := sql.Open("sqlite", sqlstore.SQLiteDSN(args[1], 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := sqlstore.Open(ctx, db, sqlstore.SQLite, sqlstore.Options{Name: App.Name})
	if err != nil {
		t.Fatal(err)
	}
	const tenant = "upcaster-fix"
	if args[0] == "seed" {
		if err := st.CreateTenant(ctx, tenant); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(BookAddedV1{Book: "x", Copies: 2})
		if err != nil {
			t.Fatal(err)
		}
		events := make([]grist.Event, 8)
		for i := range events {
			events[i] = grist.Event{Type: "book-added", Version: 1, Tags: []grist.TagPair{{Key: "book", Value: "x"}}, Body: body}
		}
		if _, err := st.Append(ctx, tenant, store.Batch{Events: events, Time: 1, Cause: "version-1-history"}); err != nil {
			t.Fatal(err)
		}
	}
	query := grist.NewQuery(App, "upcaster-rehearsal-shelf", func(r *grist.Reads, q upcasterProbeBook) (Shelf, error) { return Books.For(r, q.Book), nil }, grist.Authorize(grist.Authenticated))
	result := upcasterRead{}
	for _, ev := range App.Catalog().Events {
		if ev.Name == "book-added" {
			result.EventVersion = ev.Version
		}
	}
	for _, rm := range App.Catalog().ReadModels {
		if rm.Name == "books" {
			result.Fingerprint, result.ReadModelVersion = rm.Fingerprint, rm.Version
		}
	}
	result.Shelf, result.QueryPosition, err = query.Run(ctx, st, grist.Envelope{ID: "inspect", Tenant: tenant, Caller: "probe"}, upcasterProbeBook{Book: "x"})
	if err != nil {
		t.Fatal(err)
	}
	result.Stats = App.ReadStats()
	snapshot, found, err := st.GetSnapshot(ctx, tenant, "books", "book=x", result.Fingerprint)
	if err != nil || !found {
		t.Fatalf("no persisted books snapshot: found=%v err=%v", found, err)
	}
	if err := json.Unmarshal(snapshot.State, &result.SnapshotState); err != nil {
		t.Fatal(err)
	}
	rows, head, err := st.Read(ctx, tenant, store.Selector{Types: []string{"book-added"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows, result.Head = len(rows), head
	h := sha256.New()
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	h.Write(data)
	expectedBody, err := json.Marshal(BookAddedV1{Book: "x", Copies: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		// Hash raw bodies too: RawMessage marshaling can normalize whitespace.
		fmt.Fprintf(h, "\x00%d\x00", len(row.Body))
		h.Write(row.Body)
		if row.Type != "book-added" || row.Version != 1 || !bytes.Equal(row.Body, expectedBody) {
			t.Fatalf("stored event was rewritten: %+v", row)
		}
	}
	result.StoredVersion = 1
	result.RowsHash = hex.EncodeToString(h.Sum(nil))
	data, err = json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(args[2], data, 0600); err != nil {
		t.Fatal(err)
	}
}
