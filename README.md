# reliable-udp

Scaffolding for an exercise: **build a reliable, ordered, connection-oriented byte-stream transport
on top of UDP** — a TCP-like protocol you implement yourself, with a deliberate focus on the failure
cases (loss, reordering, duplication, delay).

Module path: `github.com/chvojkav/reliable-udp`

## Documents (read in this order)

1. **`docs/SPEC.md`** — the protocol specification. Source of truth: interfaces, the `net.Conn` /
   `net.PacketConn` model, requirements R1–R8, and the wire format.
2. **`docs/SETUP.md`** — how to source the code (fork / `go mod replace`), run client+server, use
   Wireshark, and set up the capstone benchmark.
3. **`docs/ASSIGNMENT.md`** — the staged assignment (handshake → adversarial channel → HTTP
   benchmark capstone → optional optimization extension).
4. **`docs/CONTEXT.md`** — notes-to-self: deferred work and the planned message-oriented layer.

## What's provided vs. what you build

You **implement the transport** (a `net.Conn` plus client/server factories) and **design your own
adversarial test scenarios**. Everything else is scaffolding — interfaces, mains, the `frame` codec
(opt-out), the `lossy` test wrapper, the `httpx` listener adapter, the `bench` harness, and the
Wireshark dissector (`wireshark/reliable_udp.lua`). See `docs/SETUP.md` §3.

## Key design choices

- Transport **consumes** an injected `net.PacketConn` (never owns the socket) → swappable for a lossy
  test channel.
- Transport **implements** `net.Conn` → drop-in for `net/http` (the capstone) and for the future
  message-oriented layer.
- Single connection per instance, byte-stream, fixed-timeout retransmission in v1; RTT estimation and
  congestion control are deferred stretch goals the capstone is designed to motivate.
