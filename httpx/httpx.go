// Package httpx adapts a single reliable-UDP connection into a net.Listener so
// that net/http can serve over the custom transport without modification. This
// is the capstone adapter described in docs/ASSIGNMENT.md Stage 3.
//
// # Usage
//
// With your transport:
//
//	pc, _ := net.ListenPacket("udp", "127.0.0.1:9000")
//	ln, _ := httpx.NewListener(pc, myServerFactory)
//	http.Serve(ln, myHandler)
//
// Over real TCP (the baseline for the benchmark):
//
//	http.ListenAndServe("127.0.0.1:9000", myHandler)
//
// The only variable between the two is the transport. Everything above it —
// the HTTP handler, the request parser, keep-alive logic — is identical.
//
// # Single-connection limitation
//
// This transport is single-connection per socket (SPEC.md §2: "Single per
// instance"). The returned Listener's Accept therefore works as follows:
//   - The first Accept call performs the handshake (calls the ServerFactory)
//     and returns the resulting net.Conn.
//   - All subsequent Accept calls block until Close is called, then return
//     a wrapped net.ErrClosed error.
//
// http.Serve calls Accept in a loop; that loop will serve one connection and
// then stall on the second Accept until the listener is closed — which is the
// correct behavior for a single-connection benchmark.
//
// # Race between Close and an in-progress handshake
//
// If Close is called while the first Accept is still inside the ServerFactory
// (i.e. the handshake is in progress), the factory runs to completion on the
// now-closed underlying PacketConn. The factory will observe the close as an
// error and return it; Accept propagates that error to the caller. This is
// acceptable for the benchmark use case where lifetime is caller-controlled.
package httpx

import (
	"fmt"
	"net"
	"sync"

	reliableudp "github.com/chvojkav/reliable-udp"
)

// NewListener wraps conn and factory into a net.Listener suitable for
// http.Serve. It returns immediately without performing any I/O; the handshake
// runs inside the first Accept call.
func NewListener(conn net.PacketConn, factory reliableudp.ServerFactory) (net.Listener, error) {
	return &listener{
		conn:    conn,
		factory: factory,
		done:    make(chan struct{}),
	}, nil
}

type listener struct {
	conn    net.PacketConn
	factory reliableudp.ServerFactory

	once      sync.Once  // ensures the factory is called at most once
	closeOnce sync.Once  // ensures done is closed exactly once
	done      chan struct{}
}

// Accept performs the handshake on the first call and returns the resulting
// net.Conn. All subsequent calls block until the listener is closed.
func (l *listener) Accept() (net.Conn, error) {
	// Fast-path: if the listener is already closed, don't start a handshake.
	select {
	case <-l.done:
		return nil, fmt.Errorf("httpx: Accept on closed listener: %w", net.ErrClosed)
	default:
	}

	var (
		c      net.Conn
		err    error
		didRun bool
	)
	l.once.Do(func() {
		didRun = true
		c, err = l.factory(l.conn)
	})
	if didRun {
		return c, err
	}

	// Single-connection: the handshake already happened (or failed). Block
	// until Close so http.Serve's Accept loop parks cleanly.
	<-l.done
	return nil, fmt.Errorf("httpx: listener closed: %w", net.ErrClosed)
}

// Close closes the listener and the underlying PacketConn. Any Accept calls
// that are blocked waiting for a second connection will unblock and return an
// error wrapping net.ErrClosed.
func (l *listener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.conn.Close()
}

// Addr returns the local address of the underlying PacketConn.
func (l *listener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

