// Package quorumkv is a replicated key-value store on qenv: three
// replicas, each a crashable process with a durable store, and a client
// library that coordinates every operation over quorums, as in ABD
// (Attiya, Bar-Noy and Dolev) for many writers. A put reads a quorum of R
// replicas for the newest version, then writes the next one to every
// replica and waits for W acknowledgements; a replica it could not reach
// gets a hint, which a handoff daemon delivers until it lands. A get reads
// R replicas and writes the newest value it saw back to W before it
// answers: that write-back makes the register linearizable. With W+R > 3,
// two quorums meet.
//
// Clients record each operation when it is invoked (qobs.Invoke) and when
// it answers (Call.Done), with the interval in the simulation's time, so
// the history is checked linearized per key (qtest.LinearizeByKey). After
// the system settles, a probe records what every replica holds (observe),
// which the model's eventual val converged compares with the register.
//
// The mutants each break one guarantee, each caught by one check:
//
//   - W3 waits for all three replicas: with one replica down for good, no
//     put finishes (SettleCore around the other two: Settles fails).
//   - Leak gives up a put after one failed round without answering its
//     caller (Done): the call stays pending (Completes fails).
//   - NoHandoff drops the hints: a replica that missed a write keeps the
//     old value forever, which quorum reads mask (converged fails).
//   - BlindRepair answers a get without its write-back: a read can see a
//     write that a later read does not (Mismatch of read, linearized).
//   - Repick picks a new version when it retries a put: the value lands
//     twice, around a later write (Mismatch of read, linearized). This
//     example's first faithful version did it, and its first explorations
//     found it.
package quorumkv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Mutation injects a bug.
type Mutation int

const (
	Faithful Mutation = iota
	// W3 waits for every replica to acknowledge a put.
	W3
	// Leak gives up a put after one failed round, without Done.
	Leak
	// NoHandoff drops the hints of replicas a put could not reach.
	NoHandoff
	// BlindRepair answers a get without writing the value back.
	BlindRepair
	// Repick picks a new version when it retries a put: the value lands
	// twice, under two versions, around another write.
	Repick
)

// Mutations names each mutation, for the simulator's -mutation flag.
var Mutations = map[string]Mutation{"": Faithful, "w3": W3, "leak": Leak, "no-handoff": NoHandoff, "blind-repair": BlindRepair, "repick": Repick}

// Replicas are the replicas' names, the model's REPLICAS.
var Replicas = []string{"r1", "r2", "r3"}

// Keys are the keys the clients use, the model's KEYS.
var Keys = []string{"a", "b"}

// RoundTrip is a request's network time, in virtual time.
const RoundTrip = time.Millisecond

// Entry is a key's value at a replica: a version (a round and the writer,
// so two writers never pick the same one) and the value.
type Entry struct {
	Ver int
	Val int
}

// Req is a replica's request: read a key, or write an entry if it is newer
// than the replica's.
type Req struct {
	Write bool
	Key   string
	Entry Entry
}

// Replica serves its store: a write keeps the newer entry, a read returns
// the replica's.
func Replica(db *qenv.Store) func(ctx context.Context, r Req) (Entry, error) {
	return func(ctx context.Context, r Req) (Entry, error) {
		var out Entry
		err := qenv.Retry(ctx, db, 5, func(tx qenv.Txn) error {
			v, _, err := tx.GetForUpdate(r.Key)
			if err != nil {
				return err
			}
			cur, _ := v.(Entry)
			if r.Write && r.Entry.Ver > cur.Ver {
				tx.Put(r.Key, r.Entry)
				cur = r.Entry
			}
			out = cur
			return nil
		})
		return out, err
	}
}

// Cluster is the replicas as a client sees them, and the hints of writes
// that did not reach one.
type Cluster struct {
	Mutation Mutation
	Services []*qenv.Service[Req, Entry]
	R, W     int
	Timeout  time.Duration
	hints    []hint
	notify   qenv.Signal
}

