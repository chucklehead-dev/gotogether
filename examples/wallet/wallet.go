// Package wallet is a wallet service on qenv: accounts in a transactional
// store, transfers and deposits applied at most once by their request id,
// an outbox that a relay drains to a queue, and a ledger that posts every
// applied request. Clients retry a request until it is answered, with the
// same id, over a network that loses, delays and duplicates; the bank, the
// ledger and the clients crash.
//
// Each account is an append-only list of entries (a request's id and its
// amount), so the store's history is a list-append workload's, which
// qhistory reads (Elle's): the history a run records (Deployment.History)
// is checked against an isolation level as a real database's would be.
//
// Clients record each call when invoked and when answered (qobs.Invoke,
// Call.Done); the ledger records each post where it commits; after the
// system settles, a probe records every account's balance. The model,
// model/wallet.qnt, judges all of it.
//
// The mutants each break one guarantee, each caught by one check:
//
//   - NoDedupe applies a retried request again: a lost response's retry
//     moves the money twice (Exhaust finds it within one fault).
//   - UnlockedRecord reads the request's record without its lock: a
//     duplicated request's two deliveries both find no record, and at read
//     committed both apply it (the first faithful version did this).
//   - NoValidation applies a request it must refuse: a transfer to its own
//     source makes money, one to an account it does not hold destroys it,
//     a negative amount reverses it.
//   - ReadThenWrite reads the source account without locking it: at read
//     committed two transfers from it both spend what one saw, a lost
//     update; at snapshot isolation and above the first committer wins.
//   - WriteSkew reads a credit line's other account without locking it:
//     at snapshot isolation two transfers, one from each, both pass and
//     the line goes negative (write skew; qhistory names it G2).
//   - NoOutbox publishes to the ledger after the commit instead of through
//     the outbox: a crash between them loses the post, which only the
//     eventual check after a settle sees (Exhaust finds it within one
//     fault).
//   - ReplicaRead answers a balance from the ledger's copy, behind the
//     store: a read after a deposit misses it (linearized per account).
package wallet

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Mutation injects a bug.
type Mutation int

const (
	Faithful Mutation = iota
	// NoDedupe applies a retried request again.
	NoDedupe
	// ReadThenWrite reads the source account without a lock.
	ReadThenWrite
	// WriteSkew reads a credit line's other account without a lock.
	WriteSkew
	// NoOutbox publishes to the ledger after the commit.
	NoOutbox
	// ReplicaRead answers a balance from the ledger's copy.
	ReplicaRead
	// UnlockedRecord reads the request's record without its lock.
	UnlockedRecord
	// NoValidation applies a request it must refuse.
	NoValidation
)

func (m Mutation) String() string {
	return [...]string{"Faithful", "NoDedupe", "ReadThenWrite", "WriteSkew", "NoOutbox", "ReplicaRead", "UnlockedRecord", "NoValidation"}[m]
}

// Accounts are the accounts, the model's ACCOUNTS; Joint the two that share
// a credit line, its JOINT.
var (
	Accounts = []string{"a1", "a2", "b", "c"}
	Joint    = []string{"a1", "a2"}
)

// Initial is every account's opening balance, the model's INITIAL.
const Initial = 100

// Timeout is how long a client waits for an answer before it retries.
const Timeout = 50 * time.Millisecond

// An Entry is one request's move on an account. Its tag is the append's
// own, unique across the run: a retried transaction appends the request
// again, and a history (qhistory) tells appends apart by their values. The
// tags come from a counter in the bank's memory, which a simulated crash
// keeps (a real bank would draw them from the database, as a sequence).
type Entry struct {
	ID, Amount, Tag int
}

// balance is an account's balance from its entries.
func balance(es []Entry) int {
	b := Initial
	for _, e := range es {
		b += e.Amount
	}
	return b
}

// Req is a client's request.
type Req struct {
	Kind      string // "transfer", "deposit" or "balance"
	ID        int    // a transfer's or deposit's, the same in every retry
	Src, Dest string // a transfer's
	Account   string // a deposit's or a balance's
	Amount    int
}

// Resp is the bank's answer: whether a request was applied (a transfer
// the funds did not cover is not), or a balance.
type Resp struct {
	OK      bool
	Balance int
}

// A Posting is what the ledger is told of an applied request: its moves.
type Posting struct {
	ID    int
	Moves []Move
}

