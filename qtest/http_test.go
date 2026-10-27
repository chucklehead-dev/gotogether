package qtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qobs/qobshttp"
)

// account is an HTTP service of one account: POST /deposit?amount=n
// records the deposit where it takes effect, under the lock that orders
// it; /balance answers the balance and records nothing.
func accountService() http.Handler {
	var mu sync.Mutex
	bal := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/deposit", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("amount"))
		mu.Lock()
		defer mu.Unlock()
		bal += n
		qobs.Record(r.Context(), "deposit", nil, "amount", n, "obs.balance", bal)
	})
	mux.HandleFunc("/balance", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(w, bal)
	})
	return mux
}

// drive deposits amount five times, then reads the balance and records a
// withdrawal of 1 it makes as the service's client would (on return, on
// the scenario's goroutine): a step of the client side, in the same run
// as the handlers'.
func drive(t testing.TB, url string, amount int) {
	for range 5 {
		resp, err := http.Post(fmt.Sprintf("%s/deposit?amount=%d", url, amount), "", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	resp, err := http.Get(url + "/balance")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	bal, _ := strconv.Atoi(string(b))
	qobs.Record(context.Background(), "withdraw", nil, "amount", 1, "obs.balance", bal-1)
}

// An HTTP service's handlers record into the run of the Record that
// started their server (qobshttp.Handler), with runs in parallel: each
// run's balances are its own deposits', so a step of the other run's
// would be a mismatch. Without the wrapper the handlers' steps escape, and
// Record fails naming it.
func TestRecordHTTPServiceInParallel(t *testing.T) {
	needQuint(t)
	t.Run("parallel", func(t *testing.T) {
		for _, amount := range []int{3, 4, 5} {
			t.Run(fmt.Sprint(amount), func(t *testing.T) {
				t.Parallel()
				Record(t, account, func(rec *qobs.Recorder) {
					srv := httptest.NewServer(qobshttp.Handler(accountService(), rec))
					defer srv.Close()
					drive(t, srv.URL, amount)
				}, opts()...).Expect(t, Conforms())
			})
		}
	})
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		Record(f, account, func(*qobs.Recorder) {
			srv := httptest.NewServer(accountService())
			defer srv.Close()
			drive(f, srv.URL, 3)
		}, opts()...)
	}()
	<-done
	if len(f.errs) == 0 || !strings.Contains(f.errs[0], "qobshttp.Handler(h, rec)") || strings.Contains(f.errs[0], "qenv.Go") {
		t.Errorf("unwrapped: errors %q, want the escaped steps named with the HTTP fix", f.errs)
	}
}
