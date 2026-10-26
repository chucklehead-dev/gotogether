// Mutant precheck (a go build overlay of payd/charge.go, with schema.go
// whose charges.idem_key has no UNIQUE): the idempotency key is checked by
// a lookup of its charge, in its own autocommit, outside the transaction
// that makes the charge, and nothing in the database stops a second one.
// Two attempts with one key that overlap (a duplicate, a retry of a late
// request) both find nothing and both charge.

package payd

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// createCharge is POST /v1/charges: find the key's charge, else capture
// and charge.
func (s *server) createCharge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 255 {
		writeError(w, http.StatusBadRequest, "an Idempotency-Key header of 1 to 255 characters is required")
		return
	}
	var req chargeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.Account == "" || req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, `the body is {"account": "<id>", "amount": <a positive integer>}`)
		return
	}

	// a charge made for the key is the answer
	var prev Charge
	err := s.db.QueryRowContext(ctx, `SELECT id, account, amount, capture_id FROM charges WHERE idem_key = ?`, key).
		Scan(&prev.ID, &prev.Account, &prev.Amount, &prev.Capture)
	switch {
	case err == nil:
		if prev.Account != req.Account || prev.Amount != req.Amount {
			writeError(w, http.StatusConflict, "the Idempotency-Key was used with another request")
			return
		}
		prev.Status = "succeeded"
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSON(w, http.StatusOK, prev)
		return
	case !errors.Is(err, sql.ErrNoRows):
		internal(w, "looking the key up", err)
		return
	}

	capture, err := s.capture(ctx, key, req.Account, req.Amount)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	var c Charge
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance - ?, n_charges = n_charges + 1 WHERE id = ?`, req.Amount, req.Account)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			if err == nil {
				err = errNoAccount
			}
			return err
		}
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT n_charges FROM accounts WHERE id = ?`, req.Account).Scan(&n); err != nil {
			return err
		}
		c = Charge{ID: fmt.Sprintf("ch_%s_%d", req.Account, n), Account: req.Account, Amount: req.Amount, Capture: capture, Status: "succeeded"}
		_, err = tx.ExecContext(ctx, `INSERT INTO charges (id, idem_key, account, amount, capture_id) VALUES (?, ?, ?, ?, ?)`,
			c.ID, key, c.Account, c.Amount, c.Capture)
		return err
	})
	switch {
	case errors.Is(err, errNoAccount):
		writeError(w, http.StatusNotFound, "no such account")
	case err != nil:
		internal(w, "charging", err)
	default:
		writeJSON(w, http.StatusCreated, c)
	}
}
