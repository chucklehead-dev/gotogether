package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/payd/payd"
	"github.com/chucklehead-dev/gotogether/examples/payd/payd/client"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
	"github.com/chucklehead-dev/gotogether/qenv/qsql"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// An Op is one charge a client asks for.
type Op struct {
	Key     string
	Account string
	Amount  int64
}

// Config is a simulated run's shape.
type Config struct {
	// Isolation is the database's level (ReadCommitted by default:
	// PostgreSQL's).
	Isolation qenv.Isolation
	// Workload is each client's charges, in order (Shared by default).
	Workload [][]Op
	// EditCharge has an operator edit the first charge once the clients
	// are done (UPDATE charges SET amount = 0): what the change feed must
	// show the model.
	EditCharge bool
	// Check runs the scheduler's and the Store's own checks at every step
	// and commit (as GOTOGETHER_CHECK=1 does).
	Check bool
}

// Shared is three clients, two of which send keys the other sends too (a
// checkout page submitted twice, from two tabs): k1 and k3 are each asked
// for twice, at once.
var Shared = [][]Op{
	{{"k1", "acct_1", 250}, {"k2", "acct_2", 100}},
	{{"k1", "acct_1", 250}, {"k3", "acct_2", 40}},
	{{"k3", "acct_2", 40}},
}

// One is a single client's single charge: what a search of every run
// within a fault explores.
var One = [][]Op{{{"k1", "acct_1", 250}}}

// Accounts are the accounts the database holds, with their balances.
var Accounts = map[string]int64{"acct_1": 1000, "acct_2": 500}

// Timeouts: the clients wait longer for payd than payd waits for the
// processor, so payd answers a lost capture (502) before its client gives
// up on it.
const (
	ClientTimeout    = 2 * time.Second
	ProcessorTimeout = time.Second
)

// Deployment is a simulated payd: its process, its server, its database's
// Store, and the processor double's.
type Deployment struct {
	Sim       *qenv.Sim
	Payd      *qenv.Process
	Server    *qhttp.Server
	Store     *qenv.Store
	Processor *qhttp.Server
	ProcStore *qenv.Store
}

// Deploy builds payd and its processor on s.
func Deploy(s *qenv.Sim, cfg Config) *Deployment {
	iso := cfg.Isolation
	if iso == 0 {
		iso = qenv.ReadCommitted
	}
	d := &Deployment{Sim: s}
	d.Store = qenv.NewStore(s, "paydb", iso)

	// the processor: a double that dedupes by Idempotency-Key in its own
	// store and records each capture it commits
	d.ProcStore = qenv.NewStore(s, "procdb", qenv.Serializable)
	d.Processor = qhttp.NewServer(s, "processor", nil)
	d.Processor.Handle(context.Background(), &Processor{Store: d.ProcStore})

	// the schema and the accounts, as an operator set them up before the
	// service first started
	setup := qsql.Open(s, d.Store)
	if err := payd.Migrate(context.Background(), setup); err != nil {
		panic(err)
	}
	for _, a := range sortedKeys(Accounts) {
		if _, err := setup.Exec(`INSERT INTO accounts (id, balance, n_charges) VALUES (?, ?, 0)`, a, Accounts[a]); err != nil {
			panic(err)
		}
	}
	d.Store.CommitFaults = true // from here: 08006, a commit applied and the connection lost

	// payd's main, at every incarnation: open the database, migrate,
	// serve. It is the service's own code from there on.
	d.Payd = s.Process("payd", true, func(ctx context.Context) {
		db := qsql.Open(s, d.Store, qsql.Options{BadConn: true, OnCommit: recordCommit})
		for payd.Migrate(ctx, db) != nil {
			qenv.Sleep(ctx, 100*time.Millisecond) // as a supervisor restarts a service that failed to start
		}
		procTr := qhttp.NewTransport(d.Processor)
		procTr.Refuse = true
		d.Server.Handle(ctx, payd.New(db, &http.Client{Transport: procTr, Timeout: ProcessorTimeout}, d.Processor.URL()))
	})
	d.Server = qhttp.NewServer(s, "payd", d.Payd)
	return d
}

