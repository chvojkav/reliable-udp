// Package bench is a reusable HTTP load driver for the reliable-UDP capstone
// benchmark (docs/ASSIGNMENT.md Stage 3). It drives load against any base URL
// and does not depend on a concrete transport — you point it at whatever is
// listening.
//
// # Intended runs (2×2 matrix)
//
// The assignment compares two transports (real TCP, custom UDP) under two
// channel conditions (clean, lossy). Run the same handler over both transports
// and apply tc netem loss at the OS level for a fair comparison:
//
//  1. Start the same http.Handler over TCP:
//       http.ListenAndServe(":8080", handler)
//     and over the custom transport (via httpx.NewListener):
//       ln, _ := httpx.NewListener(pc, myServerFactory)
//       http.Serve(ln, handler)
//
//  2. Run bench against each — clean channel:
//       bench --url http://127.0.0.1:8080 --mode throughput --label "TCP clean"
//       bench --url http://127.0.0.1:9000 --mode throughput --label "UDP clean"
//
//  3. Apply OS-level loss (applies equally to both transports — see SETUP.md §7.1):
//       sudo tc qdisc add dev lo root netem loss 5%
//
//  4. Re-run both:
//       bench --url http://127.0.0.1:8080 --mode throughput --label "TCP lossy"
//       bench --url http://127.0.0.1:9000 --mode throughput --label "UDP lossy"
//
//  5. Compare the 2×2 result table and answer the analysis questions in
//     ASSIGNMENT.md Stage 3.
//
// Keep-alive must be labelled explicitly (on vs. off) because it dramatically
// changes per-request cost — disabling it forces a fresh handshake per request
// and isolates connection-setup overhead from data-transfer overhead.
package bench

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mode selects the workload profile.
type Mode string

const (
	ModeThroughput Mode = "throughput" // large payloads, measures bytes/sec
	ModeLatency    Mode = "latency"    // small requests, measures tail latency
)

// DefaultPayloadSize returns the recommended response size for a mode.
func DefaultPayloadSize(m Mode) int {
	if m == ModeThroughput {
		return 64 * 1024 // 64 KB — stresses the window and retransmission logic
	}
	return 128 // 128 B — keeps requests tiny so latency dominates
}

// Config describes a single load run.
type Config struct {
	// BaseURL is the HTTP base URL to benchmark (e.g. "http://127.0.0.1:9000").
	BaseURL string

	// Mode selects the workload profile.
	Mode Mode

	// Concurrency is the number of parallel goroutines issuing requests.
	Concurrency int

	// Requests is the total number of requests to issue. When zero, Duration
	// is used instead.
	Requests int

	// Duration is how long to run. Used only when Requests == 0.
	Duration time.Duration

	// PayloadSize is the expected response body size in bytes (used to
	// compute bytes/sec throughput). For the built-in Handler this is also
	// the actual response size.
	PayloadSize int

	// KeepAlive controls HTTP keep-alive. Disable to force a new connection
	// per request, which isolates connection-setup cost from transfer cost.
	KeepAlive bool

	// Label is printed in the results block for identification.
	Label string
}

// Result holds the metrics from one completed run.
type Result struct {
	Label         string
	Mode          Mode
	KeepAlive     bool
	TotalRequests int
	Errors        int
	Elapsed       time.Duration
	RPS           float64 // requests per second
	ThroughputBPS float64 // bytes per second (response bytes only)
	MeanLatency   time.Duration
	P50           time.Duration
	P90           time.Duration
	P99           time.Duration
}

// Format returns a human-readable multi-line result block.
func (r Result) Format() string {
	ka := "on"
	if !r.KeepAlive {
		ka = "off"
	}
	label := r.Label
	if label == "" {
		label = string(r.Mode)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "=== %s (mode=%s keepalive=%s) ===\n", label, r.Mode, ka)
	fmt.Fprintf(&b, "  requests:   %d (%d errors)\n", r.TotalRequests, r.Errors)
	fmt.Fprintf(&b, "  elapsed:    %s\n", r.Elapsed.Round(time.Millisecond))
	fmt.Fprintf(&b, "  rps:        %.1f req/s\n", r.RPS)
	fmt.Fprintf(&b, "  throughput: %.1f KB/s\n", r.ThroughputBPS/1024)
	fmt.Fprintf(&b, "  latency:    mean=%-8s p50=%-8s p90=%-8s p99=%s\n",
		r.MeanLatency.Round(time.Microsecond),
		r.P50.Round(time.Microsecond),
		r.P90.Round(time.Microsecond),
		r.P99.Round(time.Microsecond),
	)
	return b.String()
}

