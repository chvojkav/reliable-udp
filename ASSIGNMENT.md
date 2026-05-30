# Assignment: Build a Reliable Transport over UDP

**Module:** `github.com/chvojkav/reliable-udp`
Read `SPEC.md` first — it is the source of truth. Read `SETUP.md` for how to get the code and run
things.

---

## What you are building

A reliable, ordered, connection-oriented, single-connection **byte-stream** transport on top of
UDP. Your transport must satisfy Go's `net.Conn`, and you provide a `ClientFactory` and
`ServerFactory` that produce it (see `SPEC.md` §3–§4). When done, your transport is a drop-in
`net.Conn` — which the capstone exploits to run a real HTTP server over it.

**The point of this exercise is the failure cases**, not the happy path. A version that works only
when no packet is ever lost has missed the entire lesson. Build against an adversarial channel from
the start.

---

## What you are given

See `SETUP.md` §3. In short: the interfaces, the mains, the `frame` codec (opt-out), the `lossy`
test wrapper (drop/reorder/duplicate/delay, seedable), the `httpx` listener adapter, the `bench`
harness, and the Wireshark dissector. **You write the transport and your own test scenarios.**

---

## Debugging investigation format

When you hit a non-obvious bug (and you will — reordering and duplicate-ACK behavior are subtle),
work it as a structured investigation rather than by guessing:

> **Hypothesis** (with confidence level) → **Why it's plausible** → **What to verify** → **Why this
> tells us something** → **Command + what it does** → **Expected output** (✅ supports / ❌
> contradicts / ❓ inconclusive) → **Concept sidebar** (optional)

Always form the expectation *before* interpreting results, and always have a "what if inconclusive"
path. The Wireshark dissector and your logging hook are your primary instruments — use them to
*see* sequence numbers, ACKs, and retransmissions rather than inferring them.

---

## Stage 1 — Handshake and the happy path

**Goal:** a connection that establishes and transfers data correctly over a *clean* channel.

- Implement the three-way handshake (SYN / SYN-ACK / ACK), ISN per `SPEC.md` §7.5.
- Implement `Read`/`Write`/`Close` against the send/receive buffers.
- Implement cumulative ACK and the basic sliding window (fixed size, `SPEC.md` §7).
- Remember **SYN and FIN consume sequence space** (`SPEC.md` §7.4) — get the ACK numbers right.
- Implement orderly teardown: a clean FIN exchange ends the stream with `io.EOF` on the peer's
  `Read`.

**Done when:** client and server transfer a multi-KB payload correctly over the real UDP socket,
and a clean close yields `io.EOF` (not an error) on the reader.

## Stage 2 — Survive an adversarial channel

**Goal:** correctness under loss, reordering, duplication, and delay.

- Wrap the injected socket with the provided `lossy` channel in your tests.
- Implement retransmission on timeout (**fixed timeout** — `SPEC.md` §5 R5). Detect loss and resend.
- Enforce ordered delivery (R3): buffer out-of-order segments, deliver in order.
- Enforce duplicate suppression (R4): a doubly-delivered datagram reaches the app once.
- Verify flow control (R7): a fast writer cannot overrun a slow reader; the window bounds in-flight
  data.

**Design your own test scenarios** with the `lossy` wrapper: pure loss, pure reordering, pure
duplication, delay, and combinations. Make runs reproducible by seeding the wrapper. The provided
smoke tests prove the *wrapper* works; the *protocol* scenarios are yours to design — that design
is part of the learning.

**Done when:** your transport transfers data correctly and reaches a clean close across each
failure mode individually and in combination, with reproducible (seeded) tests.

## Stage 3 — Capstone: HTTP-over-your-protocol benchmark

**Goal:** run the unmodified `net/http` server over both real TCP and your transport, compare
performance, and *explain the difference*.

- Use the provided `httpx` adapter to expose your transport as a `net.Listener`; serve the same
  `http.Handler` over it and over real TCP.
- Use the provided `bench` harness. Measure **throughput** (bulk transfer) and **latency** (small
  requests), with keep-alive explicitly on and off.
- Run **both regimes**: a clean channel, and an OS-induced lossy channel via `tc netem` (see
  `SETUP.md` §7.1) that applies equally to TCP and your protocol.
- Capture both in Wireshark. Compare your handshake to TCP's; identify your retransmissions and ACK
  progression.

### Analysis questions (answer these)

1. **Is your protocol slower? By how much, in each regime (clean vs. lossy, throughput vs.
   latency)?** Report numbers, not impressions.
2. **Why?** Attribute the gap to causes and estimate each one's contribution. Consider, at least:
   - userspace vs. kernel execution and per-packet syscall overhead (`ReadFrom`/`WriteTo` crossing
     the kernel boundary on every datagram);
   - your **fixed** retransmission timeout vs. the kernel's RTT-estimated adaptive timeout;
   - the absence of congestion control vs. TCP's slow start / congestion avoidance / fast recovery;
   - Go scheduling and buffer-copy costs.
3. **Where does the gap *widen* when you add loss, and which missing mechanism explains that?**

A slower result is the *expected and correct* outcome — real TCP runs in the kernel and embodies
decades of tuning. The lesson is explaining *why* and *how much*, not winning.

---

## Stage 4 (optional extension) — Close the gap

Once you have answered *why* your protocol is slower, the natural follow-up is to **implement the
optimizations your analysis identified** and re-run the benchmark to measure the improvement. This
turns the analysis into a hypothesis and tests it.

Candidate optimizations, each tied to a gap you likely measured in Stage 3:

- **RTT-estimated adaptive retransmission timeout** (replacing the fixed timeout), using Karn's
  algorithm. Requires adding the *Timestamp* option — `SPEC.md` reserved `DataOffset` exactly for
  this, so it is *not* a format break. Expect the biggest win in the lossy regime, where a
  too-short or too-long fixed timeout hurts most.
- **Congestion control** (slow start → congestion avoidance, plus fast retransmit / fast recovery
  on duplicate ACKs). May use the *SACK* option and/or the ECN flag bits. Expect gains under loss
  and on higher-latency/bulk transfers.
- **Reducing per-packet syscall overhead** (batching, larger segments) — addresses the
  clean-regime gap.

**Method:** for each optimization, state the hypothesis ("RTT estimation should reduce p99 latency
under 5% loss because…"), implement it, re-run the *same* benchmark, and report the measured delta
against your Stage 3 baseline. Back every claim with HTTP-benchmark numbers, not intuition.

See `CONTEXT.md` for how these extensions connect to the wire format and to the planned
message-oriented protocol layer.
