package rehearsal_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	payd "example.com/onboarding-http-sql/service"
	"fmt"
	"github.com/chucklehead-dev/gotogether/qobs"
	"io"
	_ "modernc.org/sqlite"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestShrinkRecord(t *testing.T) {
	if os.Getenv("ONBOARDING_ACTIONS") == "" {
		t.Skip("external shrink driver only")
	}
	var actions []string
	if err := json.Unmarshal([]byte(os.Getenv("ONBOARDING_ACTIONS")), &actions); err != nil {
		t.Fatal(err)
	}
	has := func(s string) bool {
		for _, a := range actions {
			if a == s {
				return true
			}
		}
		return false
	}
	if !has("charge") {
		if err := os.WriteFile(os.Getenv("ONBOARDING_STEPS"), []byte("{\"quint.format\":\"gotogether.steps/1\"}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = payd.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts VALUES ('a',100,0)`); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	captures := map[string]string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Amount            int
			Source, Reference string
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Amount != 10 || req.Source != "a" || req.Reference != "key" {
			t.Errorf("unexpected capture %+v", req)
		}
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		mu.Lock()
		key := r.Header.Get("Idempotency-Key")
		id := captures[key]
		if id == "" {
			id = fmt.Sprintf("capture%d", len(captures)+1)
			captures[key] = id
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%q}`, id)
	}))
	defer peer.Close()
	app := httptest.NewServer(payd.New(db, peer.Client(), peer.URL))
	defer app.Close()
	os.MkdirAll("logs", 0700)
	sink, err := qobs.FileSink(os.Getenv("ONBOARDING_STEPS"), qobs.PerStep)
	if err != nil {
		t.Fatal(err)
	}
	rec := qobs.NewRecorder(sink, qobs.WithActor("payment"))
	defer rec.Close()
	snapshot := func(action string, code int, replayed bool) {
		var balance, n int
		var phase string
		if err := db.QueryRow(`SELECT balance,n_charges FROM accounts WHERE id='a'`).Scan(&balance, &n); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT status FROM idempotency WHERE key='key'`).Scan(&phase); err != nil {
			t.Fatal(err)
		}
		if phase == "done" {
			phase = "done"
		}
		mu.Lock()
		count := len(captures)
		mu.Unlock()
		rec.Record(ctx, action, nil, "obs.phase", phase, "obs.balance", balance, "obs.debits", n, "obs.captures", count, "obs.code", code, "obs.replayed", replayed)
	}
	post := func(amount int) (int, bool) {
		req, _ := http.NewRequest("POST", app.URL+"/v1/charges", bytes.NewBufferString(fmt.Sprintf(`{"account":"a","amount":%d}`, amount)))
		req.Header.Set("Idempotency-Key", "key")
		resp, err := app.Client().Do(req)
		if err != nil {
			t.Error(err)
			return 0, false
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, resp.Header.Get("Idempotent-Replayed") == "true"
	}
	done := make(chan struct {
		code   int
		replay bool
	}, 1)
	go func() {
		code, replay := post(10)
		done <- struct {
			code   int
			replay bool
		}{code, replay}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("processor not entered")
	}
	snapshot("begin", 0, false)
	if has("conflict") {
		code, replayed := post(11)
		snapshot("conflict", code, replayed)
	}
	close(release)
	select {
	case result := <-done:
		snapshot("finish", result.code, result.replay)
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
	if has("replay") {
		code, replayed := post(10)
		snapshot("replay", code, replayed)
	}
	rec.Settled(ctx)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
}