// CSVHeader returns the CSV header line.
func CSVHeader() string {
	return "label,mode,keepalive,requests,errors,elapsed_ms,rps,throughput_bps,mean_us,p50_us,p90_us,p99_us"
}

// CSV returns a single CSV data line for this result.
func (r Result) CSV() string {
	ka := "on"
	if !r.KeepAlive {
		ka = "off"
	}
	return fmt.Sprintf("%s,%s,%s,%d,%d,%.1f,%.2f,%.0f,%d,%d,%d,%d",
		r.Label, r.Mode, ka,
		r.TotalRequests, r.Errors,
		float64(r.Elapsed.Milliseconds()),
		r.RPS, r.ThroughputBPS,
		r.MeanLatency.Microseconds(),
		r.P50.Microseconds(),
		r.P90.Microseconds(),
		r.P99.Microseconds(),
	)
}

// Run drives load according to cfg and returns the collected metrics.
// It respects ctx cancellation.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.Requests == 0 && cfg.Duration == 0 {
		return Result{}, fmt.Errorf("bench: one of Requests or Duration must be non-zero")
	}

	client := buildClient(cfg)
	url := strings.TrimRight(cfg.BaseURL, "/")

	// work is a closed buffered channel acting as a token pool for Requests
	// mode. Workers receive one token per request; when the pool is exhausted
	// the channel is drained and workers exit naturally. Nil in Duration mode.
	var work chan struct{}
	if cfg.Requests > 0 {
		work = make(chan struct{}, cfg.Requests)
		for i := 0; i < cfg.Requests; i++ {
			work <- struct{}{}
		}
		close(work)
	}

	runCtx, cancel := context.WithCancel(ctx)
	if cfg.Requests == 0 {
		// Duration mode: run until timeout or parent cancellation.
		runCtx, cancel = context.WithTimeout(ctx, cfg.Duration)
	}
	defer cancel()

	type workerResult struct {
		latencies []time.Duration
		bytes     int64
		errors    int
	}

	workerResults := make([]workerResult, cfg.Concurrency)
	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			var wr workerResult
			// Always save results on exit, regardless of which exit path fires.
			defer func() { workerResults[id] = wr; wg.Done() }()

			for {
				if work != nil {
					// Requests mode: claim one token or stop.
					select {
					case _, ok := <-work:
						if !ok {
							return
						}
					case <-runCtx.Done():
						return
					}
				} else {
					// Duration mode: stop when context expires.
					select {
					case <-runCtx.Done():
						return
					default:
					}
				}

				t0 := time.Now()
				n, err := doRequest(runCtx, client, url)
				lat := time.Since(t0)

				if err != nil {
					// Ignore errors caused by context cancellation at run end.
					if runCtx.Err() == nil {
						wr.errors++
					}
				} else {
					wr.latencies = append(wr.latencies, lat)
					wr.bytes += n
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Merge worker results.
	var allLatencies []time.Duration
	var totalBytes int64
	var totalErrors int
	for _, wr := range workerResults {
		allLatencies = append(allLatencies, wr.latencies...)
		totalBytes += wr.bytes
		totalErrors += wr.errors
	}

	sort.Slice(allLatencies, func(i, j int) bool { return allLatencies[i] < allLatencies[j] })

	res := Result{
		Label:         cfg.Label,
		Mode:          cfg.Mode,
		KeepAlive:     cfg.KeepAlive,
		TotalRequests: len(allLatencies) + totalErrors,
		Errors:        totalErrors,
		Elapsed:       elapsed,
	}
	if elapsed > 0 {
		res.RPS = float64(res.TotalRequests) / elapsed.Seconds()
		res.ThroughputBPS = float64(totalBytes) / elapsed.Seconds()
	}
	if len(allLatencies) > 0 {
		res.MeanLatency = meanDuration(allLatencies)
		res.P50 = percentile(allLatencies, 0.50)
		res.P90 = percentile(allLatencies, 0.90)
		res.P99 = percentile(allLatencies, 0.99)
	}
	return res, nil
}

func buildClient(cfg Config) *http.Client {
	tr := &http.Transport{
		DisableKeepAlives:   !cfg.KeepAlive,
		MaxIdleConnsPerHost: cfg.Concurrency,
	}
	return &http.Client{Transport: tr}
}

func doRequest(ctx context.Context, client *http.Client, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return n, err
	}
	if resp.StatusCode != http.StatusOK {
		return n, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return n, nil
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Round(float64(len(sorted)-1) * p))
	return sorted[idx]
}

func meanDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	var sum int64
	for _, d := range ds {
		sum += int64(d)
	}
	return time.Duration(sum / int64(len(ds)))
}
