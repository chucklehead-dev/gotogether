package payd

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// createCharge is POST /v1/charges: claim the key, capture, complete (the
// package's doc says why in three steps).
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
	hash := requestHash(req)

	// 1. claim the key, or find what it answered
	var (
		claimed bool
		prev    struct {
			hash, status string
			code         sql.NullInt64
			body         sql.NullString
		}
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		claimed = false
		var balance int64
		if err := tx.QueryRowContext(ctx, `SELECT balance FROM accounts WHERE id = ?`, req.Account).Scan(&balance); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errNoAccount
			}
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO idempotency (key, req_hash, status) VALUES (?, ?, 'pending') ON CONFLICT (key) DO NOTHING`, key, hash)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 1 {
			claimed = true
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT req_hash, status, code, body FROM idempotency WHERE key = ?`, key).
			Scan(&prev.hash, &prev.status, &prev.code, &prev.body)
	})
	switch {
	case errors.Is(err, errNoAccount):
		writeError(w, http.StatusNotFound, "no such account")
		return
	case err != nil:
		internal(w, "claiming the idempotency key", err)
		return
	case !claimed && prev.hash != hash:
		writeError(w, http.StatusConflict, "the Idempotency-Key was used with another request")
		return
	case !claimed && prev.status == "done":
		w.Header().Set("Idempotent-Replayed", "true")
		writeRaw(w, int(prev.code.Int64), []byte(prev.body.String))
		return
	}
	// claimed now, or pending since an attempt that did not finish (it
	// crashed, or its client gave up on it, or it is still running): drive
	// it on, the processor deduping the capture by the key

	// 2. capture
	capture, err := s.capture(ctx, key, req.Account, req.Amount)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// 3. complete: the first to get here charges; another finds it done
	var (
		completed bool
		code      int
		body      []byte
	)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		completed, code, body = false, 0, nil
		res, err := tx.ExecContext(ctx, `UPDATE idempotency SET status = 'done' WHERE key = ? AND status = 'pending'`, key)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			if err != nil {
				return err
			}
			var c sql.NullInt64
			var b sql.NullString
			if err := tx.QueryRowContext(ctx, `SELECT code, body FROM idempotency WHERE key = ?`, key).Scan(&c, &b); err != nil {
				return err
			}
			code, body = int(c.Int64), []byte(b.String)
			return nil
		}
		completed = true
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance = balance - ?, n_charges = n_charges + 1 WHERE id = ?`, req.Amount, req.Account); err != nil {
			return err
		}
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT n_charges FROM accounts WHERE id = ?`, req.Account).Scan(&n); err != nil {
			return err
		}
		c := Charge{ID: fmt.Sprintf("ch_%s_%d", req.Account, n), Account: req.Account, Amount: req.Amount, Capture: capture, Status: "succeeded"}
		if _, err := tx.ExecContext(ctx, `INSERT INTO charges (id, idem_key, account, amount, capture_id) VALUES (?, ?, ?, ?, ?)`,
			c.ID, key, c.Account, c.Amount, c.Capture); err != nil {
			return err
		}
		code = http.StatusCreated
		body, _ = json.Marshal(c)
		_, err = tx.ExecContext(ctx, `UPDATE idempotency SET code = ?, body = ?, charge_id = ? WHERE key = ?`, code, string(body), c.ID, key)
		return err
	})
	if err != nil {
		internal(w, "completing the charge", err)
		return
	}
	if !completed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeRaw(w, code, body)
}

// requestHash identifies a request by what it asks, not how it is
// written: a retry with the same key must ask the same.
func requestHash(req chargeRequest) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", req.Account, req.Amount)))
	return hex.EncodeToString(h[:16])
}
