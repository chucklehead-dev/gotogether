package qenv

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv/internal/history"
)

// ErrUnavailable is a Store's refusal of a write while it fails over (a
// replica being promoted: read-only for Failover.ReadOnly), and of every
// later operation of a transaction open when it failed over or was
// restored (its connection reset). Nothing of the transaction is applied:
// the caller knows the outcome, and may retry once the store is back.
// GetForUpdate and Commit return it; a reset transaction's reads go on
// returning what they returned (a dead connection's last answers), and
// commit nothing.
var ErrUnavailable = errors.New("qenv: store unavailable (failing over, or the connection was reset by a failover or a restore); nothing applied: retry")

// Failover is a managed database's failover (docs/grist-platform.md
// §3.4): the primary dies at a commit and a replica is promoted. That
// commit is applied and its caller told the outcome is unknown, every
// open transaction's connection is reset (ErrUnavailable), and the
// promoted replica refuses writes for ReadOnly. With asynchronous
// replication the replica lacks the primary's last commits: option
// loseTail also loses the LoseTail commits acknowledged before the one in
// flight, and that one, so a client holds an answer for a commit that is
// gone.
type Failover struct {
	// LoseTail is how many acknowledged commits option loseTail loses
	// besides the one in flight (0: the option is not offered, and a
	// failover loses nothing).
	LoseTail int
	// ReadOnly is how long the promoted replica refuses writes
	// (ErrUnavailable), in virtual time (0: none).
	ReadOnly time.Duration
}

// Failovers turns on the fault kind "store.<name>.failover" at every
// commit that writes: option failover (the commit applied, its answer
// unknown, connections reset, read-only for f.ReadOnly) and option
// loseTail (the same, then the commit and the f.LoseTail before it lost),
// when f.LoseTail > 0. Off by default, as CommitFaults is: code that
// claims to survive a failover turns it on.
func (st *Store) Failovers(f Failover) {
	if f.LoseTail < 0 || f.ReadOnly < 0 {
		panic(fmt.Sprintf("qenv: store %s: Failover{LoseTail: %d, ReadOnly: %v}: give non-negative values", st.Name, f.LoseTail, f.ReadOnly))
	}
	st.failover = &f
}

// FailOver fails the store over now, outside any commit (a test's or a
// scenario's incident at a point it chooses): lose of the last commits are
// lost (0: none), every open transaction is reset, and writes are refused
// for readOnly. It returns how many commits it lost.
func (st *Store) FailOver(lose int, readOnly time.Duration) int {
	n := st.loseCommits(lose)
	st.reset()
	st.readOnlyUntil = st.sim.Now().Add(readOnly)
	st.changed.Notify()
	return n
}

// failoverAt is a commit's failover, if the run chooses one: the commit
// is applied already.
func (st *Store) failoverAt() bool {
	if st.failover == nil {
		return false
	}
	k := st.failoverKind
	if st.failover.LoseTail == 0 {
		k = k.Only("failover")
	}
	switch st.sim.FaultOf(k) {
	case "failover":
		st.FailOver(0, st.failover.ReadOnly)
		return true
	case "loseTail":
		st.FailOver(st.failover.LoseTail+1, st.failover.ReadOnly)
		return true
	}
	return false
}

// Backup takes a backup of the store now: what Restore puts back.
func (st *Store) Backup() { st.backup, st.hasBackup = st.seq, true }

// Restores registers the nemesis "restore.<name>" (nemesis.restore.<name>):
// the store put back to its latest backup (a CSI snapshot's, a backup's
// restore: every commit after it gone, every open transaction reset).
// A backup is taken at the first commit at least every after the last
// (and at the first commit; every 0: at every commit, so a restore loses
// the last one), and by Backup; the nemesis is enabled while a backup has
// commits after it, at most max times a run (default 1).
func (st *Store) Restores(every time.Duration, max int) {
	if every < 0 {
		panic(fmt.Sprintf("qenv: store %s: Restores(%v, %d): give how often a backup is taken (0: at every commit)", st.Name, every, max))
	}
	if max <= 0 {
		max = 1
	}
	st.backupEvery, st.backups = every, true
	st.sim.Nemesis("restore."+st.Name, func() bool {
		return st.restores < max && st.hasBackup && st.lastCommit() > st.backup
	}, func() { st.Restore() })
}

