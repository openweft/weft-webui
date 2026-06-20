package server

// api_events_sse.go bridges the agent's WatchEvents gRPC stream into
// a Server-Sent Events feed the dashboard can consume directly. SSE
// is the right shape : long-lived, one-way (server→client), uses a
// standard EventSource on the frontend, survives proxies that block
// websockets.
//
// Wire shape :
//   GET /api/events/stream
//     → Content-Type: text/event-stream
//     → text/event-stream frames, one per platform event :
//         event: <kind>
//         data: { "subject": "...", "project_uuid": "...", "meta": {...} }
//         id: <unix-ns>
//
//   The frontend opens `new EventSource('/api/events/stream')` and
//   wires onmessage. Reconnect on drop is the browser's default
//   behaviour ; the server doesn't checkpoint — clients should treat
//   missed events as "go re-list the affected resource".

import (
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
)

// EventFrame is the over-the-wire SSE payload — the flat shape the
// dashboard's EventSource consumer expects. Mirrors what
// wclient.WatchEvents emits per event ; we just project the map
// into typed fields for nicer JSON consumer ergonomics.
type EventFrame struct {
	Kind    string            `json:"kind"`
	Subject string            `json:"subject,omitempty"`
	Project string            `json:"project,omitempty"`
	TS      string            `json:"ts,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

type watchEventsQuery struct {
	KindPrefix string `query:"kind_prefix" doc:"Optional comma-separated kind prefixes filter (e.g. 'vm.,host.')."`
	Project    string `query:"project" doc:"Optional project UUID filter."`
	Subject    string `query:"subject" doc:"Optional subject filter (e.g. a VM UUID)."`
}

func mountWatchEventsSSE(api huma.API, _ Scope) {
	sse.Register(api, huma.Operation{
		OperationID: "watch-events-sse",
		Method:      http.MethodGet,
		Path:        "/api/events/stream",
		Summary:     "Server-Sent Events stream of platform events",
		Description: "Bridges the agent's WatchEvents gRPC stream. Consumers : `new EventSource('/api/events/stream?kind_prefix=vm.')`. Reconnect via the browser's default backoff ; missed events should trigger a re-list.",
		Tags:        []string{"events"},
	}, map[string]any{
		"event": &EventFrame{},
	}, func(ctx context.Context, in *watchEventsQuery, send sse.Sender) {
		if live == nil {
			_ = send.Data(&EventFrame{Kind: "control.unavailable", Subject: "no live wclient wired"})
			return
		}
		var prefixes []string
		if in.KindPrefix != "" {
			prefixes = splitCSV(in.KindPrefix)
		}
		stream, err := live.WatchEvents(ctx, prefixes, in.Project, in.Subject)
		if err != nil {
			_ = send.Data(&EventFrame{Kind: "control.error", Subject: fmt.Sprintf("watch: %v", err)})
			return
		}
		defer stream.Close()
		// Initial hello so the browser's EventSource flips to "open"
		// state before the first real event lands.
		_ = send.Data(&EventFrame{Kind: "control.hello", Subject: "stream open"})
		for {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-stream.Errors:
				if !ok {
					return
				}
				if err != nil {
					_ = send.Data(&EventFrame{Kind: "control.error", Subject: err.Error()})
				}
				return
			case ev, ok := <-stream.Events:
				if !ok {
					return
				}
				frame := &EventFrame{
					Kind:    asString(ev["kind"]),
					Subject: asString(ev["subject"]),
					Project: asString(ev["project"]),
					TS:      asString(ev["ts"]),
				}
				if m, ok := ev["meta"].(map[string]string); ok {
					frame.Meta = m
				}
				if err := send.Data(frame); err != nil {
					return
				}
			}
		}
	})
}

func splitCSV(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if start < i {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
