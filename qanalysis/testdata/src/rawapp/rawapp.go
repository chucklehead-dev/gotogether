package rawapp

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/chucklehead-dev/gotogether/grist"
	"google.golang.org/grpc"
)

var (
	db     *sql.DB
	conn   *grpc.ClientConn
	client = &http.Client{}
)

func effects() []grist.Result {
	return []grist.Result{
		grist.Effect(func(ctx context.Context) error {
			req, _ := http.NewRequestWithContext(ctx, "POST", "http://psp/charge", nil)
			_, err := client.Do(req) // want `a grist effect calls \(\*http.Client\).Do directly, reaching an HTTP service past the seam grist records`
			return err
		}, grist.AtLeastOnce),
		grist.Effect(func(ctx context.Context) error {
			_, err := http.Get("http://mailer/send") // want `calls http.Get directly`
			return err
		}, grist.AtLeastOnce),
		grist.Effect(func(ctx context.Context) error {
			_, err := db.ExecContext(ctx, "UPDATE t SET n = n + 1") // want `calls \(\*sql.DB\).ExecContext directly, reaching a database`
			return err
		}, grist.AtLeastOnce),
		grist.Effect(func(ctx context.Context) error {
			return conn.Invoke(ctx, "/bank/Pay", nil, nil) // want `calls \(\*grpc.ClientConn\).Invoke directly, reaching a gRPC service`
		}, grist.AtLeastOnce),
		grist.Effect(func(ctx context.Context) error {
			func() {
				db.Exec("DELETE FROM t") // want `calls \(\*sql.DB\).Exec directly`
			}()
			return nil
		}, grist.AtLeastOnce),
		grist.Effect(audit, grist.AtLeastOnce),
		grist.Effect(func(ctx context.Context) error {
			//grist:raw the warehouse's legacy endpoint, not declared until it has a client
			_, err := http.Post("http://warehouse/legacy", "text/plain", nil)
			_, _ = db.Query("SELECT 1") //grist:raw a health probe of the app's own database
			_, _ = db.Query("SELECT 2") /* want `//grist:raw without a reason on a call of \(\*sql.DB\).Query` */ //grist:raw
			return err
		}, grist.AtLeastOnce),
	}
}

// audit is an effect given by name.
func audit(ctx context.Context) error {
	_, err := db.QueryContext(ctx, "INSERT INTO audit VALUES (1)") // want `calls \(\*sql.DB\).QueryContext directly`
	return err
}

// outside an effect: not grist's seam to judge
func report() {
	db.Exec("VACUUM")
	http.Get("http://metrics/")
}
