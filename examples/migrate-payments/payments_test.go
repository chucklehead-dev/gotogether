package payments_test

// The legacy tests, as they were before the migration: each request's
// answer asserted, one operation at a time. They pass, and they miss the
// legacy bug (no test refunds a charge twice) and the idempotency keys
// (no test retries one). migration_test.go runs each of them again,
// recorded, and judges its run by the model.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	payments "github.com/chucklehead-dev/gotogether/examples/migrate-payments"
)

// client calls the service's handler directly, on the test's goroutine.
type client struct {
	t *testing.T
	h http.Handler
}

func newClient(t *testing.T) *client {
	return &client{t, payments.New(payments.Config{Instance: "test"}).Handler()}
}

// do sends one request and returns its status and the charge it answered
// with (zero for a refusal).
func (c *client) do(method, path, key string, amount int64) (int, payments.Charge) {
	c.t.Helper()
	body := ""
	if amount != 0 {
		body = `{"amount": ` + itoa(amount) + `}`
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	var ch payments.Charge
	if w.Code < 300 {
		if err := json.Unmarshal(w.Body.Bytes(), &ch); err != nil {
			c.t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return w.Code, ch
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// authorized is a new authorized charge of amount.
func (c *client) authorized(amount int64) string {
	c.t.Helper()
	code, ch := c.do("POST", "/charges", "", amount)
	if code != http.StatusCreated || ch.Status != "authorized" {
		c.t.Fatalf("authorize %d: %d %+v", amount, code, ch)
	}
	return ch.ID
}

// captured is a new charge of amount, captured whole.
func (c *client) captured(amount int64) string {
	c.t.Helper()
	id := c.authorized(amount)
	if code, _ := c.do("POST", "/charges/"+id+"/capture", "", amount); code != http.StatusOK {
		c.t.Fatalf("capture %s: %d", id, code)
	}
	return id
}

func TestAuthorize(t *testing.T) {
	c := newClient(t)
	code, ch := c.do("POST", "/charges", "order-1", 1000)
	if code != http.StatusCreated || ch.Status != "authorized" || ch.Authorized != 1000 {
		t.Fatalf("got %d %+v", code, ch)
	}
}

func TestAuthorizeInvalid(t *testing.T) {
	c := newClient(t)
	if code, _ := c.do("POST", "/charges", "", 0); code != http.StatusBadRequest {
		t.Fatalf("a charge of 0: %d, want 400", code)
	}
}

func TestPartialCapture(t *testing.T) {
	c := newClient(t)
	id := c.authorized(1000)
	code, ch := c.do("POST", "/charges/"+id+"/capture", "", 600)
	if code != http.StatusOK || ch.Captured != 600 || ch.Status != "captured" {
		t.Fatalf("capture 600 of 1000: %d %+v", code, ch)
	}
	if _, ch := c.do("GET", "/charges/"+id, "", 0); ch.Captured != 600 {
		t.Fatalf("lookup: %+v", ch)
	}
}

func TestCaptureTooMuch(t *testing.T) {
	c := newClient(t)
	id := c.authorized(1000)
	if code, _ := c.do("POST", "/charges/"+id+"/capture", "", 1500); code != http.StatusUnprocessableEntity {
		t.Fatalf("capture 1500 of 1000: %d, want 422", code)
	}
}

func TestCaptureTwice(t *testing.T) {
	c := newClient(t)
	id := c.captured(1000)
	if code, _ := c.do("POST", "/charges/"+id+"/capture", "", 1000); code != http.StatusConflict {
		t.Fatalf("second capture: %d, want 409", code)
	}
}

func TestRefund(t *testing.T) {
	c := newClient(t)
	id := c.captured(1000)
	code, ch := c.do("POST", "/charges/"+id+"/refund", "", 1000)
	if code != http.StatusOK || ch.Refunded != 1000 {
		t.Fatalf("refund 1000: %d %+v", code, ch)
	}
}

func TestPartialRefund(t *testing.T) {
	c := newClient(t)
	id := c.captured(1000)
	code, ch := c.do("POST", "/charges/"+id+"/refund", "", 300)
	if code != http.StatusOK || ch.Refunded != 300 {
		t.Fatalf("refund 300: %d %+v", code, ch)
	}
}

func TestRefundTooMuch(t *testing.T) {
	c := newClient(t)
	id := c.captured(1000)
	if code, _ := c.do("POST", "/charges/"+id+"/refund", "", 1500); code != http.StatusUnprocessableEntity {
		t.Fatalf("refund 1500 of 1000: %d, want 422", code)
	}
}

func TestRefundBeforeCapture(t *testing.T) {
	c := newClient(t)
	id := c.authorized(1000)
	if code, _ := c.do("POST", "/charges/"+id+"/refund", "", 100); code != http.StatusConflict {
		t.Fatalf("refund of an authorization: %d, want 409", code)
	}
}

func TestVoid(t *testing.T) {
	c := newClient(t)
	id := c.authorized(1000)
	if code, ch := c.do("POST", "/charges/"+id+"/void", "", 0); code != http.StatusOK || ch.Status != "voided" {
		t.Fatalf("void: %d %+v", code, ch)
	}
}

func TestVoidAfterCapture(t *testing.T) {
	c := newClient(t)
	id := c.captured(1000)
	if code, _ := c.do("POST", "/charges/"+id+"/void", "", 0); code != http.StatusConflict {
		t.Fatalf("void of a captured charge: %d, want 409", code)
	}
}

func TestLookupUnknown(t *testing.T) {
	c := newClient(t)
	if code, _ := c.do("GET", "/charges/ch_404", "", 0); code != http.StatusNotFound {
		t.Fatalf("lookup of no charge: %d, want 404", code)
	}
}
