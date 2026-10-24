package qenv

import (
	"context"
	"errors"
	"sort"
)

// ErrNoSpace is a write that failed for a full disk (ENOSPC): nothing of
// it was written.
var ErrNoSpace = errors.New("qenv: no space left on device")

// ErrIO is an fsync that failed (EIO). As Linux does, the writes it was to
// make durable are marked clean and dropped: still readable, never on the
// disk, and a later fsync succeeds without them ("fsyncgate").
var ErrIO = errors.New("qenv: input/output error")

// Disk is a machine's disk: files whose writes reach the OS at once and
// the disk only when synced (File.Sync, fsync). A crash of the process it
// belongs to is a fault point ("disk.<name>.crash"): nominal, the process
// died and the OS kept its writes not yet synced (its page cache);
// "powerLoss", the machine lost power and they are gone; "tornWrite", it
// lost power during the last of them, which reached the disk only in
// part. It is the double that tells "written" from
// "durable": a log that never syncs survives every process crash and
// loses its tail to the first power loss.
//
// Its other faults: "disk.<name>.write" ("noSpace": ENOSPC, nothing
// written), "disk.<name>.sync" ("error": a failed fsync, whose writes are
// lost to the next power loss though a later fsync succeeds) and
// "disk.<name>.read" ("corrupt": a read returns a byte flipped, as bit rot
// does). A program that trusts a retried fsync, or does not checksum what
// it reads back, meets them.
//
// Files are byte slices, read whole; writes and syncs are seams.
type Disk struct {
	Name  string
	sim   *Sim
	proc  *Process
	files map[string]*diskFile
	fault FaultKind // "disk.<name>.crash"
	// its I/O faults: a full disk, a failed fsync, a corrupt read
	write, sync, read FaultKind
}

type diskFile struct {
	data   []byte // what the machine reads: synced and not
	synced int    // the prefix of data on the disk
	starts []int  // where each write since the last sync began
	// lost are the ranges of writes failed fsyncs dropped: a power loss
	// loses them, whatever a later fsync said, and keeps what was written
	// and synced after (a program that writes its data again)
	lost [][2]int
}

// durable is what of the file a power loss keeps: the synced writes but
// those a failed fsync dropped.
func (f *diskFile) durable() []byte {
	var out []byte
	at := 0
	for _, r := range f.lost {
		if r[0] >= f.synced {
			break
		}
		out = append(out, f.data[at:r[0]]...)
		at = min(r[1], f.synced)
	}
	return append(out, f.data[at:f.synced]...)
}

// NewDisk returns an empty disk of process p's machine.
func NewDisk(s *Sim, name string, p *Process) *Disk {
	s.claim("disk."+name, "Disk", name)
	d := &Disk{Name: name, sim: s, proc: p, files: map[string]*diskFile{},
		fault: DeclareFaults("disk."+name+".crash", "powerLoss", "tornWrite").at(p),
		write: DeclareFaults("disk."+name+".write", "noSpace").at(p),
		sync:  DeclareFaults("disk."+name+".sync", "error").at(p),
		read:  DeclareFaults("disk."+name+".read", "corrupt").at(p)}
	s.OnCrash(func(c *Process) {
		if c == d.proc {
			d.crash()
		}
	})
	return d
}

func (d *Disk) crash() {
	fault := d.sim.FaultOf(d.fault)
	names := make([]string, 0, len(d.files))
	for n := range d.files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f := d.files[n]
		switch fault {
		case "powerLoss":
			f.data = f.durable()
		case "tornWrite":
			if k := len(f.starts); k > 0 && len(f.lost) == 0 {
				last := f.starts[k-1]
				f.data = f.data[:last+(len(f.data)-last)/2]
			} else {
				f.data = f.durable() // with dropped writes, the power loss is the loss that matters
			}
		}
		f.synced, f.starts, f.lost = len(f.data), nil, nil
	}
}

// ReadFile is the file's content as the machine sees it, and whether it
// exists. A corrupt read ("disk.<name>.read") by one of the simulation's
// tasks flips its middle byte.
func (d *Disk) ReadFile(name string) ([]byte, bool) {
	f, ok := d.files[name]
	if !ok {
		return nil, false
	}
	out := append([]byte(nil), f.data...)
	// only a task's read is faulted: a test inspecting the disk after
	// the run reads it as it is
	if len(out) > 0 && taskOf(nil) != nil && d.sim.FaultOf(d.read) == "corrupt" {
		out[len(out)/2] ^= 0xff
	}
	return out, true
}

// Durable is the part of the file a power loss would keep: what was
// synced.
func (d *Disk) Durable(name string) []byte {
	if f, ok := d.files[name]; ok {
		return f.durable()
	}
	return nil
}

// Create opens a file for writing, truncating it (the truncation is
// durable at once: metadata is not modelled).
func (d *Disk) Create(name string) *File {
	d.files[name] = &diskFile{}
	return &File{d: d, name: name}
}

// Append opens a file for writing at its end, creating it.
func (d *Disk) Append(name string) *File {
	if d.files[name] == nil {
		d.files[name] = &diskFile{}
	}
	return &File{d: d, name: name}
}

// File is an open file of a Disk: an io.Writer with Sync, for the code
// under test to write through (qobs.NewJSONLSink, a journal).
type File struct {
	d    *Disk
	name string
}

// Write appends b. It is in the OS when Write returns, not on the disk. On
// a full disk it fails with ErrNoSpace and writes nothing.
func (f *File) Write(b []byte) (int, error) {
	Yield(context.Background()) // the calling task's: a crash may come before the write
	if f.d.sim.FaultOf(f.d.write) == "noSpace" {
		return 0, ErrNoSpace
	}
	df := f.d.files[f.name]
	df.starts = append(df.starts, len(df.data))
	df.data = append(df.data, b...)
	return len(b), nil
}

// Sync makes every write so far durable, as fsync does. On ErrIO it does
// not, and the writes it was to make durable never will be: a power loss
// drops them, though they stay readable and a later Sync returns nil.
// Writes after them, synced, survive: a program that writes its data
// again after the error keeps it.
func (f *File) Sync() error {
	Yield(context.Background())
	df := f.d.files[f.name]
	if f.d.sim.FaultOf(f.d.sync) == "error" {
		if df.synced < len(df.data) {
			df.lost = append(df.lost, [2]int{df.synced, len(df.data)})
		}
		df.synced, df.starts = len(df.data), nil // marked clean: no later fsync writes them
		return ErrIO
	}
	df.synced, df.starts = len(df.data), nil
	return nil
}

// Close closes the file. It does not sync, as close(2) does not.
func (f *File) Close() error { return nil }
