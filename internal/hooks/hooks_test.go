package hooks

import (
	"context"
	"testing"
)

// A goroutine's creator is read from its stack's last line, and a step
// escaping from it goes to the creator's run's log when that run keeps
// one, else process-wide.
func TestEscapeToTheCreatorsRun(t *testing.T) {
	parent := GoroutineID()
	var got int64
	var ok bool
	done := make(chan struct{})
	go func() { //qenv:ok the goroutine whose creator is read
		defer close(done)
		got, ok = creatorID()
	}()
	<-done
	if !ok || got != parent {
		t.Fatalf("creatorID = %d, %v; want %d", got, ok, parent)
	}

	var e Escapes
	SetCreatorRun(func(id int64) (context.Context, bool) {
		if id != parent {
			return nil, false
		}
		return WithEscapes(context.Background(), &e), true
	})
	defer creatorRun.Store(nil)
	mark, _ := Escaped(0)
	done = make(chan struct{})
	go func() { //qenv:ok an escaped step from a goroutine this one started
		defer close(done)
		Escape("one")
	}()
	<-done
	if n, first := e.Steps(); n != 1 || first != "one" {
		t.Errorf("the run's log: %d, %q; want the step", n, first)
	}
	if n, _ := Escaped(mark); n != mark {
		t.Error("the step was logged process-wide too")
	}
	Escape("unowned") // the test's own creator has no run
	if n, msgs := Escaped(mark); n != mark+1 || msgs[0] != "unowned" {
		t.Errorf("process-wide: %d since %d, %q", n, mark, msgs)
	}
}
