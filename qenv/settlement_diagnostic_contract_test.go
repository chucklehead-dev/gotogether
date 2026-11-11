package qenv_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestSettleReportsDownProcessAndTimedWork(t *testing.T) {
	s, _ := downSettleSim(t)
	p := s.Process("offline", false, func(context.Context) {})
	p.Crash()
	s.Spawn("delayed-client", func(ctx context.Context) { qenv.Sleep(ctx, 10*time.Second) })
	err := s.Settle(time.Second)
	var unsettled *qenv.Unsettled
	if !errors.As(err, &unsettled) || errors.Is(err, qenv.ErrStuck) {
		t.Fatalf("settlement must report unfinished timed work, without a stuck cause: %v", err)
	}
	if len(unsettled.DownProcesses) != 1 || len(unsettled.Tasks) != 1 {
		t.Fatalf("lost unavailable process or pending task: %+v", unsettled)
	}
	message := err.Error()
	for _, want := range []string{"offline#1", "delayed-client"} {
		if !strings.Contains(message, want) {
			t.Errorf("diagnostic %q omits %q", message, want)
		}
	}
	if strings.Contains(message, "<nil>") {
		t.Errorf("diagnostic invented a cause: %q", message)
	}
}
