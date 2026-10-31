package grist

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// ErrRewound is a tenant's log that went backwards under a node: a
// managed database's failover that lost its asynchronously replicated
// tail, or a restore from a backup or a volume snapshot
// (docs/grist-platform.md §3.4). Commits a client holds a receipt for
// may be gone, and effects already made for triggers the log now holds
// again would run again: the node fails closed for effects (its
// processors of the tenant stop, decision 3), while commands and queries
// go on (the log is consistent from here). An operator decides what the
// lost commits did (`<app> steps -export` from another node's file, the
// dependencies' own records), then runs `<app> resume -tenant <t>`
// (Resume); nothing resumes on its own. Errors are *RewoundError, which
// Is ErrRewound.
var ErrRewound = errors.New("grist: the tenant's log went backwards")

// A RewoundError says how a node saw a tenant's log go backwards: the
// highest head and step row it had seen, what it read instead, and the
// fix.
type RewoundError struct {
	Tenant string
	// Seen is the highest head the node had seen, Head the head (or for a
	// checkpoint, the checkpoint) it read instead; Processor names the
	// processor whose checkpoint went back, or is "" for the head or the
	// step log.
	Seen, Head Position
	Processor  string
	// Steps is how the step log went back (the relay's: "row 12 is not
	// the row it recorded"), "" when a poller saw it.
	Steps string
}

func (e *RewoundError) Error() string {
	var what string
	switch {
	case e.Steps != "":
		what = "its step log went backwards: " + e.Steps
	case e.Processor != "" && e.Head > e.Seen:
		what = fmt.Sprintf("processor %s's checkpoint %d is past the head %d", e.Processor, e.Seen, e.Head)
	case e.Processor != "":
		what = fmt.Sprintf("processor %s's checkpoint went back from %d to %d", e.Processor, e.Seen, e.Head)
	default:
		what = fmt.Sprintf("its head went back from %d to %d", e.Seen, e.Head)
	}
	return fmt.Sprintf("grist: tenant %s's log went backwards (a failover lost its tail, or a restore): %s; its processors are stopped, "+
		"so no effect runs again for a trigger it already ran for (commands and queries go on). Find what the lost commits did (`<app> steps -export` "+
		"from a node's steps, the dependencies' records), then run `<app> resume -tenant %s`", e.Tenant, what, e.Tenant)
}

// Is reports whether target is ErrRewound.
func (e *RewoundError) Is(target error) bool { return target == ErrRewound }

// A tenantClock is a node's memory of a tenant's log (its relay's, shared
// by the node's tasks and its requests): the highest head and checkpoints
// it saw, whether its processors are stopped by a rewind, and the base its
// steps' domain clocks (order.*) are recorded at.
type tenantClock struct {
	// generation retires processor work across an observed rewind, including
	// work whose callback returns only after a later resume.
	generation uint64
	head       Position
	cps        map[string]Position
	stopped    *RewoundError
	// recorded: the relay recorded the stop's gristRewound step, at
	// rewindAt on the tenant's clock
	recorded bool
	rewindAt int64
	// base is added to every order.* value of the tenant's steps recorded
	// now: 0 until a rewind, then past every value before it, so the new
	// history's steps come after the old's and the rewind's
	base int64
	// resumed is notified when the processors may go on
	resumed qenv.Signal
	// marks are each poller's own highest head and checkpoint read (by
	// its key): a poller's reads come one after another, so its own go
	// back only when the log does; two pollers' do not compare (one's read
	// begun before the other's committed may end after it)
	marks map[string]*pollMark
}

// clocks are a node's tenant clocks (Relay.clocks).
type clocks struct {
	mu sync.Mutex
	t  map[string]*tenantClock
	// noStop is the fail-closed's ablation (a test's): rewinds are seen
	// and recorded, the processors never stop
	noStop bool
	// onRewind, if set, is told of each rewind once (Main logs it)
	onRewind func(*RewoundError)
}

// of is tenant t's clock (under c.mu).
func (c *clocks) of(t string) *tenantClock {
	if c.t == nil {
		c.t = map[string]*tenantClock{}
	}
	tc := c.t[t]
	if tc == nil {
		tc = &tenantClock{cps: map[string]Position{}, marks: map[string]*pollMark{}}
		c.t[t] = tc
	}
	return tc
}

