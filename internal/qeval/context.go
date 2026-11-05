package qeval

import "context"

// WithContext returns an immutable request view of m. Declarations and static
// metadata remain shared, while cancellation belongs only to this view and its
// evaluations. A background context detaches request ownership.
func (m *Model) WithContext(ctx context.Context) *Model {
	if ctx == nil {
		panic("qeval: nil context")
	}
	c := *m
	if ctx.Done() == nil {
		ctx = nil
	}
	c.ctx = ctx
	return &c
}

func (m *Model) detached() *Model {
	if m.ctx == nil {
		return m
	}
	c := *m
	c.ctx = nil
	return &c
}

func (m *Model) contextErr() error {
	if m.ctx != nil {
		return m.ctx.Err()
	}
	return nil
}

func (m *Model) checkContext() {
	if err := m.contextErr(); err != nil {
		throw(err)
	}
}

// finishContext also catches cancellation swallowed by a lazy unused let.
func (m *Model) finishContext(err *error) {
	if canceled := m.contextErr(); canceled != nil {
		*err = canceled
	}
}

func (ev *evaluation) checkpoint() {
	if ev.m.ctx == nil {
		return
	}
	ev.ticks++
	if ev.ticks == 0 {
		ev.m.checkContext()
	}
}

// newContextCheckpoint is local to a traversal, never retained by a graph.
func newContextCheckpoint(ctx context.Context) func() {
	if ctx.Done() == nil {
		return nil
	}
	var ticks uint8
	return func() {
		ticks++
		if ticks == 0 {
			if err := ctx.Err(); err != nil {
				throw(err)
			}
		}
	}
}
