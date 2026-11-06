package qcover

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/formats"
)

// ReadSummary reads a Summary as JSON: a Summary's own, or the "code" of
// an exploration's evidence (qtest.Evidence, written under
// GOTOGETHER_EVIDENCE or GOTOGETHER_KEEP), known by its format
// (the current gotogether.evidence format), so the tools that compare coverage
// read either. Other evidence versions are refused, never interpreted as current.
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
		return nil, fmt.Errorf("%s: %w (want a qcover.Summary or an exploration's evidence as JSON)", path, err)
	}
	if strings.HasPrefix(e.Format, formats.Evidence.Family()) {
		if _, err := formats.Evidence.Check(e.Format); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if len(e.Code) == 0 || string(e.Code) == "null" {
			return nil, fmt.Errorf("%s: the evidence has no code coverage: weave the test with qweave -cover", path)
		}
		b = e.Code
	}
	var s Summary
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w (want a qcover.Summary or an exploration's evidence as JSON)", path, err)
	}
	return &s, nil
}
