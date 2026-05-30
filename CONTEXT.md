# Context for Future Self

Notes-to-self about where this is going, why things are shaped the way they are, and what was
deliberately left undone. Not assignee-facing (though harmless if read).

---

## Why the design is shaped this way

The whole structure exists to make **one piece of assignee code run unchanged in three contexts**:
against a real `*net.UDPConn` (the mains), against the `lossy` wrapper (tests), and against an
in-memory channel (also tests). That is the payoff of two decisions:

1. The transport **consumes** `net.PacketConn` (injected, never owned) — so the channel is swappable.
2. The transport **implements** `net.Conn` — so it is a drop-in for anything expecting a connection,
   including `net/http` (the capstone), and including the *next* protocol layer.

If a future maintainer is tempted to "helpfully" add multi-client support to the server: don't. The
single-connection model is what keeps the injected-channel abstraction clean (one socket = one
logical channel = a trivial `lossy` wrapper, not a routing table). Multi-client is a different
exercise.

Two injection seams, by design and symmetric: the harness injects the channel *into* the transport,
and the transport's *factory* is injected *into* the harness. The factory split (client carries the
target address; server learns the peer from the first SYN) encodes the one real client/server
asymmetry — exactly `connect()` vs `accept()`. Everything after the handshake is symmetric, which is
why there is a single `net.Conn` type and not two.

---

## Deferred work and how it connects to the wire format

The wire format was front-loaded with forward-compat hooks specifically so the deferred work is *not*
a format break:

- **`DataOffset`** (header-length byte) was reserved so TCP-style **options** can be added later.
  Payload always starts at `DataOffset`, so old readers still find it.
- **RTT estimation / Karn's algorithm** wants a **Timestamp** option → goes in the option space that
  `DataOffset` enables.
- **Congestion control** may want a **SACK** option (same mechanism) and/or the **ECN** flag bits
  (the CWR/ECE bits TCP added) → the `Flags` byte has bits 4–7 free.
- **RST** (abnormal close) already has a reserved flag bit (bit 3). v1 signals failure implicitly via
  timeout; wiring up real RST is a small, self-contained later addition that pairs with the
  `net.Conn` error-vs-`io.EOF` distinction.

So the order of attack for extensions is: RST (cheap, reserved) → Timestamp+RTT estimation (the
biggest benchmark win under loss) → congestion control (largest, may use SACK/ECN).

---

## The capstone is the motivation engine

The HTTP benchmark is deliberately sequenced so the assignee *first* measures that their protocol is
slower and *explains why*, and *then* (Stage 4) is invited to implement the very optimizations their
analysis pointed to, re-running the same benchmark to measure the win. The "why is it slower"
analysis is not a dead end — it is the spec for the optimization work. Keep that framing if revising
the assignment: analysis → hypothesis → implement → re-measure.

---

## The next exercise: a message-oriented protocol on top

This byte-stream transport is the substrate for a *follow-up* exercise: a higher-level
**message-oriented** protocol layered on top of the `net.Conn`, exactly as a real message protocol
sits on TCP. That is why:

- We stayed a pure byte stream here (no framing, no PSH-style message semantics) — framing belongs to
  the layer above.
- We chose `net.Conn` output — the message layer consumes it like any normal protocol consumes TCP.
- Lifecycle callbacks (`on_connected` / `on_data`) were deliberately kept *out* of v1. Those are a
  higher-level framework idiom and belong to the message layer, not the byte-stream transport.

When building that layer, the message-framing question (length-prefix vs. delimiter vs. self-
describing header) is the first fork — and note it will want its *own* injected-dependency and
testing story, mirroring this one.
