package lossy_test

import (
	"net"
	"testing"
	"time"

	"github.com/chvojkav/reliable-udp/lossy"
)

// udpPair opens two bound UDP sockets on loopback.
// The sender socket is what gets wrapped with a Channel; the receiver socket
// is used to read what actually arrived.
func udpPair(t *testing.T) (sender, receiver net.PacketConn) {
	t.Helper()
	r, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); r.Close() })
	return s, r
}

// readOne tries to read one datagram from conn within timeout.
// Returns nil if nothing arrives in time.
func readOne(conn net.PacketConn, timeout time.Duration) []byte {
	conn.SetReadDeadline(time.Now().Add(timeout)) //nolint:errcheck
	defer conn.SetReadDeadline(time.Time{})        //nolint:errcheck
	buf := make([]byte, 2048)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

// readN collects up to n datagrams, stopping as soon as a read times out.
func readN(conn net.PacketConn, n int, each time.Duration) [][]byte {
	var out [][]byte
	for i := 0; i < n; i++ {
		b := readOne(conn, each)
		if b == nil {
			break
		}
		out = append(out, b)
	}
	return out
}

// TestDropAll verifies DropRate=1.0 silently discards every datagram.
func TestDropAll(t *testing.T) {
	sender, receiver := udpPair(t)
	ch := lossy.New(sender, lossy.Config{DropRate: 1.0, Seed: 1})

	for i := 0; i < 5; i++ {
		if _, err := ch.WriteTo([]byte("hello"), receiver.LocalAddr()); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
	}

	if got := readOne(receiver, 50*time.Millisecond); got != nil {
		t.Errorf("expected nothing to arrive, got %q", got)
	}
}

// TestDropNone verifies DropRate=0.0 delivers every datagram.
func TestDropNone(t *testing.T) {
	sender, receiver := udpPair(t)
	ch := lossy.New(sender, lossy.Config{DropRate: 0.0, Seed: 1})

	const N = 5
	for i := 0; i < N; i++ {
		if _, err := ch.WriteTo([]byte("hello"), receiver.LocalAddr()); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
	}

	got := readN(receiver, N+1, 100*time.Millisecond)
	if len(got) != N {
		t.Errorf("got %d datagrams, want %d", len(got), N)
	}
}

// TestDuplicateAll verifies DuplicateRate=1.0 delivers each datagram exactly twice.
func TestDuplicateAll(t *testing.T) {
	sender, receiver := udpPair(t)
	ch := lossy.New(sender, lossy.Config{DuplicateRate: 1.0, Seed: 1})

	if _, err := ch.WriteTo([]byte("dup"), receiver.LocalAddr()); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	got := readN(receiver, 3, 100*time.Millisecond)
	if len(got) != 2 {
		t.Errorf("want 2 copies, got %d", len(got))
	}
}

// TestPassThrough verifies that a zero Config is fully transparent:
// bytes are identical and order is preserved.
func TestPassThrough(t *testing.T) {
	sender, receiver := udpPair(t)
	ch := lossy.New(sender, lossy.Config{}) // all zero

	msgs := [][]byte{[]byte("alpha"), []byte("beta"), []byte("gamma")}
	for _, m := range msgs {
		if _, err := ch.WriteTo(m, receiver.LocalAddr()); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
	}

	for i, want := range msgs {
		got := readOne(receiver, 100*time.Millisecond)
		if got == nil {
			t.Fatalf("msg[%d]: nothing arrived, want %q", i, want)
		}
		if string(got) != string(want) {
			t.Errorf("msg[%d]: got %q, want %q", i, got, want)
		}
	}

	if extra := readOne(receiver, 20*time.Millisecond); extra != nil {
		t.Errorf("unexpected extra datagram: %q", extra)
	}
}

// TestReproducibility verifies that the same Seed produces the same sequence
// of decisions across two independent Channel instances.
func TestReproducibility(t *testing.T) {
	const seed = 42
	const N = 30

	collectKinds := func() []lossy.EventKind {
		sender, receiver := udpPair(t)
		_ = receiver
		ch := lossy.New(sender, lossy.Config{
			DropRate:      0.3,
			DuplicateRate: 0.3,
			ReorderRate:   0.2,
			Seed:          seed,
		})
		var kinds []lossy.EventKind
		ch.Log = func(e lossy.Event) { kinds = append(kinds, e.Kind) }
		for i := 0; i < N; i++ {
			ch.WriteTo([]byte("x"), receiver.LocalAddr()) //nolint:errcheck
		}
		return kinds
	}

	run1 := collectKinds()
	run2 := collectKinds()

	if len(run1) == 0 {
		t.Fatal("no events recorded — is Log being called?")
	}
	if len(run1) != len(run2) {
		t.Fatalf("event count differs: run1=%d run2=%d", len(run1), len(run2))
	}
	for i := range run1 {
		if run1[i] != run2[i] {
			t.Errorf("event[%d]: run1=%v run2=%v", i, run1[i], run2[i])
		}
	}
}

// TestLogHook verifies the Log hook is called for every WriteTo and that drop
// events are reported correctly.
func TestLogHook(t *testing.T) {
	sender, receiver := udpPair(t)
	_ = receiver

	var events []lossy.Event
	ch := lossy.New(sender, lossy.Config{DropRate: 1.0, Seed: 7})
	ch.Log = func(e lossy.Event) { events = append(events, e) }

	const N = 4
	for i := 0; i < N; i++ {
		ch.WriteTo([]byte("log-test"), receiver.LocalAddr()) //nolint:errcheck
	}

	if len(events) != N {
		t.Fatalf("got %d events, want %d", len(events), N)
	}
	for i, e := range events {
		if e.Kind != lossy.EventDropped {
			t.Errorf("event[%d]: got %v, want dropped", i, e.Kind)
		}
		if e.Len != len("log-test") {
			t.Errorf("event[%d]: Len=%d, want %d", i, e.Len, len("log-test"))
		}
	}
}

// TestReorderActuallyReorders sends two datagrams where the first is always
// reordered (ReorderRate=1) and the second is not, then checks that the
// first datagram arrives after the second.
func TestReorderActuallyReorders(t *testing.T) {
	sender, receiver := udpPair(t)

	// ReorderRate=1 means every datagram is held. With two sends:
	//   WriteTo("first"):  reorder=true  → held="first",  nothing sent
	//   WriteTo("second"): reorder=false → sends "second", then flushes "first"
	// Receiver should see "second" then "first".
	ch := lossy.New(sender, lossy.Config{ReorderRate: 1.0, Seed: 1})

	ch.WriteTo([]byte("first"), receiver.LocalAddr())  //nolint:errcheck
	ch.WriteTo([]byte("second"), receiver.LocalAddr()) //nolint:errcheck

	got := readN(receiver, 3, 100*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("got %d datagrams, want 2", len(got))
	}
	if string(got[0]) != "second" || string(got[1]) != "first" {
		t.Errorf("order: got [%q, %q], want [\"second\", \"first\"]", got[0], got[1])
	}
}