// Restore puts the store back to its latest backup (Backup, or the one
// Restores took) and resets every open transaction; it reports whether
// there was a backup. Commits after the backup are gone, as a restore
// from a snapshot loses them.
func (st *Store) Restore() bool {
	if !st.hasBackup {
		return false
	}
	st.restores++
	st.rewind(st.backup)
	st.reset()
	st.changed.Notify()
	return true
}

// Restored is how many times the store was restored, and Lost how many
// commits failovers and restores lost: for a test asserting its incident
// happened.
func (st *Store) Restored() int { return st.restores }

// Lost is how many commits failovers and restores lost.
func (st *Store) Lost() int { return st.lost }

// Commits is how many commits that wrote the store holds (those lost
// aside): a scenario's clock, to fail over or restore after the k-th.
func (st *Store) Commits() int { return len(st.commits) }

// lastCommit is the sequence number of the last commit that wrote (0:
// none).
func (st *Store) lastCommit() int64 {
	if len(st.commits) == 0 {
		return 0
	}
	return st.commits[len(st.commits)-1]
}

// loseCommits loses the last n commits that wrote, and returns how many
// it lost.
func (st *Store) loseCommits(n int) int {
	n = min(n, len(st.commits))
	if n == 0 {
		return 0
	}
	before := len(st.commits)
	st.rewind(st.commits[len(st.commits)-n] - 1)
	return before - len(st.commits)
}

// rewind drops every version after seq: the store as it was then. The
// sequence goes on from where it was (a version number is never reused),
// so a transaction's snapshot taken before still reads below it.
func (st *Store) rewind(seq int64) {
	for k, vs := range st.rows {
		i := len(vs)
		for i > 0 && vs[i-1].seq > seq {
			i--
		}
		if i == 0 {
			delete(st.rows, k)
		} else {
			st.rows[k] = vs[:i]
		}
	}
	i := len(st.commits)
	for i > 0 && st.commits[i-1] > seq {
		i--
	}
	st.lost += len(st.commits) - i
	st.commits = st.commits[:i]
	if st.hasBackup && st.backup > seq {
		st.hasBackup = false // a backup of a history that is gone
	}
	if st.hist != nil {
		// a new history from the state left: its versions were committed
		// in a past the checker no longer has (a version the old history
		// saw overwritten is current again, which no level allows of one
		// history)
		st.hist = history.New(st.hist.Level())
		keys := make([]string, 0, len(st.rows))
		for k := range st.rows {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			vs := st.rows[k]
			st.hist.Load(k, history.Version(vs[len(vs)-1].seq))
		}
	}
}

// reset resets every open transaction's connection: its locks released,
// its next GetForUpdate or Commit ErrUnavailable.
func (st *Store) reset() {
	txs := make([]*tx, 0, len(st.open))
	for tx := range st.open {
		txs = append(txs, tx)
	}
	slices.SortFunc(txs, func(a, b *tx) int { return cmp.Compare(a.id, b.id) })
	for _, tx := range txs {
		tx.reset = true
		for _, k := range tx.locked {
			if st.locks[k] == tx {
				delete(st.locks, k)
			}
		}
		tx.locked = nil
	}
}

// unavailable is why tx may not lock or commit now: its connection was
// reset, or the store is read-only (writes only).
func (tx *tx) unavailable(writes bool) error {
	switch {
	case tx.reset:
		tx.end()
		return ErrUnavailable
	case writes && tx.st.sim.Now().Before(tx.st.readOnlyUntil):
		tx.end()
		return fmt.Errorf("%w (store %s is read-only until %v of virtual time)", ErrUnavailable, tx.st.Name, tx.st.readOnlyUntil.Sub(tx.st.sim.start))
	}
	return nil
}
