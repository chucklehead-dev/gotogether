package qenv_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// consume runs a queue's workers through crashes, lost acknowledgements
// and partitions under Check, and returns the panic it ended in, or "".
func consume(seed uint64, ordered, broken bool) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	c := qenv.NewSeeded(seed)
	c.Rates = qenv.DefaultRates.Set("nemesis", 0.05).Set("queue", 0.2)
	s := qenv.New(c)
	defer s.Close()
	s.Check = true
	broker := s.Process("broker", false, func(context.Context) {})
	q := qenv.NewQueue(s, "jobs", broker)
	q.Ordered = ordered
	if broken {
		qenv.BreakQueue(q)
	}
	s.OnCrash(func(p *qenv.Process) {
		if p != broker {
			p.Restart()
		}
	})
	var procs []*qenv.Process
	for _, name := range []string{"w1", "w2"} {
		procs = append(procs, s.Process(name, true, func(ctx context.Context) {
			for {
				d := q.Receive(ctx)
				qenv.Sleep(ctx, time.Millisecond)
				if s.Choose("work.nack", 4) == 0 {
					d.Nack(ctx)
				} else {
					d.Ack(ctx)
				}
			}
		}))
	}
	s.Spawn("client", func(ctx context.Context) {
		for i := 0; i < 8; i++ {
			q.Publish(ctx, i)
		}
	})
	s.Net().PartitionNemesis(append(procs, broker)...)
	if err := s.Run(time.Second); err != nil {
		return err.Error()
	}
	return ""
}

// The queue keeps its contract through crashes, lost acknowledgements,
// nacks and partitions, ordered or not; a queue that forgets a crashed
// consumer's messages is caught losing one.
func TestQueueContract(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		for seed := uint64(0); seed < 40; seed++ {
			if msg := consume(seed, ordered, false); msg != "" {
				t.Fatalf("ordered=%v seed %d: %s", ordered, seed, msg)
			}
		}
	}
	caught := 0
	for seed := uint64(0); seed < 40; seed++ {
		msg := consume(seed, false, true)
		if msg == "" {
			continue
		}
		if !strings.Contains(msg, "was lost, neither acknowledged nor pending nor held") {
			t.Fatalf("seed %d: %s", seed, msg)
		}
		caught++
	}
	if caught == 0 {
		t.Error("the broken queue never lost a message in 40 runs")
	}
}
