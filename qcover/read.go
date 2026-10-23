package qcover

import (
	"encoding/json"
	"fmt"
	"os"
)

// ReadSummary reads a Summary as JSON: a Summary's own, or the "code" of
// an exploration's evidence.json (qtest.Evidence, written under
// GOTOGETHER_KEEP), so the tools that compare coverage read either.
func ReadSummary(path string) (*Summary, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e struct {
		Conform *int            `json:"conform"` // only an Evidence has it (keys match case-insensitively: not runs)
		Code    json.RawMessage `json:"code"`
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("%s: %w (want a qcover.Summary or an evidence.json as JSON)", path, err)
	}
	if e.Conform != nil {
		if len(e.Code) == 0 || string(e.Code) == "null" {
			return nil, fmt.Errorf("%s: the evidence has no code coverage: weave the test with qweave -cover", path)
		}
		b = e.Code
	}
	var s Summary
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w (want a qcover.Summary or an evidence.json as JSON)", path, err)
	}
	return &s, nil
}
