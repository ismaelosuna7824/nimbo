// Package events fans model.Event values out to server-sent-event clients.
package events

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Message is a published event, already encoded as JSON.
type Message struct {
	Type       string
	WorkflowID string
	Data       []byte // JSON model.Event
}

const defaultBuffer = 256

// Hub implements model.Publisher. Publish never blocks: a subscriber whose
// buffer is full is dropped (its channel is closed) so one slow client
// cannot stall publishers or other clients.
type Hub struct {
	mu        sync.Mutex
	subs      map[*subscriber]struct{}
	buffer    int
	heartbeat time.Duration
}

type subscriber struct {
	workflow string
	ch       chan Message
}

// NewHub returns a hub with a 256-message buffer per subscriber and a 15s
// SSE heartbeat.
func NewHub() *Hub {
	return &Hub{subs: map[*subscriber]struct{}{}, buffer: defaultBuffer, heartbeat: 15 * time.Second}
}

// Publish encodes e once and delivers it to every matching subscriber.
// Subscribers filtered by workflow receive that workflow's events and
// events without a workflow ID.
func (h *Hub) Publish(e model.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) == 0 {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		slog.Error("encode event", "type", e.Type, "err", err)
		return
	}
	msg := Message{Type: e.Type, WorkflowID: e.WorkflowID, Data: data}
	for s := range h.subs {
		if s.workflow != "" && e.WorkflowID != "" && s.workflow != e.WorkflowID {
			continue
		}
		select {
		case s.ch <- msg:
		default:
			delete(h.subs, s)
			close(s.ch)
		}
	}
}

// Subscribe registers a subscriber ("" = all workflows). The channel is
// closed when the subscriber is dropped for being slow or after cancel.
func (h *Hub) Subscribe(workflow string) (<-chan Message, func()) {
	s := &subscriber{workflow: workflow, ch: make(chan Message, h.buffer)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[s]; ok {
			delete(h.subs, s)
			close(s.ch)
		}
	}
	return s.ch, cancel
}

// ServeHTTP streams events as SSE. Query parameter "workflow" filters by
// workflow ID.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	ch, cancel := h.Subscribe(r.URL.Query().Get("workflow"))
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 2000\n: connected\n\n"); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		case msg, ok := <-ch:
			if !ok {
				return // dropped as slow; the client reconnects
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Type, msg.Data); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