type hint struct {
	replica int
	key     string
	entry   Entry
}

// quorum sends req to the replicas in turn, from the one after start,
// until want have answered (all of them, if every is set), and returns
// their answers; the replicas that did not answer are in missed.
func (c *Cluster) quorum(ctx context.Context, req Req, want, start int, every bool) (got []Entry, missed []int) {
	for i := range c.Services {
		r := (start + i) % len(c.Services)
		if !every && len(got) >= want {
			break
		}
		// a round trip takes time: without it, calls one after another
		// would share an instant, and real time would order none of them
		qenv.Sleep(ctx, RoundTrip)
		e, err := c.Services[r].Call(ctx, req, c.Timeout)
		if err != nil {
			missed = append(missed, r)
			continue
		}
		got = append(got, e)
	}
	return got, missed
}

func newest(es []Entry) Entry {
	var best Entry
	for _, e := range es {
		if e.Ver > best.Ver {
			best = e
		}
	}
	return best
}

// errGaveUp is the Leak mutant's: a put abandoned after one round.
var errGaveUp = errors.New("quorumkv: gave up")

// Put writes val to key for writer id (1..15), retrying rounds until a
// write quorum acknowledged it.
func (c *Cluster) Put(ctx context.Context, id int, key string, val int) error {
	call := qobs.Invoke(ctx, "write", "key", key, "value", val)
	var e Entry
	for chosen := false; ; {
		if !chosen || c.Mutation == Repick {
			// the version, once: a retry that picked another would write the
			// value twice, around a later write, which no order explains
			if got, _ := c.quorum(ctx, Req{Key: key}, c.R, id, false); len(got) >= c.R {
				e, chosen = Entry{Ver: (newest(got).Ver/16+1)*16 + id, Val: val}, true
			}
		}
		if chosen {
			acks, missed := c.quorum(ctx, Req{Write: true, Key: key, Entry: e}, c.W, id, true)
			if c.Mutation != NoHandoff {
				for _, r := range missed {
					c.hints = append(c.hints, hint{r, key, e})
				}
				c.notify.Notify()
			}
			if len(acks) >= c.W {
				call.Done(nil)
				return nil
			}
		}
		if c.Mutation == Leak {
			return errGaveUp // and never tells its caller's history
		}
		qenv.Sleep(ctx, c.Timeout)
	}
}

// Get reads key: the newest entry of a read quorum, written back to a
// write quorum before it answers (but by BlindRepair).
func (c *Cluster) Get(ctx context.Context, id int, key string) int {
	call := qobs.Invoke(ctx, "read", "key", key)
	for {
		got, _ := c.quorum(ctx, Req{Key: key}, c.R, id, false)
		if len(got) >= c.R {
			e := newest(got)
			if c.Mutation == BlindRepair {
				call.Done(nil, "obs.val", e.Val)
				return e.Val
			}
			if acks, _ := c.quorum(ctx, Req{Write: true, Key: key, Entry: e}, c.W, id, false); len(acks) >= c.W {
				call.Done(nil, "obs.val", e.Val)
				return e.Val
			}
		}
		qenv.Sleep(ctx, c.Timeout)
	}
}

// Handoff delivers hints until each lands, a daemon: it waits for hints
// forever.
func (c *Cluster) Handoff(ctx context.Context) {
	qenv.Daemon(ctx)
	for {
		for len(c.hints) == 0 {
			c.notify.Wait(ctx, 0)
		}
		h := c.hints[0]
		c.hints = c.hints[1:]
		if _, err := c.Services[h.replica].Call(ctx, Req{Write: true, Key: h.key, Entry: h.entry}, c.Timeout); err != nil {
			c.hints = append(c.hints, h) // again later
			qenv.Sleep(ctx, c.Timeout)
		}
	}
}

