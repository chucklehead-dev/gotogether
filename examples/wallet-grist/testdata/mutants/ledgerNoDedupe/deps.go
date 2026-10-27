package walletgrist

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A Ledger posts moves, deduplicating by key: a post under a key it
// already holds is answered ok and posts nothing.
type Ledger interface {
	Post(ctx context.Context, tenant, key, account string, amount int) error
}

// LedgerDep is the application's ledger, reached through a processor's
// context (grist.Dep): one per run, attached by gristsim's Config.Setup,
// Main's Dependencies or the real tier's harness; a ledger that refuses
// every post where none is attached.
var LedgerDep = grist.NewDep[Ledger]("ledger", noLedger{})

type noLedger struct{}

func (noLedger) Post(context.Context, string, string, string, int) error {
	return errors.New("wallet: no ledger: attach one (LedgerDep.With)")
}

// An Entry is a post as the ledger takes it.
type Entry struct {
	Tenant, Key, Account string
	Amount               int
}

// A LedgerEntry is a post as the ledger makes it: its record at the
// ledger's site (LedgerPosts), the tenant its actor.
type LedgerEntry struct {
	Key     string `json:"key"`
	Account string `json:"account"`
	Amount  int    `json:"amount"`
}

// LedgerPosts is the ledger's own record of each post it makes, where it
// makes it (the step ledgerPost, actor the tenant): an effect's site
// records the effect, so a post made twice is in the trace whatever the
// processor saw.
var LedgerPosts = grist.NewSite[LedgerEntry](App, "ledger-post")

// Book is the ledger's state: the keys it posted, in order. It records
// each post at its site (LedgerPosts).
type Book struct {
	mu   sync.Mutex
	keys map[string]bool
	// Entries are the posts made, in order.
	Entries []Entry
}

// post makes e's post once per key, recording it.
func (b *Book) post(ctx context.Context, e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.keys == nil {
		b.keys = map[string]bool{}
	}
	// the mutant ledgerNoDedupe: a retry under a key posts again
	b.keys[e.Key] = true
	b.Entries = append(b.Entries, e)
	LedgerPosts.Record(ctx, e.Tenant, LedgerEntry{Key: e.Key, Account: e.Account, Amount: e.Amount})
}

// SimLedger is the ledger in a simulation: a qenv.Service of its own
// process ("ledger"), whose requests and responses may be lost or late.
type SimLedger struct {
	svc  *qenv.Service[Entry, struct{}]
	book Book
}

// NewSimLedger registers the ledger service in s.
func NewSimLedger(s *qenv.Sim) *SimLedger {
	l := &SimLedger{}
	l.svc = qenv.NewService(s, "ledger", nil, func(ctx context.Context, e Entry) (struct{}, error) {
		l.book.post(ctx, e)
		return struct{}{}, nil
	})
	return l
}

// Post implements Ledger: an error if no answer came within a second.
func (l *SimLedger) Post(ctx context.Context, tenant, key, account string, amount int) error {
	_, err := l.svc.Call(ctx, Entry{Tenant: tenant, Key: key, Account: account, Amount: amount}, time.Second)
	return err
}

// LocalLedger is the ledger in process (the real tier's and the binary's
// stand-in), recording its posts to the recorder of the context it is
// given.
type LocalLedger struct{ book Book }

// Post implements Ledger.
func (l *LocalLedger) Post(ctx context.Context, tenant, key, account string, amount int) error {
	l.book.post(ctx, Entry{Tenant: tenant, Key: key, Account: account, Amount: amount})
	return nil
}
