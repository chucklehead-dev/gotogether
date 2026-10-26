// Mutant noForwardKey (a go build overlay of payd/processor.go): the
// capture is sent without the Idempotency-Key, so the processor cannot
// tell a retried capture from a new one. The charge's reference is still
// in the body, which the processor records and does not dedupe on.

package payd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// captureRequest is the processor's POST /v1/captures body: the amount,
// the account it is charged to, and our reference for it (the key).
type captureRequest struct {
	Amount    int64  `json:"amount"`
	Source    string `json:"source"`
	Reference string `json:"reference"`
}

// capture asks the processor to capture amount from account, as the
// charge of key, and returns the capture's id. The processor dedupes by
// the Idempotency-Key: asked again with it, it returns the same capture.
func (s *server) capture(ctx context.Context, key, account string, amount int64) (string, error) {
	b, _ := json.Marshal(captureRequest{Amount: amount, Source: account, Reference: key})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.procURL+"/v1/captures", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.proc.Do(req)
	if err != nil {
		return "", fmt.Errorf("processor: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("processor: reading its response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("processor: %s: %s", resp.Status, bytes.TrimSpace(body))
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &c); err != nil || c.ID == "" {
		return "", fmt.Errorf("processor: a capture without an id: %s", body)
	}
	return c.ID, nil
}