// A pollMark is a poller's highest head and checkpoint read.
type pollMark struct{ head, cp Position }

// stop stops t's processors for e, once a rewind, and moves the tenant's
// clock past every value of the old history: the rewind's step (which the
// relay records next) at rewindAt, every step recorded from now at the new
// base, after it.
func (c *clocks) stop(tc *tenantClock, e *RewoundError) {
	if tc.stopped != nil {
		return
	}
	seen := max(tc.head, e.Seen, e.Head)
	for _, cp := range tc.cps {
		seen = max(seen, cp)
	}
	tc.generation++
	tc.stopped, tc.recorded = e, false
	tc.rewindAt = tc.base + 2*int64(seen+1) + 1
	tc.base = tc.rewindAt + 1
	if c.onRewind != nil {
		c.onRewind(e)
	}
}

// saw raises t's memory of its head (a row the relay recorded).
func (c *clocks) saw(t string, head Position) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tc := c.of(t)
	tc.head = max(tc.head, head)
}

// stopFor stops t's processors for e (the relay's finding), once.
func (c *clocks) stopFor(t string, e *RewoundError) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stop(c.of(t), e)
}

// observeWork checks a current poller's checkpoint and head against its earlier
// reads. A lower head or checkpoint, or a checkpoint past the head, stops the
// tenant. Retired reads are discarded atomically with the generation check;
// the bool reports whether this observation belonged to current work.
func (c *clocks) observeWork(ctx context.Context, t, p, key string, cp, head Position) (*RewoundError, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.workCurrentLocked(ctx, t) {
		return nil, false
	}
	return c.observeLocked(t, p, key, cp, head), true
}

func (c *clocks) observeLocked(t, p, key string, cp, head Position) *RewoundError {
	tc := c.of(t)
	m := tc.marks[key]
	if m == nil {
		m = &pollMark{}
		tc.marks[key] = m
	}
	switch {
	case tc.stopped != nil:
	case head < m.head:
		c.stop(tc, &RewoundError{Tenant: t, Seen: m.head, Head: head})
	case cp < m.cp:
		c.stop(tc, &RewoundError{Tenant: t, Seen: m.cp, Head: cp, Processor: p})
	case cp > head:
		c.stop(tc, &RewoundError{Tenant: t, Seen: cp, Head: head, Processor: p})
	default:
		m.head, m.cp = max(m.head, head), max(m.cp, cp)
		tc.head = max(tc.head, head)
		tc.cps[p] = max(tc.cps[p], cp)
	}
	if c.noStop {
		return nil
	}
	return tc.stopped
}

// committedWork raises a current poller's remembered checkpoint and head after
// its own commit. It cannot revoke an admitted transaction, but a retired
// acknowledgment must not seed the resumed generation's memory.
func (c *clocks) committedWork(ctx context.Context, t, p, key string, cp, head Position) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workCurrentLocked(ctx, t) {
		c.committedLocked(t, p, key, cp, head)
	}
}

func (c *clocks) committedLocked(t, p, key string, cp, head Position) {
	tc := c.of(t)
	if m := tc.marks[key]; m != nil {
		m.head, m.cp = max(m.head, head), max(m.cp, cp)
	}
	tc.head = max(tc.head, head)
	tc.cps[p] = max(tc.cps[p], cp)
}

// stopped is t's stop, nil while its processors run.
func (c *clocks) stopped(t string) *RewoundError {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.noStop {
		return nil
	}
	return c.of(t).stopped
}

// processorWork pins a batch to the observed history in which it was read.
// It is admission fencing only: a rewind cannot revoke an already-issued store
// transaction or external call, and an unobserved restore is outside this fence.
type processorWork struct {
	clocks     *clocks
	tenant     string
	generation uint64
}
type processorWorkKey struct{}

func (c *clocks) workContext(ctx context.Context, tenant string) context.Context {
	if w, ok := ctx.Value(processorWorkKey{}).(processorWork); ok && w.clocks == c && w.tenant == tenant {
		return ctx
	}
	c.mu.Lock()
	generation := c.of(tenant).generation
	c.mu.Unlock()
	return context.WithValue(ctx, processorWorkKey{}, processorWork{c, tenant, generation})
}

