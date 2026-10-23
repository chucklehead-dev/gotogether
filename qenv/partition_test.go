package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A call across a partition is lost, both ways, until it heals; a task of
// no process is never partitioned.
func TestPartition(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	echo := func(ctx context.Context, n int) (int, error) { return n, nil }
	server := s.Process("server", false, func(context.Context) {})
	svc := qenv.NewService(s, "echo", server, echo)
	var results []error
	var client *qenv.Process
	client = s.Process("client", false, func(ctx context.Context) {
		call := func() {
			_, err := svc.Call(ctx, 1, 100*time.Millisecond)
			results = append(results, err)
		}
		call()
		s.Net().Partition(client, server)
		call()
		s.Net().Heal(client, server)
		call()
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	var outside error
	s.Spawn("outsider", func(ctx context.Context) {
		s.Net().Partition(client, server)
		_, outside = svc.Call(ctx, 1, 100*time.Millisecond)
		s.Net().Heal(client, server)
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0] != nil || !errors.Is(results[1], qenv.ErrTimeout) || results[2] != nil {
		t.Errorf("before, during, after the partition: %v", results)
	}
	if outside != nil {
		t.Errorf("a task of no process was partitioned: %v", outside)
	}
}

// Partitions as nemeses cut and heal links at the nemesis rate, and Settle
// heals them all: a client that retries gets through once it settles.
func TestPartitionsNemesisAndSettle(t *testing.T) {
	cut := 0
	for seed := uint64(1); seed <= 20; seed++ {
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.NoFaults.Set("nemesis", 0.2)
		s := qenv.New(c)
		server := s.Process("server", false, func(context.Context) {})
		svc := qenv.NewService(s, "echo", server, func(ctx context.Context, n int) (int, error) { return n, nil })
		failed, done := 0, false
		client := s.Process("client", false, func(ctx context.Context) {
			for i := 0; i < 50; i++ {
				if _, err := svc.Call(ctx, i, 10*time.Millisecond); err != nil {
					failed++
				}
			}
			for {
				if _, err := svc.Call(ctx, 0, 10*time.Millisecond); err == nil {
					done = true
					return
				}
			}
		})
		s.Net().PartitionNemesis(client, server)
		if err := s.Run(2 * time.Second); err != nil {
			t.Fatal(err)
		}
		if err := s.Settle(time.Minute); err != nil {
			t.Fatal(err)
		}
		if !done {
			t.Fatalf("seed %d: the client never got through after Settle", seed)
		}
		if failed > 0 {
			cut++
		}
		s.Close()
	}
	if cut == 0 {
		t.Error("no run was ever partitioned")
	}
}

// A queue's broker partitioned from a consumer drops the consumer's
// connection: the message it holds is redelivered and its acknowledgement
// is lost. A publisher cut off waits until the partition heals. A queue
// with no broker process is never partitioned.
func TestQueuePartition(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	broker := s.Process("broker", false, func(context.Context) {})
	q := qenv.NewQueue(s, "jobs", broker)
	var got []string
	var consumer, publisher *qenv.Process
	consumer = s.Process("consumer", false, func(ctx context.Context) {
		d := q.Receive(ctx)
		s.Net().Partition(consumer, broker)
		d.Ack(ctx) // lost: the cut dropped the connection
		s.Net().Heal(consumer, broker)
		for i := 0; i < 2; i++ {
			d := q.Receive(ctx)
			got = append(got, fmt.Sprintf("%v redelivered=%v", d.Body, d.Redelivered))
			d.Ack(ctx)
		}
	})
	var publishedAt time.Duration
	publisher = s.Process("publisher", false, func(ctx context.Context) {
		q.Publish(ctx, "a")
		s.Net().Partition(publisher, broker)
		qenv.Go(ctx, "healer", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Second)
			s.Net().Heal(publisher, broker)
		})
		start := s.Now()
		q.Publish(ctx, "b")
		publishedAt = s.Now().Sub(start)
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a redelivered=true", "b redelivered=false"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("deliveries %v, want %v", got, want)
	}
	if publishedAt != time.Second {
		t.Errorf("the cut-off publish returned after %v, want once the partition healed (1s)", publishedAt)
	}
	if q.Pending() != 0 || q.InFlight() != 0 {
		t.Errorf("%d pending, %d in flight", q.Pending(), q.InFlight())
	}

	free := qenv.NewQueue(s, "free", nil)
	s.Net().Partition(publisher, broker)
	s.Spawn("on a cut network", func(ctx context.Context) { free.Publish(ctx, "c") })
	if err := s.Run(time.Minute); err != nil || free.Pending() != 1 {
		t.Errorf("a queue with no broker process was partitioned: %v, %d pending", err, free.Pending())
	}
}

// A cut heals by itself after Network.CutLength: a publisher waiting on a
// cut, with nothing else to run, would otherwise wait forever, since a
// nemesis acts only at a scheduling step (the run was stuck). The heal
// nemesis may end a cut sooner.
func TestPartitionsHealByThemselves(t *testing.T) {
	waited := 0
	for seed := uint64(1); seed <= 20; seed++ {
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.NoFaults.Set("nemesis", 0.5)
		s := qenv.New(c)
		s.Net().CutLength = 3 * time.Second
		broker := s.Process("broker", false, func(context.Context) {})
		q := qenv.NewQueue(s, "jobs", broker)
		var took time.Duration
		pub := s.Process("publisher", false, func(ctx context.Context) {
			start := s.Now()
			for i := 0; i < 5; i++ {
				q.Publish(ctx, i)
			}
			took = s.Now().Sub(start)
		})
		s.Net().PartitionNemesis(pub, broker)
		if err := s.Run(time.Minute); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		s.Close()
		if q.Published != 5 {
			t.Fatalf("seed %d: %d of 5 published", seed, q.Published)
		}
		if took > 0 {
			waited++
		}
		if took > 5*s.Net().CutLength {
			t.Errorf("seed %d: publishing took %v, beyond a cut's length per message", seed, took)
		}
	}
	if waited == 0 {
		t.Error("no run's publisher waited on a cut")
	}
}

// Each nemesis family is a choice of its own at its own rate: partitions
// off ("nemesis.partition" 0) leave crashes as they were, and every
// nemesis enabled at a choice is noted by name, fired or not. A Script's
// pick by name is recorded as the family's choice, which replays.
func TestNemesisFamilies(t *testing.T) {
	run := func(c qenv.Chooser) (*qenv.ChoiceLog, int) {
		log := &qenv.ChoiceLog{C: c}
		s := qenv.New(log)
		defer s.Close()
		a := s.Process("a", true, func(ctx context.Context) {
			for range 20 { // short enough to finish between crashes
				qenv.Yield(ctx)
			}
		})
		b := s.Process("b", false, func(context.Context) {})
		s.Net().PartitionNemesis(a, b)
		cuts := 0
		s.Log = func(line string) {
			if strings.Contains(line, "nemesis partition.") {
				cuts++
			}
		}
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		return log, cuts
	}
	c := qenv.NewSeeded(1)
	c.Rates = qenv.NoFaults.Set("nemesis", 0.1).Set("nemesis.partition", 0)
	log, cuts := run(c)
	kinds := map[string]bool{}
	crashes := 0
	for _, ch := range log.Choices {
		kinds[ch.Kind] = true
		if ch.Kind == "fault.nemesis.crash" && ch.Value > 0 {
			crashes++
		}
	}
	if cuts != 0 || crashes == 0 || !kinds["fault.nemesis.partition"] || kinds["fault.nemesis"] {
		t.Errorf("cuts %d, crashes %d, kinds %v", cuts, crashes, kinds)
	}
	if strings.Join(log.Nemeses, " ") != "crash.a partition.a.b" {
		t.Errorf("offered %v", log.Nemeses)
	}

	sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.partition.a.b"}}}
	log, cuts = run(sc)
	if cuts != 1 {
		t.Fatalf("the scripted partition: %d cuts", cuts)
	}
	again, cuts := run(qenv.Replay(log.Choices))
	if cuts != 1 || len(again.Choices) != len(log.Choices) {
		t.Errorf("the script's run replayed with %d cuts, %d of %d choices", cuts, len(again.Choices), len(log.Choices))
	}
}

// A family a wrapper turns off reaches no log inside it: no choice, and no
// nemesis offered.
func TestNemesisOffWrapped(t *testing.T) {
	log := &qenv.ChoiceLog{C: qenv.NewSeeded(1)}
	s := qenv.New(qenv.Without(log, "nemesis.partition"))
	defer s.Close()
	a := s.Process("a", true, func(ctx context.Context) {
		for range 20 {
			qenv.Yield(ctx)
		}
	})
	b := s.Process("b", false, func(context.Context) {})
	s.Net().PartitionNemesis(a, b)
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, c := range log.Choices {
		if c.Kind == "fault.nemesis.partition" {
			t.Fatalf("a partition choice reached the log: %v", c)
		}
	}
	if strings.Join(log.Nemeses, " ") != "crash.a" {
		t.Errorf("offered %v, want crash.a only", log.Nemeses)
	}
}

// A task spawned into a process from outside is the process's: a cut
// reaches it, and a crash kills it.
func TestProcessSpawn(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	server := s.Process("server", false, func(context.Context) {})
	svc := qenv.NewService(s, "echo", server, func(ctx context.Context, n int) (int, error) { return n, nil })
	node := s.Process("node", true, func(context.Context) {})
	s.Net().Partition(node, server)
	var err error
	node.Spawn("work", func(ctx context.Context) { _, err = svc.Call(ctx, 1, 10*time.Millisecond) })
	ran := false
	node.Spawn("long", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second)
		ran = true
	})
	s.Spawn("crasher", func(ctx context.Context) {
		qenv.Sleep(ctx, 100*time.Millisecond)
		node.Crash()
	})
	if runErr := s.Run(time.Minute); runErr != nil {
		t.Fatal(runErr)
	}
	if !errors.Is(err, qenv.ErrTimeout) || ran {
		t.Errorf("the spawned task's call: %v (want a timeout across the cut); the long task ran after the crash: %v", err, ran)
	}
}

