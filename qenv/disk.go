package qenv

import (
	"context"
	"sort"
)

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
// Files are byte slices, read whole; writes and syncs are seams.
type Disk struct {
	Name  string
	sim   *Sim
	proc  *Process
	files map[string]*diskFile
	fault FaultKind // "disk.<name>.crash"
}

type diskFile struct {
	data   []byte // what the machine reads: synced and not
	synced int    // the prefix of data on the disk
	starts []int  // where each write since the last sync began
}

// NewDisk returns an empty disk of process p's machine.
func NewDisk(s *Sim, name string, p *Process) *Disk {
	s.claim("disk."+name, "Disk", name)
	d := &Disk{Name: name, sim: s, proc: p, files: map[string]*diskFile{},
		fault: DeclareFaults("disk."+name+".crash", "powerLoss", "tornWrite")}
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
			f.data = f.data[:f.synced]
		case "tornWrite":
			if k := len(f.starts); k > 0 {
				last := f.starts[k-1]
				f.data = f.data[:last+(len(f.data)-last)/2]
			}
		}
		f.synced, f.starts = len(f.data), nil
	}
}

// ReadFile is the file's content as the machine sees it, and whether it
// exists.
func (d *Disk) ReadFile(name string) ([]byte, bool) {
	f, ok := d.files[name]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), f.data...), true
}

// Durable is the part of the file a power loss would keep: what was
// synced.
func (d *Disk) Durable(name string) []byte {
	if f, ok := d.files[name]; ok {
		return append([]byte(nil), f.data[:f.synced]...)
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

// Write appends b. It is in the OS when Write returns, not on the disk.
func (f *File) Write(b []byte) (int, error) {
	Yield(context.Background()) // the calling task's: a crash may come before the write
	df := f.d.files[f.name]
	df.starts = append(df.starts, len(df.data))
	df.data = append(df.data, b...)
	return len(b), nil
}

// Sync makes every write so far durable, as fsync does.
func (f *File) Sync() error {
	Yield(context.Background())
	df := f.d.files[f.name]
	df.synced, df.starts = len(df.data), nil
	return nil
}

// Close closes the file. It does not sync, as close(2) does not.
func (f *File) Close() error { return nil }
