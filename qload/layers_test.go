package qload_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qload"
)

// outer records the retries it is asked about, and gives up after two.
type outer struct{ asked []int }

func (o *outer) Request(time.Time) {}
func (o *outer) Retry(n int, _ time.Time) (time.Duration, bool) {
	o.asked = append(o.asked, n)
	return time.Second, n <= 2
}

// A proxy's three tries per client attempt: its retries at once, the
// client asked only at each third, about its own retry count; the client
// giving up after its second retry ends the request at nine attempts.
func TestLayeredMultipliesAttempts(t *testing.T) {
	o := &outer{}
	l := qload.Layered(3, o)
	var got []string
	for n := 1; ; n++ {
		wait, ok := l.Retry(n, time.Time{})
		if !ok {
			got = append(got, fmt.Sprintf("%d:stop", n))
			break
		}
		got = append(got, fmt.Sprintf("%d:%v", n, wait))
	}
	want := "[1:0s 2:0s 3:1s 4:0s 5:0s 6:1s 7:0s 8:0s 9:stop]"
	if fmt.Sprint(got) != want || fmt.Sprint(o.asked) != "[1 2 3]" {
		t.Fatalf("retries %v (client asked %v), want %s (asked [1 2 3])", got, o.asked, want)
	}
}