func (c *clocks) workCurrent(ctx context.Context, tenant string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.workCurrentLocked(ctx, tenant)
}

func (c *clocks) workCurrentLocked(ctx context.Context, tenant string) bool {
	if c.noStop {
		return true
	}
	tc := c.of(tenant)
	w, ok := ctx.Value(processorWorkKey{}).(processorWork)
	return ok && w.clocks == c && w.tenant == tenant && w.generation == tc.generation && tc.stopped == nil
}

// waitResumed waits until t's processors may go on, or ctx is done.
func (c *clocks) waitResumed(ctx context.Context, t string) {
	for ctx.Err() == nil {
		c.mu.Lock()
		tc := c.of(t)
		stopped := tc.stopped != nil && !c.noStop
		c.mu.Unlock()
		if !stopped {
			return
		}
		tc.resumed.Wait(ctx, 0)
	}
}

// base is t's clock base now: what every order.* value of a step of t
// recorded now is shifted by.
func (c *clocks) base(t string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.of(t).base
}

// rewound marks t's rewind recorded and returns its
// step's place on the tenant's clock.
func (c *clocks) rewound(t string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	tc := c.of(t)
	tc.recorded = true
	return tc.rewindAt
}

// resume lets t's processors go on, their memory now the log's (head and
// checkpoints read again from here), and wakes them.
func (c *clocks) resume(t string) {
	c.mu.Lock()
	tc := c.of(t)
	tc.stopped, tc.recorded = nil, false
	tc.head, tc.cps, tc.marks = 0, map[string]Position{}, map[string]*pollMark{}
	// past the resume's own step (a read's place at its head): an effect
	// that reads at that head after it is ordered after it, not beside it
	tc.base += 2
	c.mu.Unlock()
	tc.resumed.Notify()
}

// toRecord reports whether t has a stop its relay has not recorded.
func (c *clocks) toRecord(t string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	tc := c.of(t)
	return tc.stopped != nil && !tc.recorded
}

// Rewound is every tenant whose processors a rewind stopped on this
// relay's node (ErrRewound), sorted by tenant: what Main's /metrics and
// its log report until `<app> resume`.
func (r *Relay) Rewound() []*RewoundError {
	r.clocks.mu.Lock()
	defer r.clocks.mu.Unlock()
	var out []*RewoundError
	for _, t := range slices.Sorted(maps.Keys(r.clocks.t)) {
		if e := r.clocks.t[t].stopped; e != nil {
			out = append(out, e)
		}
	}
	return out
}

// FailOpen makes r's node go on after a rewind (ErrRewound seen and
// recorded, the processors never stopped): the fail-closed's ablation, for
// a test that shows what it prevents (an at-most-once effect run again,
// unasked). Never in a deployment.
func (r *Relay) FailOpen() {
	r.clocks.mu.Lock()
	defer r.clocks.mu.Unlock()
	r.clocks.noStop = true
}

// OnRewind makes r tell f of each rewind it or its node's pollers see
// (once each, as they stop the tenant's processors): Main logs it,
// gristsim's operator waits on it. f runs where the rewind is seen and
// must not block.
func (r *Relay) OnRewind(f func(*RewoundError)) {
	r.clocks.mu.Lock()
	defer r.clocks.mu.Unlock()
	r.clocks.onRewind = f
}

// With is ctx carrying r's clocks: a command or query served with it
// records its step, after a rewind r saw, on the tenant's new clock
// (after the rewind's step), as the node's own tasks do. Main serves its
// API with it; a harness serving requests beside a node with its relay
// (gristsim) gives them it.
func (r *Relay) With(ctx context.Context) context.Context {
	return context.WithValue(ctx, clocksKey{}, &r.clocks)
}

type clocksKey struct{}

// clockBase is the base of tenant's clock in ctx's clocks (0 without).
func clockBase(ctx context.Context, tenant string) int64 {
	if c, ok := ctx.Value(clocksKey{}).(*clocks); ok {
		return c.base(tenant)
	}
	return 0
}