// A cut made by hand over one the nemesis made lasts until Heal: the
// nemesis's healer does not end it.
func TestManualCutOverNemesis(t *testing.T) {
	sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.partition.a.b"}}}
	s := qenv.New(sc)
	defer s.Close()
	a := s.Process("a", false, func(context.Context) {})
	b := s.Process("b", false, func(context.Context) {})
	s.Net().CutLength = 10 * time.Millisecond
	s.Net().PartitionNemesis(a, b)
	cutAfter := false
	s.Spawn("driver", func(ctx context.Context) {
		if !qenv.WaitFor(ctx, func() bool { return s.Net().Partitioned(a, b) }, time.Second) {
			t.Error("the scripted cut never came")
			return
		}
		s.Net().Partition(a, b) // by hand, over the nemesis's
		qenv.Sleep(ctx, 50*time.Millisecond)
		cutAfter = s.Net().Partitioned(a, b)
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if !cutAfter {
		t.Error("the nemesis's healer ended a cut made by hand")
	}
}

// The heal nemesis heals the nemesis's cuts only: a cut made by hand, at
// any nemesis rate, lasts until Heal.
func TestHealNemesisSparesHandCuts(t *testing.T) {
	for seed := uint64(1); seed <= 10; seed++ {
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.NoFaults.Set("nemesis", 0.5)
		s := qenv.New(c)
		a := s.Process("a", false, func(context.Context) {})
		b := s.Process("b", false, func(context.Context) {})
		s.Net().PartitionNemesis(a, b)
		s.Net().Partition(a, b)
		held := true
		s.Spawn("watch", func(ctx context.Context) {
			for range 50 {
				qenv.Yield(ctx)
				held = held && s.Net().Partitioned(a, b)
			}
		})
		if err := s.Run(time.Second); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if !held {
			t.Fatalf("seed %d: a nemesis healed a cut made by hand", seed)
		}
	}
}

// A queue's broker that crashes drops its consumers' connections, as a
// partition does: the message a consumer holds is redelivered and its
// acknowledgement lost. While it is down a publish waits for its restart,
// and the messages it had are kept. A down broker once served on.
func TestQueueBrokerCrash(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	broker := s.Process("broker", true, func(context.Context) {})
	q := qenv.NewQueue(s, "jobs", broker)
	var got []string
	var publishedAt time.Duration
	s.Process("consumer", false, func(ctx context.Context) {
		d := q.Receive(ctx)
		broker.Crash()
		qenv.Go(ctx, "operator", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Second)
			broker.Restart()
		})
		d.Ack(ctx) // lost: the crash dropped the connection
		start := s.Now()
		q.Publish(ctx, "b")
		publishedAt = s.Now().Sub(start)
		for i := 0; i < 2; i++ {
			d := q.Receive(ctx)
			got = append(got, fmt.Sprintf("%v redelivered=%v", d.Body, d.Redelivered))
			d.Ack(ctx)
		}
	})
	s.Spawn("publisher", func(ctx context.Context) { q.Publish(ctx, "a") })
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a redelivered=true", "b redelivered=false"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("deliveries %v, want %v", got, want)
	}
	if publishedAt != time.Second {
		t.Errorf("the publish to a down broker returned after %v, want once it restarted (1s)", publishedAt)
	}
}

// An Ordered queue redelivers the messages a dropped connection held
// oldest first, as a FIFO broker does: they came back last first.
func TestOrderedRedeliveryAfterBrokerCrash(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	broker := s.Process("broker", true, func(context.Context) {})
	q := qenv.NewQueue(s, "jobs", broker)
	q.Ordered = true
	var got []any
	s.Process("consumer", false, func(ctx context.Context) {
		q.Publish(ctx, "a")
		q.Publish(ctx, "b")
		q.Receive(ctx)
		q.Receive(ctx)
		broker.Crash()
		broker.Restart()
		for i := 0; i < 2; i++ {
			d := q.Receive(ctx)
			got = append(got, d.Body)
			d.Ack(ctx)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[a b]" {
		t.Errorf("redelivered %v, want [a b]", got)
	}
}
