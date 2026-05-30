// cmd/server opens a real UDP socket, hands it to the injected ServerFactory,
// and runs an echo workload over the returned net.Conn until the connection
// closes. The echo workload deliberately distinguishes io.EOF (clean close,
// SPEC.md R8) from a non-EOF error (crash/timeout) in its log output.
//
// Wire your transport by assigning transport.Server in transport/transport.go.
// See docs/SETUP.md §4 for the full workflow.
//
// Usage:
//
//	go run ./cmd/server [--listen 127.0.0.1:9000]
package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"

	"github.com/chvojkav/reliable-udp/transport"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:9000", "UDP address to listen on")
	flag.Parse()

	logger := log.New(os.Stdout, "[server] ", log.LstdFlags)
	transport.Logger = log.New(os.Stdout, "[transport] ", log.LstdFlags)

	conn, err := net.ListenPacket("udp", *listen)
	if err != nil {
		logger.Fatalf("listen %s: %v", *listen, err)
	}
	defer conn.Close()
	logger.Printf("listening on %s", conn.LocalAddr())

	logger.Printf("waiting for connection (calling ServerFactory)…")
	netConn, err := transport.Server(conn)
	if err != nil {
		logger.Fatalf("ServerFactory: %v", err)
	}
	defer netConn.Close()
	logger.Printf("connection established — remote %s", netConn.RemoteAddr())

	logger.Printf("starting echo loop")
	total, closeType, closeErr := echoUntilDone(netConn)
	switch closeType {
	case "eof":
		logger.Printf("connection closed cleanly (EOF) — echoed %d bytes total", total)
	case "error":
		logger.Printf("connection error after %d bytes: %v", total, closeErr)
	}
}

// echoUntilDone reads from conn, writes each chunk back, and returns when the
// connection ends. It returns the total bytes echoed, a string tag ("eof" or
// "error"), and the error (nil on EOF).
func echoUntilDone(conn net.Conn) (total int64, closeType string, err error) {
	buf := make([]byte, 32*1024)
	for {
		n, rerr := conn.Read(buf)
		if n > 0 {
			if _, werr := conn.Write(buf[:n]); werr != nil {
				return total, "error", werr
			}
			total += int64(n)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return total, "eof", nil
			}
			return total, "error", rerr
		}
	}
}
