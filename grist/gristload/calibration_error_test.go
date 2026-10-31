package gristload

import (
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/qmeta"
)

// Report defaults public timeouts before building this state. At the boundary
// that consumes calibration evidence, however, an invalid simulation must stop
// fitting and leave the timeout recommendation unwritten.
func TestSizingCalibrationRejectsInvalidTrajectory(t *testing.T) {
	h := hop{name: "tenant t1", lambda: 1, workers: 1, mu: 10, svc: qmeta.Service{Mean: 100 * time.Millisecond}}
	z := &sizer{
		load: Load{Calibrate: true, MaxStates: 100, Timeout: 0, Burst: Burst{Factor: 2, For: time.Second}},
		rec:  retry{gateway: 1, c: &client.Client{MaxAttempts: 1}}, unit: map[string]int{}, out: &Sizing{},
	}
	err := z.timeout(h, h.svc)
	if err == nil {
		t.Fatal("invalid calibration simulation produced a recommendation")
	}
	for _, context := range []string{"calibrating tenant t1", "timeout 0s", "from queue", "orbit", "Timeout"} {
		if !strings.Contains(err.Error(), context) {
			t.Errorf("calibration error lacks %q: %v", context, err)
		}
	}
	if z.timeoutValue != 0 || len(z.out.Settings) != 0 {
		t.Fatalf("failed calibration emitted sizing: timeout %v, settings %+v", z.timeoutValue, z.out.Settings)
	}
}
