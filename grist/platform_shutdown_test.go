package grist_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
)

// Main owns its processors through their cancellation cleanup. Returning
// before that cleanup closes the database and recording sink underneath it.
func TestMainJoinsProcessorCancellation(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a, added := amoApp(func(ctx context.Context, m grist.Meta) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return ctx.Err()
	})
	grist.NewCommand(a, "add", func(r *grist.Reads, c Added) ([]grist.Event, error) {
		return []grist.Event{added.Of(c)}, nil
	}, grist.Authorize(grist.Authenticated))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	log, url, done := serving(t, ctx, a, t.TempDir(), []string{"-addr", "127.0.0.1:0", "-health-addr", ""},
		grist.WithDrain(grist.Drain{Delay: -1, Settle: -1}))
	c := &client.Client{App: a, URL: url, CallerHeader: "X-Caller"}
	if _, err := c.Command(ctx, grist.Envelope{ID: "k1", Tenant: "t1", Caller: "x"}, "add", []byte(`{"by":1}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("processor did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("processor did not observe cancellation")
	}
	select {
	case err := <-done:
		t.Fatalf("Main returned before its processor finished cancellation cleanup: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if strings.Contains(log.String(), `"phase":"flush"`) {
		t.Error("Main completed its final relay flush before its processor finished cancellation cleanup")
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Main did not return after its processor finished cancellation cleanup")
	}
}
