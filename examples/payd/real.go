package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/payd/payd"
	"github.com/chucklehead-dev/gotogether/examples/payd/payd/client"
	"github.com/chucklehead-dev/gotogether/qobs"

	_ "modernc.org/sqlite" // database/sql's "sqlite": pure Go, no cgo
)

// OpenSQLite opens (creating) the SQLite database at path as payd's real
// deployment does: WAL, a busy timeout, and transactions that take the
// write lock when they begin (SQLite has no row locks, and a read that
// later writes would otherwise fail with SQLITE_BUSY, which no
// SQLSTATE names). The schema is payd's own SQL, unchanged.
func OpenSQLite(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
}

// RealProcessor is the processor's double for real runs: captures in
// memory, deduped by Idempotency-Key, each recorded where it is made.
type RealProcessor struct {
	mu   sync.Mutex
	byID map[string]string
	n    int
}

func (p *RealProcessor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount    int64  `json:"amount"`
		Reference string `json:"reference"`
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/captures" || json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "POST /v1/captures {amount, source, reference}", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	p.mu.Lock()
	id, ok := p.byID[key]
	if !ok || key == "" {
		p.n++
		id = fmt.Sprintf("cap_%d", p.n)
		if key != "" {
			if p.byID == nil {
				p.byID = map[string]string{}
			}
			p.byID[key] = id
		}
		qobs.Record(r.Context(), "capture", nil, "key", req.Reference, "amount", req.Amount)
	}
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"id": %q}`, id)
}

// RealResult is what a real run left: its charges, as the table holds
// them.
type RealResult struct {
	Charges []RealCharge
}

// RealCharge is a row of payd's charges table.
type RealCharge struct {
	Key, ID, Account string
	Amount           int64
}

// RunReal runs payd for real: SQLite in dir, payd and the processor
// behind httptest servers, each client's charges on a goroutine of its
// own (all at once), recorded by the same Recording as in a simulation.
// The steps go to the recorder in ctx (and to the global one, for the
// servers' goroutines); after the clients are done the charges are read
// from the table and recorded, and the run is settled.
func RunReal(ctx context.Context, dir string, workload [][]Op) (*RealResult, error) {
	db, err := OpenSQLite(filepath.Join(dir, "payd.db"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := payd.Migrate(ctx, db); err != nil {
		return nil, err
	}
	for _, a := range sortedKeys(Accounts) {
		if _, err := db.ExecContext(ctx, `INSERT INTO accounts (id, balance, n_charges) VALUES (?, ?, 0)`, a, Accounts[a]); err != nil {
			return nil, err
		}
	}
	proc := httptest.NewServer(&RealProcessor{})
	defer proc.Close()
	srv := httptest.NewServer(payd.New(db, &http.Client{Timeout: 5 * time.Second}, proc.URL))
	defer srv.Close()

	var wg sync.WaitGroup
	for i, ops := range workload {
		name := fmt.Sprintf("client%d", i+1)
		tr := http.DefaultTransport.(*http.Transport).Clone()
		sdk := client.New(srv.URL, &http.Client{Transport: &Recording{Base: tr, Client: name}, Timeout: 10 * time.Second})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, op := range ops {
				sdk.CreateCharge(ctx, op.Key, op.Account, op.Amount) // its outcome is in the steps
			}
		}()
	}
	wg.Wait()

	rows, err := db.QueryContext(ctx, `SELECT idem_key, id, account, amount FROM charges ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res := &RealResult{}
	for rows.Next() {
		var c RealCharge
		if err := rows.Scan(&c.Key, &c.ID, &c.Account, &c.Amount); err != nil {
			return nil, err
		}
		res.Charges = append(res.Charges, c)
		qobs.Record(ctx, "charge", nil, "key", c.Key, "id", c.ID, "account", c.Account, "amount", c.Amount)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	qobs.Settled(ctx) // every client is done: nothing is pending
	return res, nil
}

// Concurrent is a real run's load: n keys, each sent by every one of
// senders clients at once (a page submitted from several tabs), keys
// spread over the accounts.
func Concurrent(n, senders int) [][]Op {
	accounts := sortedKeys(Accounts)
	w := make([][]Op, senders)
	for k := range n {
		op := Op{Key: fmt.Sprintf("k%d", k+1), Account: accounts[k%len(accounts)], Amount: int64(10 + k)}
		for s := range senders {
			w[s] = append(w[s], op)
		}
	}
	return w
}
