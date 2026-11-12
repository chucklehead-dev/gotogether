package simlog_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
)

// A fixed workload and chooser must reproduce observable callback order even
// across partitions. This contract does not prescribe a particular partition
// ordering; every fresh run must match the first run of the same inputs.
func TestProducerPartitionCallbacksReplayDeterministically(t *testing.T) {
	for _, mode := range []struct {
		name string
		new  func() qenv.Chooser
	}{
		{"nominal", func() qenv.Chooser { return qenv.Nominal{} }},
		{"seeded", func() qenv.Chooser {
			c := qenv.NewSeeded(42)
			c.Rates = qenv.NoFaults
			return c
		}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			first := producerPartitionTrace(t, mode.new())
			for replay := 1; replay < 8; replay++ {
				if got := producerPartitionTrace(t, mode.new()); !slices.Equal(got, first) {
					t.Fatalf("replay %d changed callback order: got %v, first %v", replay, got, first)
				}
			}
		})
	}
}

type partitionAck struct{ partition, sequence int }

func producerPartitionTrace(t *testing.T, chooser qenv.Chooser) []partitionAck {
	t.Helper()
	const partitions, records = 24, 2
	s := qenv.New(chooser)
	s.Check = true
	defer s.Close()
	log := simlog.New(s, "partition-replay", partitions)
	keys := make([]string, partitions)
	found := 0
	for i := 0; i < 10_000 && found < partitions; i++ {
		key := fmt.Sprintf("key-%d", i)
		part := log.Partition(key)
		if keys[part] == "" {
			keys[part] = key
			found++
		}
	}
	if found != partitions {
		t.Fatalf("workload setup found keys for %d of %d partitions", found, partitions)
	}
	value := func(partition, sequence int) string { return fmt.Sprintf("%d/%d", partition, sequence) }
	acknowledged := make([][records]bool, partitions)
	var trace []partitionAck
	finished := false
	s.Spawn("producer", func(ctx context.Context) {
		p := log.Producer(true)
		p.InFlight = 1
		for sequence := range records {
			for i := range partitions {
				part := partitions - 1 - i
				p.Produce(ctx, keys[part], []byte(value(part, sequence)), func(err error) {
					if err != nil {
						t.Errorf("partition %d sequence %d: %v", part, sequence, err)
					}
					if acknowledged[part][sequence] {
						t.Errorf("partition %d sequence %d acknowledged more than once", part, sequence)
					}
					acknowledged[part][sequence] = true
					trace = append(trace, partitionAck{part, sequence})
				})
			}
		}
		p.Flush(ctx)
		finished = true
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if !finished || len(trace) != partitions*records {
		t.Fatalf("producer incomplete: finished=%t acknowledgments=%d, want %d", finished, len(trace), partitions*records)
	}
	for part := range partitions {
		landed := log.Records(part)
		if len(landed) != records {
			t.Fatalf("partition %d has %d records, want %d", part, len(landed), records)
		}
		for sequence, record := range landed {
			if !acknowledged[part][sequence] || record.Partition != part || record.Offset != sequence || record.Key != keys[part] || string(record.Value) != value(part, sequence) {
				t.Errorf("partition %d sequence %d: acknowledged=%t record=%+v", part, sequence, acknowledged[part][sequence], record)
			}
		}
	}
	return trace
}
