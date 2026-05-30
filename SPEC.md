# Reliable Transport over UDP — Specification

**Status:** v1 draft
**Module:** `github.com/chvojkav/reliable-udp`

---

## 1. Goal

Implement a reliable, ordered, connection-oriented, single-connection **byte-stream**
transport on top of UDP. The exercise targets the *failure cases* of an unreliable
datagram channel — loss, reordering, duplication, and delay — not just the happy path.

**Production-completeness is an explicit non-goal.** Decades of TCP edge cases, RFC errata,
and congestion-control tuning are out of scope. The value is in building the core mechanisms
and making them correct under an adversarial channel.

---

## 2. Design at a glance

| Decision | Choice | Rationale |
|---|---|---|
| Substrate | UDP, not raw IP | Reliability logic lives *above* the datagram layer; raw IP adds privilege/framing friction that teaches a different lesson. |
| Stream model | Byte stream | Faithful to TCP. A message-oriented protocol is the *next* exercise, layered on top. |
| Connections | Single per instance | Keeps the injected-channel abstraction clean; multi-client demux is out of scope by design. |
| Dependency injection | Transport receives an open `net.PacketConn` | Permits a `LossyChannel` wrapper for deterministic tests. Transport never owns the socket. |
| Output type | `net.Conn` | Maximum stdlib fidelity; the transport becomes a drop-in for anything expecting a connection (incl. `net/http`). |
| Concurrency | Goroutines + channels | Idiomatic Go; handshake runs synchronously in the factory, then receive/timer goroutines spin up. |
| Retransmission (v1) | Fixed timeout | Simplest correct starting point. RTT estimation is a deferred stretch goal. |
| Congestion control | Deferred | Separable from flow control and substantially harder. |

---

## 3. Dependency injection model

The transport **never creates or owns a UDP socket.** It receives an already-open,
socket-like object satisfying `net.PacketConn` (the Go stdlib datagram interface):

```go
type PacketConn interface {
    ReadFrom(p []byte) (n int, addr net.Addr, err error)
    WriteTo(p []byte, addr net.Addr) (n int, err error)
    Close() error
    SetReadDeadline(t time.Time) error
    // ... and the rest of net.PacketConn
}
```

A real `*net.UDPConn` satisfies this. So does the test-only `LossyChannel` wrapper. Neither
the transport nor the code above it can tell the difference. **The transport must be correct
even when the channel is adversarial** (drops, reorders, duplicates, delays).

Receive timeouts are expressed the Go way: set a deadline with `SetReadDeadline`, then
`ReadFrom` returns a timeout error (check via `net.Error.Timeout()` / `os.ErrDeadlineExceeded`)
when it expires. There is no `recv(timeout)` parameter. The retransmission loop is built on
deadlines.

### 3.1 Two factories, one connection type

The asymmetry between client and server is **entirely in setup**, and zero in data transfer —
exactly as in the BSD sockets API (`connect()` vs `listen()`/`accept()`, after which both ends
hold an identical connected socket). This is encoded as two factory func types that both
return a fully-connected `net.Conn`:

```go
// Address known up front, like connect().
type ClientFactory func(conn net.PacketConn, server net.Addr) (net.Conn, error)

// Peer learned from the first SYN, like accept(); no address argument.
type ServerFactory func(conn net.PacketConn) (net.Conn, error)
```

Both factories **block until the handshake completes** (or fails, returning a non-nil error,
like `connect()` being refused). The returned connection's `RemoteAddr()` reports the peer.

---

## 4. Connection interface (`net.Conn`)

The transport implements the full `net.Conn` interface:

```go
type Conn interface {
    Read(b []byte) (n int, err error)   // pull received bytes; returns io.EOF on clean close
    Write(b []byte) (n int, err error)  // queue bytes into the send stream
    Close() error                        // orderly teardown (sends FIN)
    LocalAddr() net.Addr
    RemoteAddr() net.Addr
    SetDeadline(t time.Time) error
    SetReadDeadline(t time.Time) error
    SetWriteDeadline(t time.Time) error
}
```

Semantics that matter, all faithful to TCP/Go conventions:

- **`Read` is blocking and byte-stream.** It returns whatever bytes are available in order,
  not framed messages.
- **Clean end-of-stream is `io.EOF`** from `Read`, returned once the peer has closed (sent FIN)
  *and* all buffered data has been drained. This is distinct from a non-EOF error, which means
  the connection **failed** (e.g. timed out with no orderly close). This return-value
  distinction is how requirement R8 (orderly teardown vs. crash) is satisfied.
- **`Write` accepts bytes into the send buffer** and is subject to flow control (it may block
  when the window is full).

---

## 5. Functional requirements

| # | Requirement |
|---|---|
| R1 | **Handshake.** Three-way SYN / SYN-ACK / ACK. Both sides agree on initial sequence numbers and confirm liveness. |
| R2 | **Reliable delivery.** Every byte the application writes is eventually delivered to the peer application, or the connection fails cleanly. No silent loss. |
| R3 | **Ordered delivery.** Bytes are handed to the receiving application in send order, regardless of arrival order. |
| R4 | **Duplicate suppression.** A datagram delivered twice by the channel must not be delivered twice to the application. |
| R5 | **Retransmission.** Lost segments are detected (timeout in v1) and resent. |
| R6 | **Cumulative acknowledgment.** The receiver reports the next expected byte ("I have everything below this"). |
| R7 | **Flow control.** A sliding window bounds in-flight unacknowledged data so a fast sender cannot overrun a slow receiver. Fixed window in v1, but the wire field is live from day one. |
| R8 | **Orderly teardown.** A clean close (FIN exchange) is distinguishable from a crash (timeout). See §4 (`io.EOF` vs error). |

