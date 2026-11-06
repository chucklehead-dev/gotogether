package publisher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/edgepublish/publisher"
)

// A rotated generation's background seal must finish independently of a
// current-generation push. Its writes can be held by the MBT driver until
// later model steps: taking the generation lock after sealing would wait for
// those pushes and prevent the driver from starting another one.
func TestBackgroundSealDoesNotWaitForCurrentPush(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	sealStarted := make(chan struct{})
	releaseSeal := make(chan struct{})
	tableStarted := make(chan struct{}, 1)
	unseal := sync.OnceFunc(func() { close(releaseSeal) })
	store := publisher.NewMemStore()
	store.Hook = func(ctx context.Context, op publisher.Op) publisher.Fault {
		if op.Kind == "table" {
			tableStarted <- struct{}{}
			<-ctx.Done()
			return publisher.Fail
		}
		close(sealStarted)
		<-releaseSeal
		return publisher.Apply
	}
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	p := publisher.New(publisher.Config{Epoch: "e1", Now: func() time.Time { return now }}, store)
	var pushes, drains sync.WaitGroup
	defer func() {
		unseal()
		cancel()
		pushes.Wait()
		p.Drain()
		drains.Wait()
	}()
	if err := p.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := p.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	<-sealStarted
	pushes.Go(func() { _ = p.Push(ctx, []byte("held current generation")) })
	<-tableStarted // the push holds the current generation's shared lock
	unseal()
	drained := make(chan struct{})
	drains.Go(func() {
		p.Drain()
		close(drained)
	})
	// RWMutex contention is not a durable synctest wait. A bounded native
	// wait checks this ownership boundary without relying on sleeps.
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("background seal waited for an unrelated current-generation push")
	}
}
