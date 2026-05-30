// Package transport is the single injection point where the assignee wires in
// their concrete ClientFactory and ServerFactory implementations.
//
// # How to plug in your transport (Option A — fork)
//
// Replace the stub assignments below with your real factories:
//
//	func init() {
//	    transport.Client = mypackage.NewClient
//	    transport.Server = mypackage.NewServer
//	}
//
// Or assign directly in this file:
//
//	var Client reliableudp.ClientFactory = mypackage.NewClient
//	var Server reliableudp.ServerFactory = mypackage.NewServer
//
// # How to plug in your transport (Option B — go mod replace)
//
// In your own module, import this package and assign in an init() before
// calling the mains. See docs/SETUP.md §2 for the go mod replace workflow.
//
// # Logging hook
//
// Set Logger before calling any factory if you want your transport to emit
// diagnostic output (sequence numbers, ACKs, retransmissions). The real
// transport should read this var and write to it; the stub ignores it.
package transport

import (
	"errors"
	"log"
	"net"

	reliableudp "github.com/chvojkav/reliable-udp"
)

// Logger is passed to the real transport for diagnostic output. The mains set
// this to a prefixed logger before calling any factory; the transport writes
// sequence numbers, ACKs, retransmissions, etc. through it. The stub ignores
// it.
var Logger = log.Default()

// Client is the ClientFactory used by cmd/client.
//
// TODO(assignee): replace the stub with your real ClientFactory, e.g.:
//
//	transport.Client = mytransport.NewClient
var Client reliableudp.ClientFactory = stubClient

// Server is the ServerFactory used by cmd/server.
//
// TODO(assignee): replace the stub with your real ServerFactory, e.g.:
//
//	transport.Server = mytransport.NewServer
var Server reliableudp.ServerFactory = stubServer

func stubClient(_ net.PacketConn, _ net.Addr) (net.Conn, error) {
	return nil, errors.New("transport: not implemented — assign transport.Client to your ClientFactory (see transport/transport.go)")
}

func stubServer(_ net.PacketConn) (net.Conn, error) {
	return nil, errors.New("transport: not implemented — assign transport.Server to your ServerFactory (see transport/transport.go)")
}