// A Move is an amount on an account.
type Move struct {
	Account string
	Amount  int
}

// Bank is the wallet's service: its store, and the ledger's queue.
type Bank struct {
	Mutation Mutation
	DB       qenv.DB
	Ledger   *qenv.Queue
	Replica  map[string]int // the ledger's balances, for ReplicaRead
	Store    *qenv.Store    // DB's store, whose outbox the relay waits on
	tags     int            // the last entry's tag
}

// accounts are the accounts' lists of entries, by account.
var accounts = qenv.NewTable[[]Entry]("acct/")

// read reads account in tx, locking it unless lock is false.
func read(tx qenv.Txn, account string, lock bool) ([]Entry, error) {
	if !lock {
		es, _ := accounts.Get(tx, account)
		return es, nil
	}
	es, _, err := accounts.GetForUpdate(tx, account)
	return es, err
}

// apply moves amounts in tx: each account's entry appended to its list,
// as read already (have) or read now, locked.
func (b *Bank) apply(tx qenv.Txn, id int, moves map[string]int, have map[string][]Entry) error {
	// the source is locked first (it was read), then the others in order:
	// two transfers may lock two accounts in opposite orders, and the
	// store aborts the one that would close the wait cycle (ErrDeadlock, a
	// conflict), which Retry runs again
	for _, a := range accountsOf(moves) {
		amt := moves[a]
		es, read1 := have[a]
		if !read1 {
			var err error
			if es, err = read(tx, a, true); err != nil {
				return err
			}
		}
		b.tags++
		accounts.Put(tx, a, append(slices.Clip(es), Entry{id, amt, b.tags}))
	}
	return nil
}

// accountsOf are the accounts moves touches: the bank's in order, then
// any other (NoValidation's), sorted.
func accountsOf(moves map[string]int) []string {
	var out, other []string
	for _, a := range Accounts {
		if _, ok := moves[a]; ok {
			out = append(out, a)
		}
	}
	for a := range moves {
		if !slices.Contains(Accounts, a) {
			other = append(other, a)
		}
	}
	slices.Sort(other)
	return append(out, other...)
}

// Handle serves one request: applied at most once by its id (but by
// NoDedupe), with its posting in the outbox in the same transaction (but
// by NoOutbox).
func (b *Bank) Handle(ctx context.Context, r Req) (Resp, error) {
	if r.Kind == "balance" {
		return b.balance(ctx, r.Account)
	}
	var resp Resp
	var posting *Posting
	err := qenv.Retry(ctx, b.DB, 20, func(tx qenv.Txn) error {
		resp, posting = Resp{}, nil
		// the request's record, locked: a duplicate delivered while the
		// first is in flight waits for it, rather than read no record too
		// and apply the request again (at read committed it did)
		done := "done/" + strconv.Itoa(r.ID)
		if b.Mutation != NoDedupe {
			var v any
			var ok bool
			var err error
			if b.Mutation == UnlockedRecord {
				v, ok = tx.Get(done)
			} else {
				v, ok, err = tx.GetForUpdate(done)
			}
			if err != nil {
				return err
			}
			if ok {
				resp.OK = v.(bool) // a retry: as the first time
				return nil
			}
		}
		moves, have := map[string]int{}, map[string][]Entry{}
		switch {
		case !valid(r) && b.Mutation != NoValidation:
			// refused, and recorded as refused: a retry answers the same
		case r.Kind == "deposit":
			moves[r.Account] += r.Amount
		case r.Kind == "transfer":
			// the source read, and written as read: unlocked (ReadThenWrite),
			// a write over another's that read it too
			es, err := read(tx, r.Src, b.Mutation != ReadThenWrite)
			if err != nil {
				return err
			}
			have[r.Src] = es
			funds := balance(es)
			if slices.Contains(Joint, r.Src) {
				for _, a := range Joint {
					if a == r.Src {
						continue
					}
					other, err := read(tx, a, b.Mutation != WriteSkew)
					if err != nil {
						return err
					}
					funds += balance(other)
				}
			}
			if funds >= r.Amount {
				moves[r.Src] -= r.Amount
				moves[r.Dest] += r.Amount // to its own source (NoValidation): the two moves are one
			}
		default:
			return fmt.Errorf("wallet: no request %q", r.Kind)
		}
		if len(moves) > 0 {
			if err := b.apply(tx, r.ID, moves, have); err != nil {
				return err
			}
			posting = &Posting{ID: r.ID}
			for _, a := range accountsOf(moves) {
				posting.Moves = append(posting.Moves, Move{Account: a, Amount: moves[a]})
			}
			if b.Mutation != NoOutbox {
				tx.Put("outbox/"+key(r.ID), *posting)
			}
		}
		resp.OK = len(moves) > 0
		tx.Put(done, resp.OK)
		return nil
	})
	if err != nil {
		return Resp{}, err
	}
	if b.Mutation == NoOutbox && posting != nil {
		qenv.Yield(ctx) // committed, not yet published: a crash here loses the post
		b.Ledger.Publish(ctx, *posting)
	}
	return resp, nil
}

