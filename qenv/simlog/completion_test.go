package simlog_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func completionSim(t *testing.T, chooser qenv.Chooser) (*qenv.Sim, *simlog.Log, *qobs.MemorySink) {
	t.Helper()
	sink := &qobs.MemorySink{}
	recorder := qobs.NewRecorder(sink, qobs.WithID("completion"), qobs.WithActor("producer"))
	s := qenv.New(qenv.WithContext(chooser, qobs.WithRecorder(t.Context(), recorder)))
	t.Cleanup(s.Close)
	return s, simlog.New(s, "events", 1), sink
}

func settleRecords(sink *qobs.MemorySink) int {
	n := 0
	for _, step := range sink.Steps() {
		if step.Settled {
			n++
		}
	}
	return n
}

func requireBusySender(t *testing.T, err error, sink *qobs.MemorySink) {
	t.Helper()
	var busy *qenv.Unsettled
	if !errors.As(err, &busy) || !strings.Contains(strings.Join(busy.Tasks, "\n"), "producer.sender") {
		t.Fatalf("unfinished sender must be named as unsettled: %v", err)
	}
	if n := settleRecords(sink); n != 0 {
		t.Fatalf("unfinished sender emitted %d settle records", n)
	}
}

func TestProducerSettlementWaitsForDeliveryCallback(t *testing.T) {
	s, log, sink := completionSim(t, qenv.Nominal{})
	completed := false
	s.Spawn("enqueue", func(ctx context.Context) {
		log.Producer(true).Produce(ctx, "order", []byte("accepted"), func(error) {
			qenv.Sleep(context.Background(), 2*time.Second)
			completed = true
		})
	})
	requireBusySender(t, s.Settle(time.Second), sink)
	if completed || len(log.Records(0)) != 1 {
		t.Fatal("callback did not remain pending after the append")
	}
	if err := s.Settle(2 * time.Second); err != nil || !completed || settleRecords(sink) != 1 {
		t.Fatalf("callback completion must let the idle sender settle: done=%t err=%v", completed, err)
	}
}

func TestProducerSettlementWaitsForRetryBackoff(t *testing.T) {
	s, log, sink := completionSim(t, script{"log.events.produce": {1}})
	completed := false
	s.Spawn("enqueue", func(ctx context.Context) {
		log.Producer(true).Produce(ctx, "order", []byte("accepted"), func(error) { completed = true })
	})
	if err := s.Run(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if completed || len(log.Records(0)) != 0 {
		t.Fatal("scripted lost produce did not leave an outstanding retry")
	}
	requireBusySender(t, s.Settle(5*time.Millisecond), sink)
	if err := s.Settle(time.Second); err != nil || !completed || len(log.Records(0)) != 1 || settleRecords(sink) != 1 {
		t.Fatalf("retry recovery must complete exactly once: done=%t records=%d err=%v", completed, len(log.Records(0)), err)
	}
}

func TestProducerIdleWaitEndsWhenNewWorkArrives(t *testing.T) {
	s, log, sink := completionSim(t, qenv.Nominal{})
	p := log.Producer(true)
	completed := 0
	s.Spawn("first", func(ctx context.Context) { p.Produce(ctx, "order", []byte("first"), func(error) { completed++ }) })
	if err := s.Settle(time.Second); err != nil || completed != 1 {
		t.Fatalf("idle sender: completed=%d err=%v", completed, err)
	}
	s.Spawn("second", func(ctx context.Context) {
		p.Produce(ctx, "order", []byte("second"), func(error) {
			qenv.Sleep(context.Background(), 2*time.Second)
			completed++
		})
	})
	var busy *qenv.Unsettled
	if err := s.Settle(time.Second); !errors.As(err, &busy) || completed != 1 || settleRecords(sink) != 1 {
		t.Fatalf("new work inherited idle exemption: completed=%d markers=%d err=%v", completed, settleRecords(sink), err)
	}
	if err := s.Settle(2 * time.Second); err != nil || completed != 2 || settleRecords(sink) != 2 {
		t.Fatalf("second completion: completed=%d markers=%d err=%v", completed, settleRecords(sink), err)
	}
}
