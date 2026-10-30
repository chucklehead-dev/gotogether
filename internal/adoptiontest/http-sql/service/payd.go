// Package payd is a small payments service: it charges an account an
// amount, once per idempotency key, capturing the money at a payment
// processor. It is written as any net/http and database/sql service is,
// against the standard library alone, and knows nothing of how it is
// tested.
//
//	POST /v1/charges        Idempotency-Key: <key>   {"account": "acct_1", "amount": 250}
//	GET  /v1/charges/{id}
//
// A charge is made in three steps, none of which holds a database
// transaction across the network:
//
//  1. claim: one transaction checks the account and inserts the key's row
//     in idempotency as pending (INSERT ... ON CONFLICT (key) DO NOTHING).
//     A key already there answers with its stored response, or 409 if it
//     came with another request; a key still pending is driven on.
//  2. capture: the processor is asked to capture the amount with the
//     same Idempotency-Key, so asking it again (a retry, a crash between
//     the steps, two copies of one request) captures once.
//  3. complete: one transaction marks the key done, if it is still
//     pending (a compare-and-set: of two that captured, one completes),
//     debits the account, inserts the charge and stores the response.
//
// A crash anywhere leaves either nothing or a pending key, which the
// client's retry drives to completion. Serialization failures and
// deadlocks (SQLSTATE 40001, 40P01) are retried, found as PostgreSQL and
// CockroachDB drivers report them (errors.As to SQLState).
package payd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// server is the service's state: its database and its processor.
type server struct {
	db      *sql.DB
	proc    *http.Client
	procURL string
}

// New returns the service's handler, over db (migrated with Migrate),
// capturing at the processor at procURL through proc.
func New(db *sql.DB, proc *http.Client, procURL string) http.Handler {
	s := &server{db: db, proc: proc, procURL: procURL}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/charges", s.createCharge)
	mux.HandleFunc("GET /v1/charges/{id}", s.getCharge)
	return mux
}

// Charge is a charge as the API returns it.
type Charge struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	Amount  int64  `json:"amount"`
	Capture string `json:"capture"`
	Status  string `json:"status"`
}

// chargeRequest is a POST /v1/charges body.
type chargeRequest struct {
	Account string `json:"account"`
	Amount  int64  `json:"amount"`
}

func (s *server) getCharge(w http.ResponseWriter, r *http.Request) {
	var c Charge
	err := s.db.QueryRowContext(r.Context(), `SELECT id, account, amount, capture_id FROM charges WHERE id = ?`, r.PathValue("id")).
		Scan(&c.ID, &c.Account, &c.Amount, &c.Capture)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "no such charge")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c.Status = "succeeded"
	writeJSON(w, http.StatusOK, c)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, _ := json.Marshal(v)
	writeRaw(w, code, b)
}

func writeRaw(w http.ResponseWriter, code int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// maxAttempts bounds a transaction's retries on serialization failures.
const maxAttempts = 10

// inTx runs fn in a transaction and commits it, again from the start on a
// serialization failure or a deadlock, as PostgreSQL asks of its clients.
// fn must reset whatever it sets: it may run more than once.
func (s *server) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	for attempt := 1; ; attempt++ {
		err := func() error {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if err := fn(tx); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err == nil || !retryable(err) || attempt == maxAttempts {
			return err
		}
	}
}

// retryable reports whether err is a serialization failure or a deadlock:
// the transaction did nothing, and running it again may succeed.
func retryable(err error) bool {
	var st interface{ SQLState() string }
	if !errors.As(err, &st) {
		return false
	}
	switch st.SQLState() {
	case "40001", "40P01":
		return true
	}
	return false
}

// errNoAccount is a charge to an account the service does not hold.
var errNoAccount = errors.New("no such account")

func internal(w http.ResponseWriter, what string, err error) {
	writeError(w, http.StatusInternalServerError, fmt.Sprintf("%s: %v", what, err))
}
