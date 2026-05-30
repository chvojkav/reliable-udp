package bench

import (
	"bytes"
	"net/http"
)

// Handler returns a trivial http.Handler that responds with payloadSize bytes
// of repeated 0x61 ('a'). Use it for self-tests or quick standalone runs:
//
//	http.ListenAndServe(":8080", bench.Handler(64*1024))
//
// For the actual capstone benchmark, point cmd/bench at the handler the
// assignee also serves over the custom transport (so the handler is identical
// across TCP and UDP runs).
func Handler(payloadSize int) http.Handler {
	if payloadSize < 0 {
		payloadSize = 0
	}
	body := bytes.Repeat([]byte("a"), payloadSize)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
	})
}