// recordCommit is the database's change feed (qsql's OnCommit, CDC in
// production), at the commit, the changes' linearization point: a charge
// step for each charge a commit inserted, chargeChanged for one it updated
// or deleted, then a debit for each account whose balance it lowered (by
// the key of the charge it inserted, "" if none).
func recordCommit(ctx context.Context, changes []qsql.Change) {
	key := ""
	for _, c := range changes {
		if c.Table != "charges" {
			continue
		}
		switch c.Op {
		case "insert":
			key, _ = c.New["idem_key"].(string)
			qobs.Record(ctx, "charge", nil, "key", c.New["idem_key"], "id", c.New["id"], "account", c.New["account"], "amount", c.New["amount"])
		default:
			qobs.Record(ctx, "chargeChanged", nil, "id", c.Old["id"])
		}
	}
	for _, c := range changes {
		if c.Table == "accounts" && c.Op == "update" {
			was, _ := c.Old["balance"].(int64)
			now, _ := c.New["balance"].(int64)
			if was != now {
				qobs.Record(ctx, "debit", nil, "account", c.New["id"], "key", key, "amount", was-now)
			}
		}
	}
}

// Run runs payd and its clients on c, settles, and records the accounts'
// balances.
func Run(c qenv.Chooser, cfg Config) error {
	s := qenv.New(c)
	defer s.Close()
	if cfg.Check {
		s.Check = true
	}
	if cfg.Workload == nil {
		cfg.Workload = Shared
	}
	d := Deploy(s, cfg)
	tr := qhttp.NewTransport(d.Server)
	tr.Refuse = true
	for i, ops := range cfg.Workload {
		name := fmt.Sprintf("client%d", i+1)
		sdk := client.New(d.Server.URL(), &http.Client{Transport: &Recording{Base: tr, Client: name}, Timeout: ClientTimeout})
		sdk.Sleep = qenv.Sleep
		sdk.MaxAttempts = 20 // a client gives up only after the run's faults have had every chance
		s.Spawn(name, func(ctx context.Context) {
			for _, op := range ops {
				sdk.CreateCharge(ctx, op.Key, op.Account, op.Amount) // its outcome is in the steps
			}
		})
	}
	if err := s.Run(time.Minute); err != nil {
		return err
	}
	if cfg.EditCharge {
		s.Spawn("operator", func(ctx context.Context) {
			db := qsql.Open(s, d.Store, qsql.Options{Name: "operator", OnCommit: recordCommit})
			if rows := qsql.Table(d.Store, "charges"); len(rows) > 0 {
				db.ExecContext(ctx, `UPDATE charges SET amount = 0 WHERE id = ?`, rows[0]["id"])
			}
		})
		if err := s.Run(time.Minute); err != nil {
			return err
		}
	}
	if err := s.Settle(time.Minute); err != nil {
		return err // an *Unsettled is the run's outcome, judged
	}
	// the balances, once nothing is pending, then that it is still settled
	for _, r := range qsql.Table(d.Store, "accounts") {
		qobs.Record(s.Context(), "balance", nil, "account", r["id"], "balance", r["balance"])
	}
	qobs.Settled(s.Context())
	return nil
}

// Processor is the payment processor's double: POST /v1/captures captures
// an amount, once per Idempotency-Key (a request without one is a new
// capture every time), and records the capture where it commits.
type Processor struct {
	Store *qenv.Store
}

func (p *Processor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Amount    int64  `json:"amount"`
		Reference string `json:"reference"`
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/captures" || json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "POST /v1/captures {amount, source, reference}", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	var id string
	err := qenv.Retry(ctx, p.Store, 20, func(tx qenv.Txn) error {
		id = ""
		if key != "" {
			v, ok, err := tx.GetForUpdate("idem/" + key)
			if err != nil {
				return err
			}
			if ok {
				id = v.(string)
				return nil
			}
		}
		v, _, err := tx.GetForUpdate("seq")
		if err != nil {
			return err
		}
		n, _ := v.(int)
		tx.Put("seq", n+1)
		id = fmt.Sprintf("cap_%d", n+1)
		if key != "" {
			tx.Put("idem/"+key, id)
		}
		tx.OnCommit(func() { qobs.Record(ctx, "capture", nil, "key", req.Reference, "amount", req.Amount) })
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"id": %q}`, id)
}
