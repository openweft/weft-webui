// diagnoses_sse.go — live stream of Diagnoses to the browser. The
// huma endpoint at GET /api/diagnoses returns the snapshot ; this
// handler serves GET /api/diagnoses/stream as Server-Sent Events
// (same EventSource convention the SPA uses for /api/events).
//
// The cache is the single source of truth ; the SSE handler is a
// thin pump : Subscribe → forward each Diagnosis as one SSE event.
// On reconnect the browser pulls a fresh snapshot via the huma
// endpoint before re-subscribing.
//
// Admin-only ; the Infra portal mux registers this handler, the
// User / Tenant portals don't.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/openweft/weft-webui/internal/diagnoses"
)

// handleDiagnosesStream is the SSE bridge.
func handleDiagnosesStream(cache *diagnoses.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Mandatory SSE headers. Match /api/events conventions ;
		// the SPA's reusable EventSource client expects them.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		if cache == nil {
			// No cache wired (offline mode) ; send a one-shot comment
			// so the SPA doesn't think the stream is broken and stop
			// reconnecting. EventSource interprets ": text" as a
			// keepalive comment.
			fmt.Fprintf(w, ": diagnoses cache offline\n\n")
			flusher.Flush()
			<-r.Context().Done()
			return
		}

		ch := cache.Subscribe()
		defer cache.Unsubscribe(ch)

		fmt.Fprintf(w, ": ready\n\n")
		flusher.Flush()

		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ping.C:
				// EventSource silently drops idle connections after
				// ~30s on some proxies ; a comment is a free keepalive.
				fmt.Fprintf(w, ": ping\n\n")
				flusher.Flush()
			case d, ok := <-ch:
				if !ok {
					// Cache closed (server shutdown).
					return
				}
				body, err := json.Marshal(convert(d))
				if err != nil {
					// Not fatal ; skip this message, keep the stream open.
					continue
				}
				// One SSE event per Diagnosis. The event name
				// ("diagnosis") lets the SPA addEventListener
				// selectively if it wants to multiplex other event
				// types on the same stream in V0.2.
				fmt.Fprintf(w, "event: diagnosis\ndata: %s\n\n", body)
				flusher.Flush()
			}
		}
	}
}
