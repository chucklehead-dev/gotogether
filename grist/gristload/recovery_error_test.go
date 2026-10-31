package gristload

import (
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/qmeta"
)

// Load.Seeds < 0 explicitly skips simulation in Report. Once confirmation is
// requested, however, an invalid batch must be an error rather than zero
// recovered seeds or a numeric failing-burst margin.
func TestSizingRecoveryRequiresJudgedBatch(t *testing.T) {
	h := hop{name: "tenant t1", lambda: 1, workers: 1, mu: 10, svc: qmeta.Service{Mean: 100 * time.Millisecond}}
	r := retry{gateway: 1, c: &client.Client{MaxAttempts: 1}}
	for _, n := range []int{0, -1} {
		z := &sizer{load: Load{Seeds: n, Within: time.Second, Burst: Burst{Factor: 2, For: time.Second}}}
		if count, err := z.confirm(h, r, time.Second); err == nil || count != 0 || !strings.Contains(err.Error(), "confirming tenant t1") || !strings.Contains(err.Error(), "seed") {
			t.Errorf("confirm with %d seeds = %d, %v; want contextual batch error", n, count, err)
		}
		if margin, err := z.margin(h, r, time.Second); err == nil || margin != 0 || !strings.Contains(err.Error(), "tenant t1's recovery margin") || !strings.Contains(err.Error(), "burst 8x1s") || !strings.Contains(err.Error(), "seed") {
			t.Errorf("margin with %d seeds = %g, %v; want contextual batch error", n, margin, err)
		}
	}
}
