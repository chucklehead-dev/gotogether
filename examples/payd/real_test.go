package harness

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/payd/payd"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// realRun runs payd for real on SQLite under load and checks the steps.
func realRun(t *testing.T, keys, senders int) (*qtest.Run, *RealResult) {
	t.Helper()
	var res *RealResult
	run := qtest.Record(t, "payd.binding.yaml", func(rec *qobs.Recorder) {
		var err error
		if res, err = RunReal(qobs.WithRecorder(t.Context(), rec), t.TempDir(), Concurrent(keys, senders)); err != nil {
			t.Fatal(err)
		}
	}, qtest.Global()) // the servers' handlers run on their own goroutines
	return run, res
}

// payd as it is, on SQLite behind real HTTP, every key sent by three
// clients at once: each charged and captured once, every answer its charge.
func TestReal(t *testing.T) {
	qtest.RequireQuint(t)
	if mutant != "" {
		t.Skip("payd as it is; a mutant's real run is in TestMutant")
	}
	run, res := realRun(t, 30, 3)
	run.Expect(t, qtest.Conforms())
	if len(res.Charges) != 30 {
		t.Fatalf("%d charges for 30 keys", len(res.Charges))
	}
}

// TestRealPrecheck is the precheck mutant's real run (from TestMutant):
// three clients sending each key at once race the lookup, and some key is
// charged twice.
func TestRealPrecheck(t *testing.T) {
	if mutant != "precheck" {
		t.Skip("the precheck mutant's: TestMutants runs it")
	}
	run, res := realRun(t, 10, 3)
	run.Expect(t, qtest.Violates("noDoubleCharge"))
	t.Logf("%d charges for 10 keys", len(res.Charges))
}

// A key answers as it did the first time, marked replayed; the same key
// with another request is refused (409); a key needs to be there.
func TestKeyReuse(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "payd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := payd.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO accounts (id, balance, n_charges) VALUES ('acct_1', 1000, 0)`); err != nil {
		t.Fatal(err)
	}
	proc := httptest.NewServer(&RealProcessor{})
	defer proc.Close()
	srv := httptest.NewServer(payd.New(db, proc.Client(), proc.URL))
	defer srv.Close()
	post := func(key, body string) (int, string, string) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/charges", strings.NewReader(body))
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Idempotent-Replayed"), string(b)
	}
	code1, _, first := post("k1", `{"account": "acct_1", "amount": 250}`)
	code2, replayed, again := post("k1", `{"amount": 250, "account": "acct_1"}`)
	code3, _, _ := post("k1", `{"account": "acct_1", "amount": 300}`)
	code4, _, _ := post("", `{"account": "acct_1", "amount": 250}`)
	code5, _, _ := post("k2", `{"account": "nobody", "amount": 250}`)
	if code1 != 201 || code2 != 201 || replayed != "true" || again != first || code3 != 409 || code4 != 400 || code5 != 404 {
		t.Fatalf("got %d %q, %d replayed=%q %q, %d, %d, %d; want 201, the same 201 replayed, 409, 400, 404", code1, first, code2, replayed, again, code3, code4, code5)
	}
	var balance int64
	if err := db.QueryRow(`SELECT balance FROM accounts WHERE id = 'acct_1'`).Scan(&balance); err != nil || balance != 750 {
		t.Fatalf("balance %d (%v), want 750: one charge", balance, err)
	}
}
