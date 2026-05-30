# Setup & Workflow

**Module:** `github.com/chvojkav/reliable-udp`

This repo provides the *scaffolding* for the exercise: interfaces, the client/server mains,
a framing codec, a test-only lossy channel, a Wireshark dissector, and a benchmark harness.
**You implement the transport** (the thing that satisfies `net.Conn`). The scaffolding never
touches your transport's internals — it only depends on the interfaces in `SPEC.md`.

---

## 1. Prerequisites

- **Go** (latest stable; the module targets the current release line).
- **Wireshark** (optional, for protocol inspection) with Lua support enabled (it is, in standard
  builds).
- **Linux with `tc`/`iproute2`** (optional, for the capstone benchmark's loss regime). On macOS
  the equivalent is `dnctl`/`pfctl`; on Windows use a Linux VM/WSL2.

---

## 2. Getting the code

This scaffolding lives at `github.com/chvojkav/reliable-udp`. **Do not implement your transport
inside this repo.** Instead, source the scaffolding from your *own* GitHub and add your transport
there, so your work is your own and the scaffolding stays a stable, shared baseline.

Two clean options:

### Option A — Fork (simplest)

1. Fork `github.com/chvojkav/reliable-udp` to your own account.
2. Clone your fork.
3. Implement your transport in a new package (e.g. `transport/`) that satisfies the interfaces.
4. Wire your factories into the provided mains.

### Option B — `go mod replace` (keeps scaffolding pristine)

Keep the scaffolding as an upstream dependency and override it locally while you develop:

```
// in your own module's go.mod
require github.com/chvojkav/reliable-udp vX.Y.Z
replace github.com/chvojkav/reliable-udp => ../reliable-udp
```

Implement your transport in your module; import the interfaces and mains from the dependency.

---

## 3. What is provided vs. what you implement

**Provided (do not need to write):**
- The interface definitions (`ClientFactory`, `ServerFactory`, the `net.PacketConn` / `net.Conn`
  contracts — see `SPEC.md`).
- `cmd/server` and `cmd/client` mains.
- `frame` — the wire-format codec (`Header` + `Marshal`/`Unmarshal`). **Opt-out:** see §6.
- `lossy` — the test-only `net.PacketConn` wrapper (drop/reorder/duplicate/delay, seedable) plus
  smoke tests proving the *wrapper itself* works.
- `httpx` — a `net.Listener` adapter so `net/http` runs over your transport (capstone).
- `bench` — the benchmark harness (capstone).
- `wireshark/reliable_udp.lua` — the protocol dissector.

**You implement:**
- The transport: a type satisfying `net.Conn`, plus a `ClientFactory` and `ServerFactory` that
  produce it. This is the assignment. See `ASSIGNMENT.md`.
- **Your own test scenarios** using the provided `lossy` wrapper. The wrapper is the *mechanism*;
  designing adversarial scenarios (pure loss, pure reorder, duplication, delay, and combinations)
  is part of the exercise.

---

## 4. Running it

Once your factories are wired into the mains:

```
# Terminal 1 — server
go run ./cmd/server --listen 127.0.0.1:9000

# Terminal 2 — client
go run ./cmd/client --connect 127.0.0.1:9000
```

The mains own the real `*net.UDPConn` and pass it to your factory as a `net.PacketConn`. Your
transport completes the handshake inside the factory and returns a connected `net.Conn`.

---

## 5. Wireshark: inspecting your protocol

Because your protocol rides over UDP, Wireshark sees UDP datagrams and will *not* dissect them as
TCP. The provided Lua dissector decodes your 16-byte header into Wireshark's packet tree.

1. Locate your Wireshark plugins directory: **Help → About Wireshark → Folders → "Personal Lua
   Plugins"** (or "Global Lua Plugins").
2. Copy `wireshark/reliable_udp.lua` into that directory.
3. Restart Wireshark, or **Analyze → Reload Lua Plugins** (Ctrl+Shift+L).
4. Capture on the loopback interface, filtering on your port:
   ```
   udp.port == 9000
   ```
5. The dissector registers on UDP port **9000** by default. If you use a different port, either
   change the `default_port` near the top of the Lua file, or right-click a packet →
   **Decode As… → RELUDP**.

You should see SeqNum, AckNum, Window, the decoded flag bits (SYN/ACK/FIN/RST), DataOffset, and
PayloadLen broken out per packet. Use this to *see* your handshake, retransmissions (duplicate
SeqNums), and ACK progression.

> The dissector is authored from the spec but has not been verified against a live capture in this
> environment — confirm it parses your first real handshake and adjust offsets if anything looks
> off. It mirrors `frame`'s layout; the two must agree.

---

## 6. Opting out of the provided framing codec

`frame` is provided so you can focus on protocol logic rather than byte-packing. **If you want the
wire-format exercise too**, you may delete/ignore `frame` and reimplement `Header` +
`Marshal`/`Unmarshal` yourself from `SPEC.md` §7.

- The provided round-trip test (`frame/frame_test.go`) is your oracle: a correct reimplementation
  passes it unchanged.
- Note that the **Wireshark dissector and all provided tooling assume the spec's exact layout** —
  so any reimplementation must match the bytes precisely, not just round-trip with itself.

---

## 7. Capstone: HTTP-over-your-protocol benchmark

The capstone runs the **unmodified** `net/http` server over (a) real TCP and (b) your transport,
then compares performance and explains the delta with Wireshark.

This works because your transport is a `net.Conn`: the provided `httpx` adapter wraps it as a
`net.Listener`, and `http.Serve` runs on top without modification. The only variable between the
two runs is the transport beneath HTTP — a controlled experiment.

### 7.1 Inducing loss fairly

Your in-code `lossy` wrapper only wraps *your* protocol's socket — you cannot use it to add loss to
the kernel's real TCP. To compare both transports under loss, induce loss at the OS level so it
applies to *both* equally.

On Linux, using `tc netem` on the loopback interface:

```
# Add 5% loss on loopback (affects ALL loopback traffic — TCP and your UDP alike)
sudo tc qdisc add dev lo root netem loss 5%

# Inspect
tc qdisc show dev lo

# Add reordering and delay too, if desired
sudo tc qdisc change dev lo root netem loss 5% delay 20ms reorder 25% 50%

# REMOVE when done (important — this affects all loopback traffic)
sudo tc qdisc del dev lo root
```

> `netem` on `lo` affects everything on loopback, including unrelated local services. Use a
> dedicated machine/VM, or bind your benchmark to a non-loopback interface if isolation matters.

### 7.2 What to measure

Run **both regimes** — clean channel and lossy channel — for both transports:

- **Clean:** the gap is dominated by userspace/syscall overhead (your per-packet `ReadFrom`/
  `WriteTo` cross the kernel boundary; real TCP runs *in* the kernel).
- **Lossy:** the gap now also reflects your fixed-timeout retransmission and absent congestion
  control vs. the kernel's tuned RTT estimation and congestion algorithms.

Measure both **throughput** (bulk transfer — exposes windowing) and **latency** (small requests —
exposes per-packet/RTT overhead). State keep-alive on/off explicitly; reusing one connection
isolates transport cost from connection-setup cost.

See `ASSIGNMENT.md` for the analysis questions, and `CONTEXT.md` for how the results motivate the
deferred RTT/congestion work.