// valid reports whether a request is one the bank may apply: accounts it
// holds, a transfer between two of them, a positive amount.
func valid(r Req) bool {
	switch r.Kind {
	case "deposit":
		return slices.Contains(Accounts, r.Account) && r.Amount > 0
	case "transfer":
		return slices.Contains(Accounts, r.Src) && slices.Contains(Accounts, r.Dest) && r.Src != r.Dest && r.Amount > 0
	}
	return false
}

// key is an id as a key that sorts as the number does.
func key(id int) string { return fmt.Sprintf("%08d", id) }

func (b *Bank) balance(ctx context.Context, account string) (Resp, error) {
	if b.Mutation == ReplicaRead {
		return Resp{Balance: b.Replica[account]}, nil
	}
	var es []Entry
	err := qenv.Retry(ctx, b.DB, 20, func(tx qenv.Txn) error {
		var err error
		es, err = read(tx, account, false)
		return err
	})
	return Resp{Balance: balance(es)}, err
}

// Relay drains the outbox to the ledger's queue, in id order, deleting
// each entry once published: a daemon, it waits for entries forever. A
// crash between the publish and the delete publishes it again, which the
// ledger's dedupe absorbs.
func (b *Bank) Relay(ctx context.Context) {
	qenv.Daemon(ctx)
	for {
		var next []qenv.KV
		_ = qenv.Retry(ctx, b.DB, 20, func(tx qenv.Txn) error {
			next = tx.Scan("outbox/")
			return nil
		})
		if len(next) == 0 {
			qenv.WaitIdle(ctx, func() bool { return len(b.Store.Keys("outbox/")) > 0 })
			continue
		}
		for _, kv := range next {
			b.Ledger.Publish(ctx, kv.Val.(Posting))
			_ = qenv.Retry(ctx, b.DB, 20, func(tx qenv.Txn) error {
				tx.Delete(kv.Key)
				return nil
			})
		}
	}
}

// Ledger posts each request once: it records the post where it commits,
// and keeps the balances ReplicaRead answers from.
type Ledger struct {
	DB      qenv.DB
	Queue   *qenv.Queue
	Replica map[string]int
}

// Consume posts what the queue delivers, a daemon.
func (l *Ledger) Consume(ctx context.Context) {
	qenv.Daemon(ctx)
	for {
		d := l.Queue.Receive(ctx)
		p := d.Body.(Posting)
		err := qenv.Retry(ctx, l.DB, 20, func(tx qenv.Txn) error {
			k := "posted/" + key(p.ID)
			if _, ok := tx.Get(k); ok {
				return nil
			}
			tx.Put(k, true)
			tx.OnCommit(func() {
				for _, m := range p.Moves {
					l.Replica[m.Account] += m.Amount
				}
				// the post as one call at its commit, its first move's: a
				// history partitioned by account keeps it
				m := p.Moves[0]
				qobs.Invoke(ctx, "post", "id", p.ID, "account", m.Account, "amount", m.Amount).Done(nil)
			})
			return nil
		})
		if err != nil {
			d.Nack(ctx) // not posted: delivered again
			continue
		}
		d.Ack(ctx)
	}
}

// An Op is one call of a client's.
type Op struct {
	Kind      string // "transfer", "deposit" or "read"
	Src, Dest string
	Account   string
	Amount    int
}

// Client calls the bank, retrying each request with its id until it is
// answered (a down bank, a lost request or response).
type Client struct {
	Bank *qenv.Service[Req, Resp]
	ID   int // its requests' ids are ID*100 + their number
}

func (c *Client) call(ctx context.Context, r Req) Resp {
	for {
		resp, err := c.Bank.Call(ctx, r, Timeout)
		if err == nil {
			return resp
		}
		qenv.Sleep(ctx, Timeout)
	}
}