// Config is a run's shape.
type Config struct {
	Mutation Mutation
	Clients  int      // 3 by default
	Ops      int      // per client, 4 by default
	Keys     []string // the keys the clients use, Keys by default
	// Writers, if set, makes the first Writers clients only write and the
	// others only read; by default each client writes, then reads, in turn
	Writers int
	// Core, if set, takes down every other replica for good before the
	// clients start (it crashes and never restarts) and settles with
	// faults going on outside the core (Sim.SettleCore): a quorum store
	// must finish its calls with the core alone. Only the core is probed.
	Core []string
}

// Deployment is a simulated cluster: its replicas' processes and stores,
// and the cluster its clients use.
type Deployment struct {
	Sim     *qenv.Sim
	Procs   []*qenv.Process
	Stores  []*qenv.Store
	Cluster *Cluster
}

// Deploy builds the replicas and the cluster on s.
func Deploy(s *qenv.Sim, m Mutation) *Deployment {
	d := &Deployment{Sim: s, Cluster: &Cluster{Mutation: m, R: 2, W: 2, Timeout: 50 * time.Millisecond}}
	if m == W3 {
		d.Cluster.W = 3
	}
	for _, name := range Replicas {
		p := s.Process(name, true, func(context.Context) {})
		db := qenv.NewStore(s, name, qenv.Serializable)
		d.Procs, d.Stores = append(d.Procs, p), append(d.Stores, db)
		d.Cluster.Services = append(d.Cluster.Services, qenv.NewService(s, name, p, Replica(db)))
	}
	return d
}

// Run runs the clients on c, then settles the cluster and probes what
// each replica holds.
func Run(c qenv.Chooser, cfg Config) error {
	if cfg.Clients == 0 {
		cfg.Clients = 3
	}
	if cfg.Ops == 0 {
		cfg.Ops = 4
	}
	if len(cfg.Keys) == 0 {
		cfg.Keys = Keys
	}
	s := qenv.New(c)
	defer s.Close()
	d := Deploy(s, cfg.Mutation)
	var core []*qenv.Process
	for i, name := range Replicas {
		if contains(cfg.Core, name) {
			core = append(core, d.Procs[i])
		} else if len(cfg.Core) > 0 {
			d.Procs[i].Crash() // down for good
		}
	}
	s.Spawn("handoff", d.Cluster.Handoff)
	for id := 1; id <= cfg.Clients; id++ {
		s.Spawn(fmt.Sprintf("client%d", id), func(ctx context.Context) {
			for op := range cfg.Ops {
				key := cfg.Keys[(id+op)%len(cfg.Keys)]
				if cfg.Writers > 0 && id <= cfg.Writers || cfg.Writers == 0 && op%2 == 0 {
					d.Cluster.Put(ctx, id, key, id*100+op) // a value of its own: a stale read shows
				} else {
					d.Cluster.Get(ctx, id, key)
				}
			}
		})
	}
	if err := s.Run(2 * time.Second); err != nil {
		return err
	}
	var err error
	if len(cfg.Core) > 0 {
		err = s.SettleCore(time.Minute, core...)
	} else {
		err = s.Settle(time.Minute)
	}
	if err != nil {
		return err // an *Unsettled is the run's outcome, judged
	}
	d.Probe(s.Context(), cfg.Core, cfg.Keys)
	return nil
}

// Probe records what each replica (each of only, if set) holds of every
// key, then that the system is still settled: the observations come after
// Settle's own record.
func (d *Deployment) Probe(ctx context.Context, only, keys []string) {
	for i, name := range Replicas {
		if len(only) > 0 && !contains(only, name) {
			continue
		}
		for _, key := range keys {
			e, _ := d.Stores[i].Read(key)
			v, _ := e.(Entry)
			// a call of its own: its invocation record orders it after
			// every call that completed before it
			qobs.Invoke(ctx, "observe", "replica", name, "key", key, "value", v.Val).Done(nil)
		}
	}
	qobs.Settled(ctx)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
