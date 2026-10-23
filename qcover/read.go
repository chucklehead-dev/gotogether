package qcover

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadSummary reads a Summary as JSON: a Summary's own, or the "code" of
// an exploration's evidence (qtest.Evidence, written under
// GOTOGETHER_EVIDENCE or GOTOGETHER_KEEP), known by its format
// ("gotogether.evidence/N"), so the tools that compare coverage read
// either.
func ReadSummary(path string) (*Summary, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e struct {
		Format string          `json:"format"` // an Evidence's (qtest.EvidenceFormat)
		Code   json.RawMessage `json:"code"`
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("%s: %w (want a qcover.Summary or an evidence.json as JSON)", path, err)
	}
	if strings.HasPrefix(e.Format, "gotogether.evidence/") {
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