// Do runs op, its n-th, recording it when invoked and when answered.
func (c *Client) Do(ctx context.Context, n int, op Op) {
	id := c.ID*100 + n
	switch op.Kind {
	case "transfer":
		call := qobs.Invoke(ctx, "transfer", "id", id, "src", op.Src, "dest", op.Dest, "amount", op.Amount)
		resp := c.call(ctx, Req{Kind: "transfer", ID: id, Src: op.Src, Dest: op.Dest, Amount: op.Amount})
		call.Done(nil, "obs.ok", resp.OK)
	case "deposit":
		call := qobs.Invoke(ctx, "deposit", "id", id, "account", op.Account, "amount", op.Amount)
		resp := c.call(ctx, Req{Kind: "deposit", ID: id, Account: op.Account, Amount: op.Amount})
		call.Done(nil, "obs.ok", resp.OK)
	case "read":
		call := qobs.Invoke(ctx, "read", "account", op.Account)
		resp := c.call(ctx, Req{Kind: "balance", Account: op.Account})
		call.Done(nil, "obs.balance", resp.Balance)
	}
}

// Config is a run's shape.
type Config struct {
	Mutation  Mutation
	Isolation qenv.Isolation // the bank's store's (SnapshotIsolation by default)
	// Workload is each client's calls, in order (Transfers by default)
	Workload [][]Op
	// ClientCrashes makes the clients crashable: a crashed client's call
	// stays pending, its effect unknown to it (ResolvePending)
	ClientCrashes bool
}

// Transfers is three clients moving money between accounts, depositing
// and reading balances, never more than an account holds.
var Transfers = [][]Op{
	{{Kind: "transfer", Src: "a1", Dest: "b", Amount: 10}, {Kind: "read", Account: "b"}, {Kind: "transfer", Src: "c", Dest: "a2", Amount: 20}},
	{{Kind: "transfer", Src: "b", Dest: "c", Amount: 30}, {Kind: "deposit", Account: "a1", Amount: 8}, {Kind: "transfer", Src: "a2", Dest: "a1", Amount: 5}},
	{{Kind: "read", Account: "c"}, {Kind: "transfer", Src: "b", Dest: "a1", Amount: 15}, {Kind: "read", Account: "b"}},
}

// Invalid is requests the bank must refuse, between two it applies: a
// transfer to its own source, to an account it does not hold, of a
// negative amount, and a deposit of nothing.
var Invalid = [][]Op{
	{{Kind: "transfer", Src: "b", Dest: "c", Amount: 10}, {Kind: "transfer", Src: "c", Dest: "c", Amount: 30},
		{Kind: "transfer", Src: "b", Dest: "zz", Amount: 30}, {Kind: "transfer", Src: "c", Dest: "b", Amount: -400},
		{Kind: "deposit", Account: "a1", Amount: 0}, {Kind: "transfer", Src: "a1", Dest: "c", Amount: 5}},
}

// Blind is Transfers without reads: what a run whose clients crash
// checks, by the probe's balances after the settle.
var Blind = [][]Op{
	{{Kind: "transfer", Src: "a1", Dest: "b", Amount: 10}, {Kind: "transfer", Src: "c", Dest: "a2", Amount: 20}},
	{{Kind: "transfer", Src: "b", Dest: "c", Amount: 30}, {Kind: "transfer", Src: "a2", Dest: "a1", Amount: 5}},
	{{Kind: "transfer", Src: "b", Dest: "a1", Amount: 15}, {Kind: "transfer", Src: "a1", Dest: "c", Amount: 25}},
}

// Deposits is deposits and reads, each call on one account: a history
// linearized account by account (qtest.LinearizeByKey("account")).
var Deposits = [][]Op{
	{{Kind: "deposit", Account: "a1", Amount: 10}, {Kind: "read", Account: "a1"}, {Kind: "deposit", Account: "b", Amount: 5}, {Kind: "read", Account: "b"}},
	{{Kind: "read", Account: "a1"}, {Kind: "deposit", Account: "a1", Amount: 7}, {Kind: "read", Account: "b"}},
}

// Skew is two transfers, one from each account of the credit line, that
// the line covers one at a time and not together.
var Skew = [][]Op{
	{{Kind: "transfer", Src: "a1", Dest: "b", Amount: 150}},
	{{Kind: "transfer", Src: "a2", Dest: "c", Amount: 150}},
}

