package qenv_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// crashAs makes every choice nominal but the disk's crash, which it makes
// option opt (0 nominal, 1 powerLoss, 2 tornWrite), whatever its name.
type crashAs int

func (c crashAs) Choose(kind string, n int) int {
	if strings.HasPrefix(kind, "fault.disk.") && strings.HasSuffix(kind, ".crash") {
		return int(c)
	}
	return 0
}

// A process writes three lines, syncs after the first, and its machine
// crashes: a process crash keeps them all, a power loss the synced one,
// a torn write the synced one, the second, and half of the third.
func TestDiskCrash(t *testing.T) {
	for opt, want := range []string{"one\ntwo\nthree\n", "one\n", "one\ntwo\nthr"} {
		s := qenv.New(crashAs(opt))
		var d *qenv.Disk
		p := s.Process("node", false, func(ctx context.Context) {
			f := d.Create("log")
			f.Write([]byte("one\n"))
			f.Sync()
			f.Write([]byte("two\n"))
			f.Write([]byte("three\n"))
		})
		d = qenv.NewDisk(s, "disk", p)
		if err := s.Run(time.Second); err != nil {
			t.Fatal(err)
		}
		p.Crash()
		got, _ := d.ReadFile("log")
		if string(got) != want {
			t.Errorf("crash option %d: %q, want %q", opt, got, want)
		}
		s.Close()
	}
}

// A step log on a disk, across a power loss. Written as FileSink writes
// it (no sync), the steps recorded last are lost and the line being
// written is torn: ReadJSONL keeps the rest and says so. Synced after
// every step, every step whose Record returned is in the log.
func TestStepLogAcrossPowerLoss(t *testing.T) {
	for _, synced := range []bool{false, true} {
		s := qenv.New(crashAs(2)) // torn
		var d *qenv.Disk
		returned := 0
		p := s.Process("node", false, func(ctx context.Context) {
			f := d.Create("steps.jsonl")
			sink := qobs.NewJSONLSink(f, qobs.PerStep)
			if synced {
				sink = qobs.NewJSONLSink(f, qobs.Synced)
			}
			rec := qobs.NewRecorder(sink)
			for i := 1; i <= 5; i++ {
				rec.Record(ctx, "tick", nil, "i", i)
				returned++
				if i == 2 && !synced {
					f.Sync() // a log rotation, a checkpoint: once
				}
			}
		})
		d = qenv.NewDisk(s, "disk", p)
		s.Spawn("power", func(ctx context.Context) {
			for returned < 4 {
				qenv.Yield(ctx)
			}
			p.Crash() // the machine goes down mid-write
		})
		if err := s.Run(time.Second); err != nil {
			t.Fatal(err)
		}
		data, _ := d.ReadFile("steps.jsonl")
		steps, err := qtrace.ReadJSONL(bytes.NewReader(data))
		var torn *qtrace.TornTail
		if synced {
			if len(steps) < returned {
				t.Errorf("synced: %d steps in the log, %d recorded (%v)", len(steps), returned, err)
			}
		} else if !errors.As(err, &torn) || len(steps) >= returned {
			t.Errorf("unsynced: %d steps in the log of %d recorded, err %v: want steps lost and a torn tail", len(steps), returned, err)
		}
		s.Close()
	}
}