// shiftOrders adds base to every order.* value of kv (a step's pairs).
func shiftOrders(kv []any, base int64) {
	if base == 0 {
		return
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok && strings.HasPrefix(k, "order.") {
			if v, ok := kv[i+1].(int64); ok {
				kv[i+1] = v + base
			}
		}
	}
}

// A rowMark is what a relay keeps of a row it recorded, to tell whether
// the log still holds it: its head, time and step's checksum; the command
// ids its steps answered; and every processor's checkpoint after it.
type rowMark struct {
	seq  int64
	head Position
	time int64
	sum  uint64
	ids  []string
	cps  map[string]Position // shared with the next rows until one moves a checkpoint
}

// markWindow is how many recorded rows a relay remembers (rowMark): a log
// that went back further is still seen, but its rewind is recorded from
// the oldest row remembered.
const markWindow = 1 << 16

// rowSum is a row's checksum.
func rowSum(row store.StepRow) uint64 {
	h := fnv.New64a()
	h.Write(row.Step)
	return h.Sum64()
}

// same reports whether row is the row m was made of.
func (m rowMark) same(row store.StepRow) bool {
	return row.Seq == m.seq && row.Head == m.head && row.Time == m.time && rowSum(row) == m.sum
}

// mark is p's memory of row, whose steps are steps, recorded now.
func (r *Relay) mark(p *relayed, row store.StepRow, steps []relayStep) {
	cps := map[string]Position{}
	if n := len(p.marks); n > 0 {
		cps = p.marks[n-1].cps
	}
	m := rowMark{seq: row.Seq, head: row.Head, time: row.Time, sum: rowSum(row), cps: cps}
	app := r.app.Load()
	for _, s := range steps {
		for i := 0; i+1 < len(s.kv); i += 2 {
			k, _ := s.kv[i].(string)
			switch {
			case k == "id":
				if id, ok := s.kv[i+1].(string); ok {
					m.ids = append(m.ids, id)
				}
			case k == "obs.checkpoint" && app != nil:
				// the processor is its order key's: order.p.<quint name>
				for j := 0; j+1 < len(s.kv); j += 2 {
					if ok2, _ := s.kv[j].(string); strings.HasPrefix(ok2, "order.p.") {
						if name := app.processorOf(strings.TrimPrefix(ok2, "order.p.")); name != "" {
							if v, ok := s.kv[i+1].(int64); ok {
								if m.cps[name] != Position(v) {
									m.cps = maps.Clone(m.cps)
									m.cps[name] = Position(v)
								}
							}
						}
					}
				}
			}
		}
	}
	p.marks = append(p.marks, m)
	if len(p.marks) > markWindow {
		p.marks = slices.Delete(p.marks, 0, len(p.marks)-markWindow)
	}
}

// processorOf is the name of the processor whose Quint name is quint.
func (a *App) processorOf(quint string) string {
	for _, p := range a.processors {
		if p.quint == quint {
			return p.name
		}
	}
	return ""
}

// markOf is p's mark of row seq, if it remembers it.
func (p *relayed) markOf(seq int64) (rowMark, bool) {
	if len(p.marks) == 0 {
		return rowMark{}, false
	}
	i := seq - p.marks[0].seq
	if i < 0 || i >= int64(len(p.marks)) {
		return rowMark{}, false
	}
	return p.marks[i], true
}

// diverged finds where t's step log and p's recorded rows part: the last
// row both hold (0: none), reading back from the rows it recorded. ok is
// false when they part before the oldest row p remembers.
func (r *Relay) diverged(ctx context.Context, t string, p *relayed) (int64, bool, error) {
	hi := p.rows
	first := int64(1)
	if len(p.marks) > 0 {
		first = p.marks[0].seq
	}
	for hi >= first {
		lo := max(first, hi-relayBatch+1)
		rows, err := r.log.Steps(ctx, t, lo-1, int(hi-lo+1))
		if err != nil {
			return 0, false, err
		}
		for i := len(rows) - 1; i >= 0; i-- {
			if m, ok := p.markOf(rows[i].Seq); ok && rows[i].Seq <= hi && m.same(rows[i]) {
				return rows[i].Seq, true, nil
			}
		}
		hi = lo - 1
	}
	return first - 1, first == 1, nil
}

