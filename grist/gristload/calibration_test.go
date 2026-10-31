package gristload_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/client"
	"github.com/chucklehead-dev/gotogether/grist/gristload"
)

// Calibration collects real simulated trajectories before fitting the timeout.
// Keep this separate from confirmation: Seeds=-1 skips recovery confirmation,
// but the explicitly requested calibration must still supply its own evidence.
func TestReportCalibratesFromCompleteTrajectories(t *testing.T) {
	app := shop()
	steps := recording(app, 200, 50*time.Millisecond, 5*time.Millisecond, 30*time.Millisecond, 0)
	s, err := gristload.Report(app, steps, gristload.Load{
		Rate: 2, Burst: gristload.Burst{Factor: 2, For: time.Second}, Timeout: time.Second,
		Client: &client.Client{MaxAttempts: 1}, Seeds: -1, Calibrate: true, MaxStates: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	setting := s.Get("client.attemptTimeout")
	if setting == nil || setting.Tool != "calibrated chain (CalibrateTimeout, λ held)" {
		t.Fatalf("timeout lacks completed calibration evidence: %+v", setting)
	}
	if setting.Because == "" || !strings.Contains(setting.Caveat, "service is exponential") {
		t.Fatalf("calibrated recommendation lacks its basis or limits: %+v", setting)
	}
}
