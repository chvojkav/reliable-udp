// Package reliableudp defines the public contracts for a reliable, ordered,
// connection-oriented byte-stream transport over UDP (SPEC.md §3–4).
package reliableudp

import "net"

// ClientFactory establishes a connection to a known server address, analogous
// to connect(2). It blocks until the three-way handshake completes or fails;
// on failure a non-nil error is returned (SPEC.md §3.1: "like connect() being
// refused"). The returned net.Conn is fully connected: its RemoteAddr() reports
// the server address passed as server.
//
// The factory never owns conn; the caller retains responsibility for closing it.
type ClientFactory func(conn net.PacketConn, server net.Addr) (net.Conn, error)

// ServerFactory waits for an incoming connection on conn, analogous to
// accept(2). It blocks until the three-way handshake completes or fails; on
// failure a non-nil error is returned (SPEC.md §3.1: "like connect() being
// refused"). The peer address is learned from the first SYN — no address
// argument is required. The returned net.Conn is fully connected: its
// RemoteAddr() reports the peer that initiated the handshake.
//
// The factory never owns conn; the caller retains responsibility for closing it.
type ServerFactory func(conn net.PacketConn) (net.Conn, error)