// recordRewind records t's rewind as the step gristRewound, on its own
// thread: the log back to the last row the relay and the store share (its
// head, every processor's checkpoint after it, the command ids answered
// by the rows after it, lost), and p's progress back to that row, so the
// new history's rows are recorded after it, on the tenant's new clock.
func (r *Relay) recordRewind(ctx context.Context, t string, p *relayed) error {
	d, ok, err := r.diverged(ctx, t, p)
	if err != nil {
		return err
	}
	var head Position
	cps := map[string]Position{}
	if m, found := p.markOf(d); found {
		head, cps = m.head, m.cps
	}
	var lost []string
	seen := head
	for seq := d + 1; seq <= p.rows; seq++ {
		if m, found := p.markOf(seq); found {
			lost = append(lost, m.ids...)
			seen = max(seen, m.head)
		}
	}
	all := map[string]int64{}
	if app := r.app.Load(); app != nil {
		for _, pr := range app.processors {
			all[pr.name] = int64(cps[pr.name])
		}
	}
	slices.Sort(lost)
	lost = slices.Compact(lost)
	if lost == nil {
		lost = []string{}
	}
	kv := []any{"pos", int64(head), "cps", all, "lost", lost, "seen", int64(seen), "rows", p.rows, "kept", d}
	if !ok {
		kv = append(kv, "beyond", "the log went back past the oldest row the relay remembers: lost lists only what it remembers")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !r.clocks.toRecord(t) {
		return nil // another flush recorded it
	}
	kv = append(kv, "order.pos", r.clocks.rewound(t))
	p.scratch.Record(context.Background(), "gristRewound", nil, kv...)
	out := p.sink.Steps()
	p.sink.Reset()
	if rec := qobs.From(ctx); rec != nil {
		for i := range out {
			out[i].Time, out[i].Thread = qenv.Now(ctx), "rewinds"
		}
		rec.Add(out...)
	} else {
		qobs.Record(ctx, "gristRewound", nil) // no recorder: dropped, or a strict run's escape, as any step's
	}
	// back to the shared row: the new history's rows after it are recorded
	// next, the marks of the old ones dropped
	for len(p.marks) > 0 && p.marks[len(p.marks)-1].seq > d {
		p.marks = p.marks[:len(p.marks)-1]
	}
	p.rows = d
	p.done.Store(d)
	p.epoch++ // a flush that read rows before it records none of them
	for pos := range p.causes {
		if pos > head {
			delete(p.causes, pos)
		}
	}
	p.verified.Store(int64(head))
	return nil
}

// noteCauses keeps the causes of a recorded row's events by position
// (under p.mu): a step's events end at its head, each caused as its
// obs.causes says ([kind, name]: a command's id, or "processor:" and the
// processor's name, as the store stamps Stored.Cause).
func (p *relayed) noteCauses(steps []relayStep) {
	if p.causes == nil {
		p.causes = map[Position]string{}
	}
	for _, s := range steps {
		var causes []any
		for i := 0; i+1 < len(s.kv); i += 2 {
			if s.kv[i] == "obs.causes" {
				causes, _ = s.kv[i+1].([]any)
			}
		}
		for i, c := range causes {
			pair, _ := c.([]any)
			if len(pair) != 2 {
				continue
			}
			kind, _ := pair[0].(string)
			name, _ := pair[1].(string)
			if kind == "processor" {
				name = "processor:" + name
			}
			pos := s.head - Position(len(causes)) + Position(i) + 1
			p.causes[pos] = name
			delete(p.causes, pos-markWindow)
		}
	}
}

// verify waits until t's relay recorded the commit of trigger e, or every row the log has now (its
// relay's progress, so a poller handles only what the relay has seen
// the store hold), and reports a rewind (stopping t's processors) when the
// event there is not the one recorded: a log that went backwards and
// grew again to past the position before the relay looked. It returns
// the stop, if t's processors are stopped, or ctx's end.
func (r *Relay) verify(ctx context.Context, t string, e Stored) *RewoundError {
	p := r.tenant(t)
	// caught up with the step log as it is now: a trigger appended with no
	// step row (a test's raw append) has none to wait for
	if n, err := r.log.StepCount(ctx, t); err == nil && p.done.Load() < n && p.verified.Load() < int64(e.Pos) {
		qenv.Wait(ctx, func() bool {
			return p.done.Load() >= n || p.verified.Load() >= int64(e.Pos) || r.clocks.stopped(t) != nil || ctx.Err() != nil
		})
	}
	if stop := r.clocks.stopped(t); stop != nil || ctx.Err() != nil {
		return stop
	}
	p.mu.Lock()
	cause, ok := p.causes[e.Pos]
	p.mu.Unlock()
	if ok && cause != e.Cause {
		r.clocks.stopFor(t, &RewoundError{Tenant: t, Seen: Position(p.verified.Load()), Head: e.Pos,
			Steps: fmt.Sprintf("the event at position %d is caused by %s, and the relay recorded one caused by %s there", e.Pos, e.Cause, cause)})
	}
	return r.clocks.stopped(t)
}

// recordRewindNow records t's rewind if one was seen and not yet
// recorded: a poller's finding, recorded before any step of the new
// history (the relay's own wait sleeps until the store changes).
func (r *Relay) recordRewindNow(ctx context.Context, t string) {
	if r.clocks.toRecord(t) {
		r.recordRewind(ctx, t, r.tenant(t)) // an error leaves it to the relay's next flush
	}
}

// A pinned base is the clock base a task's steps of a tenant are recorded
// at, fixed where it read (withBase): an effect or a request that read
// before a rewind records on the old clock, whenever it records.
type pinned struct {
	tenant string
	base   int64
}

type pinnedKey struct{}

// withBase is ctx with tenant's steps recorded at base.
func withBase(ctx context.Context, tenant string, base int64) context.Context {
	return context.WithValue(ctx, pinnedKey{}, pinned{tenant, base})
}

// pinBase is ctx with tenant's steps recorded at its clock's base now
// (clockBase): a request's, at its start.
func pinBase(ctx context.Context, tenant string) context.Context {
	return withBase(ctx, tenant, clockBase(ctx, tenant))
}

// orderBase is the base tenant's steps recorded with ctx take: the pinned
// one, else the clock's now.
func orderBase(ctx context.Context, tenant string) int64 {
	if p, ok := ctx.Value(pinnedKey{}).(pinned); ok && p.tenant == tenant {
		return p.base
	}
	return clockBase(ctx, tenant)
}

// resumeID is the marker id of an operator's resume at time ns: one per
// resume, so a resume run twice commits twice (each a gristResumed step).
func resumeID(ns int64) string { return reservedID + "resume/" + strconv.FormatInt(ns, 10) }

// Resume is `<app> resume`: it lets every node's processors of tenant go
// on after a rewind stopped them (ErrRewound), from their checkpoints, so
// the effects of the triggers after them run again (at least once, under
// each retained event's same EffectKey: a Keyed dependency answers from its record; at most
// once, again: decide first). It commits a step row with no event, the
// step gristResumed, which each node's relay reads; a node that saw no
// rewind records it and goes on. Never automatic (docs/grist-platform.md
// decision 3).
func Resume(ctx context.Context, s Store, tenant string) error {
	if err := CheckTenant(tenant); err != nil {
		return err
	}
	now := qenv.Now(ctx)
	b := store.Batch{Marker: &store.Marker{ID: resumeID(now.UnixNano()), Hash: "resume"}, Time: now.UnixMilli(), Cause: "grist:resume",
		Step: encodeRow([]rowStep{newRowStep("gristResumed", 0)})}
	for try := 1; ; try++ {
		_, err := s.Append(ctx, tenant, b)
		var cf *store.ConditionFailed
		switch {
		case err == nil, errors.As(err, &cf) && cf.Marker != "":
			return nil // committed (by an earlier try whose answer was lost)
		case !store.Transient(err) || try >= defaultTransientTries:
			return fmt.Errorf("grist: resuming tenant %s: %w", tenant, err)
		}
		backoff(ctx, try)
	}
}
