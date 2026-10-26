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
		read, err := qtrace.ReadJSONL(bytes.NewReader(data))
		var steps, losses int // a torn tail's steps end in its loss record
		for _, s := range read {
			if s.Model() {
				steps++
			} else if s.Lost == "suffix" {
				losses++
			}
		}
		var torn *qtrace.TornTail
		if synced {
			if steps < returned {
				t.Errorf("synced: %d steps in the log, %d recorded (%v)", steps, returned, err)
			}
		} else if !errors.As(err, &torn) || steps >= returned || losses != 1 {
			t.Errorf("unsynced: %d steps in the log of %d recorded, %d loss records, err %v: want steps lost, a torn tail, and its loss", steps, returned, losses, err)
		}
		s.Close()
	}
}

// byKind plays each fault kind's values in turn, every other choice
// nominal.
type byKind map[string][]int

func (b byKind) Choose(kind string, n int) int {
	vs := b[kind]
	if len(vs) == 0 {
		return 0
	}
	b[kind] = vs[1:]
	return vs[0]
}

// The disk's I/O errors: a write to a full disk writes nothing; a failed
// fsync loses its writes to the next power loss even after a later fsync
// succeeds (Linux's "fsyncgate"), though they stay readable until then;
// a corrupt read flips a byte of what it returns, not of the file.
func TestDiskErrors(t *testing.T) {
	run := func(faults byKind, body func(ctx context.Context, d *qenv.Disk, p *qenv.Process)) *qenv.Disk {
		s := qenv.New(faults)
		var d *qenv.Disk
		var p *qenv.Process
		p = s.Process("node", false, func(ctx context.Context) { body(ctx, d, p) })
		d = qenv.NewDisk(s, "node", p)
		if err := s.Run(time.Second); err != nil {
			t.Fatal(err)
		}
		s.Close()
		return d
	}
	var werr error
	d := run(byKind{"fault.disk.node.write": {1}}, func(ctx context.Context, d *qenv.Disk, _ *qenv.Process) {
		_, werr = d.Create("log").Write([]byte("full\n"))
	})
	if data, _ := d.ReadFile("log"); !errors.Is(werr, qenv.ErrNoSpace) || len(data) != 0 {
		t.Errorf("a full disk: err %v, the file %q", werr, data)
	}

	var first, second error
	var before []byte
	d = run(byKind{"fault.disk.node.sync": {1}, "fault.disk.node.crash": {1}}, func(ctx context.Context, d *qenv.Disk, p *qenv.Process) {
		f := d.Create("log")
		_, _ = f.Write([]byte("lost\n"))
		first = f.Sync()  // EIO: the write is marked clean, never written
		second = f.Sync() // succeeds, and writes nothing
		before, _ = d.ReadFile("log")
		p.Crash() // a power loss
	})
	after, _ := d.ReadFile("log")
	if !errors.Is(first, qenv.ErrIO) || second != nil || string(before) != "lost\n" || len(after) != 0 {
		t.Errorf("fsyncgate: syncs %v then %v, read %q before the power loss and %q after", first, second, before, after)
	}

	// a program that writes its data again after the error, and syncs, keeps
	// it: only the dropped writes are lost
	d = run(byKind{"fault.disk.node.sync": {1}, "fault.disk.node.crash": {1}}, func(ctx context.Context, d *qenv.Disk, p *qenv.Process) {
		f := d.Create("log")
		_, _ = f.Write([]byte("dropped\n"))
		_ = f.Sync() // EIO
		_, _ = f.Write([]byte("again\n"))
		_ = f.Sync()
		p.Crash()
	})
	if after, _ := d.ReadFile("log"); string(after) != "again\n" {
		t.Errorf("rewritten after EIO: %q after the power loss, want the rewrite only", after)
	}

	d = run(byKind{"fault.disk.node.read": {1}}, func(ctx context.Context, d *qenv.Disk, _ *qenv.Process) {
		f := d.Create("log")
		_, _ = f.Write([]byte("abc"))
		_ = f.Sync()
		before, _ = d.ReadFile("log") // corrupt
	})
	again, _ := d.ReadFile("log") // the file itself is whole
	if string(before) != "a\x9dc" || string(again) != "abc" {
		t.Errorf("a corrupt read: %q, then %q", before, again)
	}
}
