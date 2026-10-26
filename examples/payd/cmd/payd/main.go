// Command payd runs the payments service for real: payd over SQLite,
// listening on -addr, capturing at -processor (by default the processor
// double, in process, on a port of its own).
//
//	go run ./cmd/payd -db /tmp/payd.db
//	curl -s -X POST localhost:8080/v1/charges -H 'Idempotency-Key: k1' -d '{"account": "acct_1", "amount": 250}'
//
// The accounts of harness.Accounts are created on first start.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"time"

	harness "github.com/chucklehead-dev/gotogether/examples/payd"
	"github.com/chucklehead-dev/gotogether/examples/payd/payd"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "address to listen on")
	path := flag.String("db", "payd.db", "the SQLite database")
	proc := flag.String("processor", "", "the processor's base URL (default: the double, in process)")
	flag.Parse()

	ctx := context.Background()
	db, err := harness.OpenSQLite(*path)
	if err != nil {
		log.Fatal(err)
	}
	if err := payd.Migrate(ctx, db); err != nil {
		log.Fatal(err)
	}
	for id, balance := range harness.Accounts {
		if _, err := db.ExecContext(ctx, `INSERT INTO accounts (id, balance, n_charges) VALUES (?, ?, 0) ON CONFLICT (id) DO NOTHING`, id, balance); err != nil {
			log.Fatal(err)
		}
	}
	if *proc == "" {
		l, err := net.Listen("tcp", "localhost:0")
		if err != nil {
			log.Fatal(err)
		}
		go http.Serve(l, &harness.RealProcessor{})
		*proc = "http://" + l.Addr().String()
		log.Printf("processor double at %s", *proc)
	}
	log.Printf("payd at http://%s, database %s", *addr, *path)
	log.Fatal(http.ListenAndServe(*addr, payd.New(db, &http.Client{Timeout: 5 * time.Second}, *proc)))
}