---

## 6. Deferred (explicit stretch goals — NOT v1)

- **RTT-estimated adaptive timeout** (Karn's algorithm). Needs the *Timestamp* option (§7.4).
- **Congestion control** (slow start, congestion avoidance, fast retransmit / fast recovery).
  May use the *SACK* option and the ECN flag bits.
- **Full RST handling** (abnormal close). The flag bit is reserved (§7.3); v1 may signal failure
  implicitly via timeout.
- **Message-oriented protocol** layered on top of this byte stream (the planned next exercise).

See `CONTEXT.md` for how these connect to the wire format and the benchmark capstone.

---

## 7. Wire format

All multi-byte fields are **big-endian (network byte order)**.

### 7.1 Base header — fixed 16 bytes

```
Offset  Size  Field
0       4     SeqNum       (uint32) byte offset of the first payload byte in this segment
4       4     AckNum       (uint32) next expected byte, cumulative ("everything below received")
8       2     Window       (uint16) bytes the receiver can accept beyond AckNum
10      1     Flags        (uint8)  see §7.3
11      1     DataOffset   (uint8)  header length in bytes; == 16 in v1, > 16 means options present
12      2     PayloadLen   (uint16) length in bytes of the payload following the header
14      2     Reserved     (uint16) zero in v1; reserved for version / small fixed fields
------------------
16 bytes base header.
Options (when present) occupy bytes [16, DataOffset).
Payload occupies bytes [DataOffset, DataOffset + PayloadLen).
```

### 7.2 Field notes

- **SeqNum / AckNum count bytes, not packets** (byte-stream semantics, like TCP). `SeqNum` is the
  byte offset of this segment's first payload byte. `AckNum` is the next byte the sender of *this*
  packet expects to receive.
- **AckNum is only meaningful when the ACK flag is set.**
- **Window** is receiver-relative: how many bytes beyond `AckNum` the receiver is currently willing
  to accept. Fixed in v1; making it dynamic later is a *behavior* change, not a *format* change.
- **DataOffset** is a plain byte count (not TCP's 32-bit-word scaling, chosen for clarity). In v1 it
  is always 16 and receivers should assert that. It exists now so that adding options later
  (Timestamp, SACK) is *not* a breaking format change: payload always starts at `DataOffset`.
- **PayloadLen** is technically derivable from the UDP datagram length minus `DataOffset`. It is
  carried explicitly anyway: self-describing header + a cheap truncation sanity check.
- **Reserved** must be zero in v1; receivers should assert zero so future use is unambiguous.

### 7.3 Flags (the `Flags` byte)

| Bit | Mask | Name | v1 status |
|---|---|---|---|
| 0 | 0x01 | SYN | Active — synchronize sequence numbers; opens the connection. |
| 1 | 0x02 | ACK | Active — the `AckNum` field is valid. |
| 2 | 0x04 | FIN | Active — sender is finished sending; begins orderly close. |
| 3 | 0x08 | RST | **Reserved** — abnormal close. Defined now; full handling is a stretch goal. |
| 4–7 | — | — | Free for future flags. |

### 7.4 SYN / FIN consume sequence space

Like TCP, **SYN and FIN each consume exactly one byte of sequence space** even though they carry
no payload. This is what lets them be acknowledged reliably using the *exact same* cumulative-ACK
machinery as data bytes. Concretely:

- A SYN with `SeqNum = X` is acknowledged by `AckNum = X + 1`.
- A FIN with `SeqNum = Y` is acknowledged by `AckNum = Y + 1`.

Getting this subtly wrong (treating SYN/FIN as consuming zero bytes) is a classic error; implement
and test it deliberately.

### 7.5 Sequence number width and initial value

- **32-bit sequence numbers**, faithful to TCP. They wrap at 4 GiB.
- **Wraparound is acknowledged but unhandled in v1.** A realistic exercise will never transfer 4 GiB
  on one connection. Handling it correctly requires TCP's serial-number (modular) arithmetic —
  a worthwhile bonus, not a v1 requirement.
- **Initial sequence number (ISN) = 0 in v1** for simplicity (single connection, no reincarnation).
  TCP randomizes the ISN for security and to avoid stale-segment confusion across connection
  reincarnations; randomizing here is one line and a good thing to understand, but not required.

---

## 8. Out of scope (deliberately dropped TCP features)

- **Source/destination ports** — UDP already carries them; do not duplicate.
- **Checksum** — UDP already checksums the datagram. We rely on it. (Corruption is *not* in the v1
  failure-mode list; loss/reorder/dup/delay are. If corruption testing is later desired, the
  `LossyChannel` could flip bits and a checksum field would then be warranted.)
- **Options in v1** — the header is fixed at 16 bytes. `DataOffset` reserves the ability to add them
  without a format break.
- **URG / PSH** — URG is effectively deprecated and a known misfeature; PSH is a buffering hint with
  little to teach here.
- **ECN bits (CWR/ECE), NS** — related to congestion control; relevant only to that deferred work.