// Contended is two transfers from one account, each read before it is
// written.
var Contended = [][]Op{
	{{Kind: "transfer", Src: "b", Dest: "c", Amount: 30}},
	{{Kind: "transfer", Src: "b", Dest: "a1", Amount: 40}},
}

// Deployment is a simulated wallet: the bank, the ledger, their stores
// and the history of the bank's store.
type Deployment struct {
	Sim           *qenv.Sim
	Bank          *Bank
	Ledger        *Ledger
	BankProc      *qenv.Process
	Store, Posted *qenv.Store
	History       *History
	Service       *qenv.Service[Req, Resp]
}

// Deploy builds the wallet on s.
func Deploy(s *qenv.Sim, cfg Config) *Deployment {
	iso := cfg.Isolation
	if iso == 0 {
		iso = qenv.SnapshotIsolation
	}
	d := &Deployment{Sim: s, History: &History{}}
	replica := map[string]int{}
	for _, a := range Accounts {
		replica[a] = Initial
	}
	d.Store = qenv.NewStore(s, "bank", iso)
	// a commit may be applied and reported lost (ErrUnknownOutcome): the
	// bank answers its client with the error, the client retries, and the
	// request's record answers the retry
	d.Store.CommitFaults = true
	d.Posted = qenv.NewStore(s, "ledger", qenv.Serializable)
	d.Bank = &Bank{Mutation: cfg.Mutation, DB: &recording{d.Store, d.History}, Replica: replica, Store: d.Store}
	d.Ledger = &Ledger{DB: d.Posted, Replica: replica}
	ledgerProc := s.Process("ledger", true, func(ctx context.Context) { d.Ledger.Consume(ctx) })
	d.Ledger.Queue = qenv.NewQueue(s, "postings", ledgerProc)
	d.Bank.Ledger = d.Ledger.Queue
	d.BankProc = s.Process("bank", true, func(ctx context.Context) { d.Bank.Relay(ctx) })
	d.Service = qenv.NewService(s, "wallet", d.BankProc, d.Bank.Handle)
	return d
}

// Run runs the clients on c, then settles the wallet and probes every
// balance.
func Run(c qenv.Chooser, cfg Config) error {
	_, err := RunDeployment(c, cfg)
	return err
}

// RunDeployment is Run, returning the deployment (its History) too.
func RunDeployment(c qenv.Chooser, cfg Config) (*Deployment, error) {
	if cfg.Workload == nil {
		cfg.Workload = Transfers
	}
	s := qenv.New(c)
	defer s.Close()
	d := Deploy(s, cfg)
	for i, ops := range cfg.Workload {
		cl := &Client{Bank: d.Service, ID: i + 1}
		s.Process(fmt.Sprintf("client%d", i+1), cfg.ClientCrashes, func(ctx context.Context) {
			if p := qenv.ProcessOf(ctx); p.Incarnation > 1 {
				return // a client that crashed is gone: its call stays pending
			}
			for n, op := range ops {
				cl.Do(ctx, n+1, op)
			}
		})
	}
	if err := s.Run(5 * time.Second); err != nil {
		return d, err
	}
	if err := s.Settle(time.Minute); err != nil {
		return d, err // an *Unsettled is the run's outcome, judged
	}
	d.Probe(s.Context())
	return d, nil
}

// Probe records every account's balance, read in one transaction (so the
// history ends on the accounts' final lists), then that the system is
// still settled.
func (d *Deployment) Probe(ctx context.Context) {
	var lists [][]Entry
	_ = qenv.Retry(ctx, d.Bank.DB, 20, func(tx qenv.Txn) error {
		lists = nil
		for _, a := range Accounts {
			es, _ := read(tx, a, false)
			lists = append(lists, es)
		}
		return nil
	})
	for i, a := range Accounts {
		qobs.Invoke(ctx, "observe", "account", a, "balance", balance(lists[i])).Done(nil)
	}
	qobs.Settled(ctx)
}

// tags are an account list's entries' tags, as a history reads them.
func tags(es []Entry) []int {
	out := make([]int, len(es))
	for i, e := range es {
		out[i] = e.Tag
	}
	return out
}

// isAccount reports whether a store key is an account's, and which.
func isAccount(k string) (string, bool) { return strings.CutPrefix(k, accounts.Prefix()) }
