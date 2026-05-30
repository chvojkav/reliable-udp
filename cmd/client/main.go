// cmd/client opens a real UDP socket on an ephemeral port, hands it to the
// injected ClientFactory, sends a message or file over the returned net.Conn,
// reads the server's echo, verifies it matches, then closes cleanly.
//
// Wire your transport by assigning transport.Client in transport/transport.go.
// See docs/SETUP.md §4 for the full workflow.
//
// Usage:
//
//	go run ./cmd/client [--connect 127.0.0.1:9000] [--message "hello"] [--send-file path]
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"github.com/chvojkav/reliable-udp/transport"
)

func main() {
	connect  := flag.String("connect", "127.0.0.1:9000", "server UDP address")
	message  := flag.String("message", "hello, reliable UDP!", "message to send (ignored if --send-file is set)")
	sendFile := flag.String("send-file", "", "path to file to send instead of --message")
	flag.Parse()

	logger := log.New(os.Stdout, "[client] ", log.LstdFlags)
	transport.Logger = log.New(os.Stdout, "[transport] ", log.LstdFlags)

	// Resolve server address.
	serverAddr, err := net.ResolveUDPAddr("udp", *connect)
	if err != nil {
		logger.Fatalf("resolve %s: %v", *connect, err)
	}

	// Open an ephemeral local UDP socket.
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		logger.Fatalf("open local socket: %v", err)
	}
	defer conn.Close()
	logger.Printf("local socket %s", conn.LocalAddr())

	// Establish the connection via the injected factory.
	logger.Printf("connecting to %s (calling ClientFactory)…", serverAddr)
	netConn, err := transport.Client(conn, serverAddr)
	if err != nil {
		logger.Fatalf("ClientFactory: %v", err)
	}
	defer netConn.Close()
	logger.Printf("connected — local %s, remote %s", netConn.LocalAddr(), netConn.RemoteAddr())

	// Build the payload.
	payload, err := buildPayload(*sendFile, *message)
	if err != nil {
		logger.Fatalf("build payload: %v", err)
	}
	logger.Printf("sending %d bytes…", len(payload))

	if _, err := netConn.Write(payload); err != nil {
		logger.Fatalf("write: %v", err)
	}
	logger.Printf("sent; reading echo…")

	echo, err := readExact(netConn, len(payload))
	if err != nil {
		logger.Fatalf("read echo: %v", err)
	}

	if string(echo) != string(payload) {
		logger.Fatalf("echo mismatch: sent %d bytes, got back %d bytes (first diff at byte %d)",
			len(payload), len(echo), firstDiff(payload, echo))
	}
	logger.Printf("echo verified OK (%d bytes)", len(echo))

	logger.Printf("closing…")
	if err := netConn.Close(); err != nil {
		logger.Printf("close: %v", err)
	}
	logger.Printf("done")
}

// buildPayload returns the bytes to send: file contents if sendFile is set,
// otherwise the message string.
func buildPayload(sendFile, message string) ([]byte, error) {
	if sendFile != "" {
		data, err := os.ReadFile(sendFile)
		if err != nil {
			return nil, fmt.Errorf("read file %s: %w", sendFile, err)
		}
		return data, nil
	}
	return []byte(message), nil
}

// readExact reads exactly n bytes from r, returning an error if the connection
// closes before n bytes arrive.
func readExact(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

// firstDiff returns the index of the first differing byte between a and b, or
// min(len(a), len(b)) if the shorter slice is a prefix of the longer.
func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
