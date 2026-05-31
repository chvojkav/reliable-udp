// Package lossy provides a net.PacketConn wrapper that injects configurable,
// reproducible adversarial behavior for testing reliable-transport
// implementations (SPEC.md §3).
//
// # Design choice: impairments on the SEND path
//
// Impairments are applied inside WriteTo, not ReadFrom. This mirrors the
// mental model of an unreliable network link: the sender hands a datagram to
// the network, and the network decides whether it arrives, arrives twice,
// arrives out of order, or arrives late. The underlying conn's ReadFrom is
// passed through unmodified.
//
// # Designing adversarial scenarios
//
// Designing adversarial SCENARIOS (pure loss, pure reorder, duplication,
// delay, combinations) is the assignee's job per docs/ASSIGNMENT.md Stage 2.
// This package only provides the knobs.
package lossy

import (
	"math/rand"
	"net"
	"sync"
	"time"
)

// EventKind describes what the Channel decided to do with a datagram.
type EventKind int

const (
	EventSent       EventKind = iota // passed through normally
	EventDropped                     // silently discarded
	EventDuplicated                  // sent a second time immediately after the first
	EventReordered                   // held back; will be flushed after the next non-reordered datagram
	EventDelayed                     // sent with artificial latency (Delay + jitter)
)

func (k EventKind) String() string {
	switch k {
	case EventSent:
		return "sent"
	case EventDropped:
		return "dropped"
	case EventDuplicated:
		return "duplicated"
	case EventReordered:
		return "reordered"
	case EventDelayed:
		return "delayed"
	default:
		return "unknown"
	}
}

// Event is the value passed to the optional Log hook on each WriteTo decision.
type Event struct {
	Kind EventKind
	Len  int      // datagram length in bytes
	Addr net.Addr // destination
}

// Config controls the impairments a Channel applies.
// All rate fields are probabilities in [0.0, 1.0].
type Config struct {
	DropRate      float64       // probability a datagram is silently dropped
	DuplicateRate float64       // probability a datagram is sent twice
	ReorderRate   float64       // probability a datagram is held back until after the next
	Delay         time.Duration // base latency added to every outgoing datagram
	DelayJitter   time.Duration // additional uniform-random latency in [0, DelayJitter)
	Seed          int64         // RNG seed; same seed ⇒ same decisions (reproducible)
}

type queued struct {
	p    []byte
	addr net.Addr
}

// Channel wraps a net.PacketConn and injects impairments on the send path.
//
// # Reordering
//
// A single-slot reorder buffer holds at most one datagram. When a datagram is
// held, it is flushed (sent) immediately after the next datagram that is not
// itself reordered — so the held datagram arrives after that subsequent one.
// If a second reorder decision arrives while the slot is occupied, the buffered
// datagram is flushed first so the buffer never grows. Close also flushes the
// slot, so no datagram is ever silently lost due to reordering.
//
// # Delay and deadlines
//
// When Delay or DelayJitter is non-zero, the actual WriteTo on the underlying
// conn runs in a background goroutine. The outer WriteTo returns immediately
// with no error; errors from the background send are silently discarded. This
// means a write deadline set on the underlying conn is checked at send time
// (possibly after the deadline has passed), not at call time. For strict
// write-deadline enforcement set Delay=0 and DelayJitter=0.
//
// Background goroutines spawned for delayed sends may outlive Close; they will
// attempt to write to a closed conn and produce an ignored error.
//
// # Determinism and the Seed field
//
// The sequence of impairment DECISIONS (which datagrams are dropped, duped,
// reordered, or delayed) is fully deterministic under a fixed Seed: all RNG
// calls happen under the mutex in the same order on every run with the same
// sequence of WriteTo calls.
//
// The real-time DELIVERY ORDER of delayed datagrams is NOT deterministic, even
// with a fixed Seed. When Delay > 0, each delayed send runs in its own
// goroutine; the OS scheduler can interleave them arbitrarily, so two runs
// with the same seed may deliver datagrams in different wall-clock order.
// ReorderRate+Delay combinations are therefore not reliably reproducible at
// the delivery level.
//
// A SynchronousDelay option (serialising delayed sends through an ordered
// queue) would make delivery order deterministic, but requires ~40 lines of
// race-safe channel/goroutine coordination and is deferred. For tests that
// need strict delivery reproducibility, set Delay=0 and DelayJitter=0 and
// rely on ReorderRate alone for out-of-order scenarios.
//
// # Concurrency
//
// WriteTo is safe to call from multiple goroutines. RNG decisions and the
// reorder buffer are protected by a mutex; the actual underlying WriteTo calls
// happen outside the lock so they do not serialize the caller.
type Channel struct {
	conn net.PacketConn
	cfg  Config
	mu   sync.Mutex
	rng  *rand.Rand
	held *queued // single-slot reorder buffer; guarded by mu

	// Log is an optional hook called for every WriteTo decision. Nil disables
	// logging. It is called without holding mu and must be safe to call
	// concurrently.
	Log func(Event)
}

