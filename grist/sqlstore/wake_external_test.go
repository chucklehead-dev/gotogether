package sqlstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/sqlstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
)

// Independent native stores have no shared local signal. Polling must remain
// sufficient even when an external listener delivers no hints at all.
func TestSQLiteExternalWriterPolling(t *testing.T) {
	db := openSQLite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reader, err := sqlstore.Open(ctx, db, sqlstore.SQLite, sqlstore.Options{Name: "reader", Poll: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := sqlstore.Open(ctx, db, sqlstore.SQLite, sqlstore.Options{Name: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CreateTenant(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 3)
	go func() { results <- reader.Wait(ctx, "first", 0) }()      //qenv:ok native SQLite waits
	go func() { results <- reader.WaitSteps(ctx, "first", 0) }() //qenv:ok native SQLite waits
	go func() { results <- reader.WaitTenants(ctx, 1) }()        //qenv:ok native SQLite waits
	if _, err := writer.Append(ctx, "first", store.Batch{Events: []store.Event{{Type: "a", Version: 1, Body: []byte(`{}`)}}, Step: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := writer.CreateTenant(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}
