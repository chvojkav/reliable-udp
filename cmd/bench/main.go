// cmd/bench is the command-line front-end for the reliable-UDP benchmark
// harness. It drives HTTP load against any base URL and prints comparable
// throughput / latency numbers.
//
// # Typical workflow (see also bench package doc and docs/SETUP.md §7)
//
//	# Terminal 1 — TCP baseline
//	go run ./cmd/server --listen 127.0.0.1:8080   # or your own http.ListenAndServe
//
//	# Terminal 2 — custom transport
//	go run ./cmd/server --listen 127.0.0.1:9000
//
//	# Terminal 3 — benchmark
//	go run ./cmd/bench --url http://127.0.0.1:8080 --label "TCP clean" --mode throughput
//	go run ./cmd/bench --url http://127.0.0.1:9000 --label "UDP clean" --mode throughput
//	sudo tc qdisc add dev lo root netem loss 5%
//	go run ./cmd/bench --url http://127.0.0.1:8080 --label "TCP lossy" --mode throughput
//	go run ./cmd/bench --url http://127.0.0.1:9000 --label "UDP lossy" --mode throughput
//	sudo tc qdisc del dev lo root
//
// If --url is empty, a built-in test handler is started locally and used as
// the target — useful for checking that the harness itself is working.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/chvojkav/reliable-udp/bench"
)

func main() {
	url         := flag.String("url", "", "base URL to benchmark (empty = built-in test handler)")
	modeFlag    := flag.String("mode", "latency", "workload mode: throughput or latency")
	concurrency := flag.Int("concurrency", 0, "parallel workers (0 = mode default: 1 for latency, 4 for throughput)")
	requests    := flag.Int("requests", 0, "total requests (0 = use --duration)")
	duration    := flag.Duration("duration", 0, "run duration, e.g. 10s (used when --requests=0)")
	payloadSize := flag.Int("payload-size", 0, "response body size in bytes (0 = mode default)")
	keepAlive   := flag.String("keepalive", "on", "HTTP keep-alive: on or off")
	label       := flag.String("label", "", "label printed in results (e.g. 'TCP clean')")
	csvOut      := flag.Bool("csv", false, "print CSV header + row after the results block")
	flag.Parse()

	mode := bench.Mode(strings.ToLower(*modeFlag))
	if mode != bench.ModeThroughput && mode != bench.ModeLatency {
		log.Fatalf("--mode must be 'throughput' or 'latency', got %q", *modeFlag)
	}

	ka := strings.ToLower(*keepAlive)
	if ka != "on" && ka != "off" {
		log.Fatalf("--keepalive must be 'on' or 'off', got %q", *keepAlive)
	}
	keepAliveOn := ka == "on"

	// Apply mode defaults for unset flags.
	if *concurrency == 0 {
		if mode == bench.ModeThroughput {
			*concurrency = 4
		} else {
			*concurrency = 1
		}
	}
	if *requests == 0 && *duration == 0 {
		*requests = 500
	}
	if *payloadSize == 0 {
		*payloadSize = bench.DefaultPayloadSize(mode)
	}
	if *label == "" {
		*label = fmt.Sprintf("%s keepalive=%s", mode, ka)
	}

	// Start built-in server if no URL provided.
	target := *url
	var testSrv *httptest.Server
	if target == "" {
		testSrv = httptest.NewServer(bench.Handler(*payloadSize))
		target = testSrv.URL
		log.Printf("no --url given; started built-in test handler at %s (payload=%d bytes)", target, *payloadSize)
	}

	cfg := bench.Config{
		BaseURL:     target,
		Mode:        mode,
		Concurrency: *concurrency,
		Requests:    *requests,
		Duration:    *duration,
		PayloadSize: *payloadSize,
		KeepAlive:   keepAliveOn,
		Label:       *label,
	}

	log.Printf("starting: mode=%s concurrency=%d payload=%d keepalive=%s target=%s",
		mode, *concurrency, *payloadSize, ka, target)

	res, err := bench.Run(context.Background(), cfg)

	if testSrv != nil {
		testSrv.Close()
	}

	if err != nil {
		log.Fatalf("bench: %v", err)
	}

	fmt.Println(res.Format())

	if *csvOut {
		fmt.Fprintln(os.Stdout, bench.CSVHeader())
		fmt.Fprintln(os.Stdout, res.CSV())
	}

	// Exit non-zero when the error rate is too high (>10%) so CI can detect failure.
	if res.TotalRequests > 0 {
		errRate := float64(res.Errors) / float64(res.TotalRequests)
		if errRate > 0.10 {
			log.Printf("error rate %.1f%% exceeds 10%%", errRate*100)
			os.Exit(1)
		}
	}
}

// ensure net/http is used (it is, via bench package; this import is for cmd-level clarity).
var _ = http.MethodGet