// New wraps conn with the given impairment config.
// conn must outlive the returned Channel.
func New(conn net.PacketConn, cfg Config) *Channel {
	return &Channel{
		conn: conn,
		cfg:  cfg,
		rng:  rand.New(rand.NewSource(cfg.Seed)), //nolint:gosec // weak RNG is intentional for testing
	}
}

func (c *Channel) log(e Event) {
	if c.Log != nil {
		c.Log(e)
	}
}

// dispatch sends p to addr after sleeping delay. If delay is zero, the send is
// synchronous in the caller's goroutine; otherwise it runs in a new goroutine
// so WriteTo returns immediately.
func (c *Channel) dispatch(p []byte, addr net.Addr, delay time.Duration) {
	if delay <= 0 {
		c.conn.WriteTo(p, addr) //nolint:errcheck
		return
	}
	go func() {
		time.Sleep(delay)
		c.conn.WriteTo(p, addr) //nolint:errcheck
	}()
}

// WriteTo applies impairments then writes to the underlying conn.
// It always returns (len(p), nil) on the non-error path so callers never see
// short writes caused by impairments.
func (c *Channel) WriteTo(p []byte, addr net.Addr) (int, error) {
	n := len(p)

	// Copy p immediately: the caller may reuse the backing array after
	// WriteTo returns, and delayed/reordered sends happen asynchronously.
	buf := make([]byte, n)
	copy(buf, p)

	// All RNG decisions are made under the lock so the sequence is
	// deterministic regardless of goroutine scheduling.
	c.mu.Lock()

	drop := c.rng.Float64() < c.cfg.DropRate
	dup := !drop && c.rng.Float64() < c.cfg.DuplicateRate
	reorder := !drop && c.rng.Float64() < c.cfg.ReorderRate

	delay := c.cfg.Delay
	if c.cfg.DelayJitter > 0 {
		delay += time.Duration(c.rng.Int63n(int64(c.cfg.DelayJitter)))
	}

	if drop {
		c.mu.Unlock()
		c.log(Event{Kind: EventDropped, Len: n, Addr: addr})
		return n, nil
	}

	if reorder && c.held == nil {
		// Slot is empty: hold buf. It will be flushed after the next
		// datagram that is not itself reordered (or that finds the slot
		// full), so it arrives after that subsequent datagram.
		c.held = &queued{p: buf, addr: addr}
		c.mu.Unlock()
		c.log(Event{Kind: EventReordered, Len: n, Addr: addr})
		return n, nil
	}

	// Normal send (or reorder with a full slot — can't hold two datagrams,
	// so send buf now and flush the held one after, which still achieves
	// the out-of-order effect for the held datagram).
	// Either way: deliver buf first, then flush any held datagram so it
	// arrives after buf at the receiver.
	held := c.held
	c.held = nil
	c.mu.Unlock()

	kind := EventSent
	if delay > 0 {
		kind = EventDelayed
	}

	c.dispatch(buf, addr, delay)
	c.log(Event{Kind: kind, Len: n, Addr: addr})

	if dup {
		c.dispatch(buf, addr, delay)
		c.log(Event{Kind: EventDuplicated, Len: n, Addr: addr})
	}

	if held != nil {
		c.dispatch(held.p, held.addr, delay)
		c.log(Event{Kind: EventSent, Len: len(held.p), Addr: held.addr})
	}

	return n, nil
}

// ReadFrom delegates to the underlying conn. No impairments are applied on
// the receive path.
func (c *Channel) ReadFrom(p []byte) (int, net.Addr, error) {
	return c.conn.ReadFrom(p)
}

// Close flushes any datagram still sitting in the reorder buffer, then closes
// the underlying conn. Without this flush a held datagram would be silently
// lost if no subsequent WriteTo ever arrives — corrupting the effective drop
// rate in the assignee's tests (the datagram would disappear beyond the
// configured DropRate, making loss accounting wrong).
func (c *Channel) Close() error {
	c.mu.Lock()
	held := c.held
	c.held = nil
	c.mu.Unlock()

	if held != nil {
		c.conn.WriteTo(held.p, held.addr) //nolint:errcheck
		c.log(Event{Kind: EventSent, Len: len(held.p), Addr: held.addr})
	}

	return c.conn.Close()
}

// LocalAddr returns the local address of the underlying conn.
func (c *Channel) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// SetDeadline sets read and write deadlines on the underlying conn.
// See the Channel doc comment for how write deadlines interact with Delay.
func (c *Channel) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// SetReadDeadline sets the read deadline on the underlying conn.
func (c *Channel) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline on the underlying conn.
// See the Channel doc comment for how this interacts with Delay/DelayJitter.
func (c *Channel) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
