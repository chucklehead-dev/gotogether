// Command payments runs the legacy payment handler, as production does:
// its audit log as JSON lines on stdout.
//
//	go run ./cmd/payments -addr localhost:8080 > audit.jsonl
package main

import (
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"

	payments "github.com/chucklehead-dev/gotogether/examples/migrate-payments"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	instance := flag.String("instance", "", "this process's name in the audit log (default: the host name)")
	webhook := flag.String("webhook", "", "the merchant's notification endpoint")
	flag.Parse()
	svc := payments.New(payments.Config{
		Logger:     slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		Instance:   *instance,
		WebhookURL: *webhook,
	})
	log.Fatal(http.ListenAndServe(*addr, svc.Handler()))
}
