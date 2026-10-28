// Package client is payd's SDK: it creates a charge, retrying with the
// same Idempotency-Key whenever the outcome is unknown or the service
// failed, which the key makes safe.
//
// It retries on a timeout (the request may have been processed), a reset
// connection (likewise), a refused one (nothing was sent) and a 5xx
// response, with exponential backoff, and on nothing else: a 4xx is the
// request's fault and asking again would not change it.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// A Client calls payd at BaseURL.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// MaxAttempts bounds the attempts at one charge (default 8).
	MaxAttempts int
	// Backoff is the wait before the first retry, doubled after each
	// (default 100ms) up to MaxBackoff (default 5s).
	Backoff, MaxBackoff time.Duration
	// Sleep waits between attempts (default: a timer, or until ctx is
	// done). Tests set it to their clock's.
	Sleep func(ctx context.Context, d time.Duration)
}

// New returns a client of payd at baseURL, sending through hc.
func New(baseURL string, hc *http.Client) *Client {
	return &Client{BaseURL: baseURL, HTTP: hc}
}

// Charge is a charge payd made.
type Charge struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	Amount  int64  `json:"amount"`
	Capture string `json:"capture"`
	Status  string `json:"status"`
}

// An APIError is a response other than a charge.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return fmt.Sprintf("payd: %d: %s", e.StatusCode, e.Message) }

// CreateCharge charges account amount, once for key: every attempt sends
// the same key, and payd answers a key it has seen with its charge.
func (c *Client) CreateCharge(ctx context.Context, key, account string, amount int64) (*Charge, error) {
	body, _ := json.Marshal(map[string]any{"account": account, "amount": amount})
	attempts, wait := c.MaxAttempts, c.Backoff
	if attempts <= 0 {
		attempts = 8
	}
	if wait <= 0 {
		wait = 100 * time.Millisecond
	}
	limit := c.MaxBackoff
	if limit <= 0 {
		limit = 5 * time.Second
	}
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			c.sleep(ctx, wait)
			wait = min(2*wait, limit)
		}
		ch, err := c.try(ctx, key, body)
		if err == nil {
			return ch, nil
		}
		if !Retryable(err) {
			return nil, err
		}
		last = err
	}
	return nil, fmt.Errorf("payd: no charge after %d attempts: %w", attempts, last)
}

func (c *Client) try(ctx context.Context, key string, body []byte) (*Charge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/charges", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(b, &e)
		return nil, &APIError{StatusCode: resp.StatusCode, Message: e.Error}
	}
	var ch Charge
	if err := json.Unmarshal(b, &ch); err != nil {
		return nil, fmt.Errorf("payd: a charge that is not one: %s", b)
	}
	return &ch, nil
}

// Retryable reports whether a charge's attempt that failed with err may be
// retried with its key: its outcome is unknown (a timeout, a reset), it
// was never sent (a refused connection), or payd failed (5xx).
func Retryable(err error) bool {
	if api, ok := errors.AsType[*APIError](err); ok {
		return api.StatusCode >= 500
	}
	if ue, ok := errors.AsType[*url.Error](err); ok && ue.Timeout() {
		return true
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return true
	}
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED)
}

func (c *Client) sleep(ctx context.Context, d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(ctx, d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
