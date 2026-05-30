package httpx_test

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/chvojkav/reliable-udp/httpx"
	reliableudp "github.com/chvojkav/reliable-udp"
)

// immediateFactory returns a net.Pipe end as the "connected" conn so tests
// never touch a real UDP socket or transport.
func immediateFactory(conn net.PacketConn) (net.Conn, error) {
	c, _ := net.Pipe()
	return c, nil
}

// errorFactory always fails, simulating a handshake refusal.
func errorFactory(conn net.PacketConn) (net.Conn, error) {
	return nil, errors.New("stub: handshake refused")
}

// udpConn opens a real loopback UDP socket.
func udpConn(t *testing.T) net.PacketConn {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	return pc
}

// newLn is a test helper that constructs a listener and registers cleanup.
func newLn(t *testing.T, factory reliableudp.ServerFactory) net.Listener {
	t.Helper()
	ln, err := httpx.NewListener(udpConn(t), factory)
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// TestAcceptReturnsConn verifies that the first Accept call returns a non-nil
// net.Conn with no error when the factory succeeds.
func TestAcceptReturnsConn(t *testing.T) {
	ln := newLn(t, immediateFactory)

	c, err := ln.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if c == nil {
		t.Fatal("Accept returned nil conn")
	}
	c.Close()
}

// TestAddrMatchesUnderlying verifies Addr() returns the PacketConn's local address.
func TestAddrMatchesUnderlying(t *testing.T) {
	pc := udpConn(t)
	ln, err := httpx.NewListener(pc, immediateFactory)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if ln.Addr().String() != pc.LocalAddr().String() {
		t.Errorf("Addr()=%q, want %q", ln.Addr(), pc.LocalAddr())
	}
}

// TestFactoryErrorPropagated verifies that a factory error is returned by Accept.
func TestFactoryErrorPropagated(t *testing.T) {
	ln := newLn(t, errorFactory)

	_, err := ln.Accept()
	if err == nil {
		t.Fatal("expected error from factory, got nil")
	}
}

// TestSecondAcceptBlocksUntilClose verifies the single-connection contract:
// the second Accept blocks, and Close unblocks it with a net.ErrClosed error.
func TestSecondAcceptBlocksUntilClose(t *testing.T) {
	ln := newLn(t, immediateFactory)

	// Consume the one connection.
	c, err := ln.Accept()
	if err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	c.Close()

	// Second Accept must block.
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := ln.Accept()
		ch <- result{conn, err}
	}()

	// Give the goroutine time to enter Accept and confirm it's blocking.
	select {
	case r := <-ch:
		t.Fatalf("second Accept returned early: conn=%v err=%v", r.conn, r.err)
	case <-time.After(30 * time.Millisecond):
		// good — it's blocking
	}

	// Close the listener; the blocked Accept must unblock promptly.
	ln.Close()

	select {
	case r := <-ch:
		if r.conn != nil {
			t.Error("expected nil conn after close")
		}
		if !errors.Is(r.err, net.ErrClosed) {
			t.Errorf("expected net.ErrClosed, got %v", r.err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second Accept did not unblock after Close")
	}
}

// TestCloseBeforeAccept verifies that Accept returns net.ErrClosed immediately
// if the listener was closed before Accept was called.
func TestCloseBeforeAccept(t *testing.T) {
	ln := newLn(t, immediateFactory)

	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := ln.Accept()
	if err == nil {
		t.Fatal("expected error after pre-close, got nil")
	}
	if !errors.Is(err, net.ErrClosed) {
		t.Errorf("expected net.ErrClosed, got %v", err)
	}
}

// TestCloseIdempotent verifies Close can be called multiple times without panic.
func TestCloseIdempotent(t *testing.T) {
	ln := newLn(t, immediateFactory)
	ln.Close()
	ln.Close() // must not panic
}

// TestFactoryCalledOnce verifies the factory is called at most once even when
// Accept is called concurrently.
func TestFactoryCalledOnce(t *testing.T) {
	calls := 0
	factory := func(conn net.PacketConn) (net.Conn, error) {
		calls++
		c, _ := net.Pipe()
		return c, nil
	}

	ln := newLn(t, factory)

	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result, 3)

	// Launch three concurrent Accept calls. Only one should complete the handshake.
	for i := 0; i < 3; i++ {
		go func() {
			c, err := ln.Accept()
			results <- result{c, err}
		}()
	}

	// Wait for one to return with a conn.
	var got int
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case r := <-results:
			if r.err == nil {
				got++
				if r.conn != nil {
					r.conn.Close()
				}
			}
		case <-timeout:
			goto done
		}
	}
done:
	ln.Close()
	// Drain any remaining.
	for i := 0; i < 2; i++ {
		select {
		case <-results:
		case <-time.After(100 * time.Millisecond):
		}
	}

	if calls != 1 {
		t.Errorf("factory called %d times, want 1", calls)
	}
	if got != 1 {
		t.Errorf("%d Accept calls returned a conn, want 1", got)
	}
}
