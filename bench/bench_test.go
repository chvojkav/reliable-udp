package bench_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chvojkav/reliable-udp/bench"
)

// TestRunSaneNumbers runs the driver against an in-process httptest.Server and
// verifies that the result fields are internally consistent and non-zero.
func TestRunSaneNumbers(t *testing.T) {
	const (
		payload     = 1024
		requests    = 50
		concurrency = 4
	)

	srv := httptest.NewServer(bench.Handler(payload))
	defer srv.Close()

	cfg := bench.Config{
		BaseURL:     srv.URL,
		Mode:        bench.ModeLatency,
		Concurrency: concurrency,
		Requests:    requests,
		PayloadSize: payload,
		KeepAlive:   true,
		Label:       "self-test",
	}

	res, err := bench.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// All requests must complete (no errors against a local httptest server).
	if res.Errors != 0 {
		t.Errorf("got %d errors, want 0", res.Errors)
	}
	if res.TotalRequests != requests {
		t.Errorf("TotalRequests=%d, want %d", res.TotalRequests, requests)
	}

	// RPS must be positive.
	if res.RPS <= 0 {
		t.Errorf("RPS=%f, want >0", res.RPS)
	}

	// Throughput must reflect the payload.
	if res.ThroughputBPS <= 0 {
		t.Errorf("ThroughputBPS=%f, want >0", res.ThroughputBPS)
	}

	// Latency values must be positive and ordered p50 <= p90 <= p99.
	if res.MeanLatency <= 0 {
		t.Errorf("MeanLatency=%v, want >0", res.MeanLatency)
	}
	if res.P50 > res.P90 {
		t.Errorf("P50=%v > P90=%v", res.P50, res.P90)
	}
	if res.P90 > res.P99 {
		t.Errorf("P90=%v > P99=%v", res.P90, res.P99)
	}

	// Sanity: an in-process HTTP server should respond in well under 1 second.
	if res.P99 > time.Second {
		t.Errorf("P99=%v, suspiciously high for loopback", res.P99)
	}

	t.Logf("\n%s", res.Format())
	t.Logf("CSV: %s", res.CSV())
}

// TestRunDurationMode verifies the duration-based stop condition works.
func TestRunDurationMode(t *testing.T) {
	srv := httptest.NewServer(bench.Handler(64))
	defer srv.Close()

	cfg := bench.Config{
		BaseURL:     srv.URL,
		Mode:        bench.ModeLatency,
		Concurrency: 2,
		Duration:    150 * time.Millisecond,
		PayloadSize: 64,
		KeepAlive:   true,
		Label:       "duration-mode",
	}

	res, err := bench.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TotalRequests == 0 {
		t.Error("expected at least one request in 150ms, got 0")
	}
	if res.Elapsed < 100*time.Millisecond {
		t.Errorf("Elapsed=%v, suspiciously short", res.Elapsed)
	}
}

// TestKeepAliveOff verifies the driver runs without panics when keep-alive is disabled.
func TestKeepAliveOff(t *testing.T) {
	srv := httptest.NewServer(bench.Handler(64))
	defer srv.Close()

	cfg := bench.Config{
		BaseURL:     srv.URL,
		Mode:        bench.ModeLatency,
		Concurrency: 2,
		Requests:    10,
		PayloadSize: 64,
		KeepAlive:   false,
		Label:       "keepalive-off",
	}

	res, err := bench.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TotalRequests != 10 {
		t.Errorf("TotalRequests=%d, want 10", res.TotalRequests)
	}
}

// TestCSVFormat verifies the CSV output has the expected number of fields.
func TestCSVFormat(t *testing.T) {
	srv := httptest.NewServer(bench.Handler(64))
	defer srv.Close()

	cfg := bench.Config{
		BaseURL:     srv.URL,
		Concurrency: 1,
		Requests:    5,
		PayloadSize: 64,
		KeepAlive:   true,
		Label:       "csv-test",
	}
	res, err := bench.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	header := bench.CSVHeader()
	row := res.CSV()

	hFields := len(splitCSV(header))
	rFields := len(splitCSV(row))
	if hFields != rFields {
		t.Errorf("CSV header has %d fields but data row has %d fields\nheader: %s\nrow:    %s",
			hFields, rFields, header, row)
	}
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
