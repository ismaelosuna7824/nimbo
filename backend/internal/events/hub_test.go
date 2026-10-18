package events

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestSubscribeFiltersByWorkflow(t *testing.T) {
	h := NewHub()
	a, cancelA := h.Subscribe("wf-a")
	defer cancelA()
	all, cancelAll := h.Subscribe("")
	defer cancelAll()

	h.Publish(model.Event{Type: model.EventRunStatus, WorkflowID: "wf-b"})
	h.Publish(model.Event{Type: model.EventWorkflowUpdated, WorkflowID: "wf-a"})
	h.Publish(model.Event{Type: model.EventWorkflowDeleted}) // global

	gotA := drain(a)
	if len(gotA) != 2 || gotA[0].WorkflowID != "wf-a" || gotA[1].Type != model.EventWorkflowDeleted {
		t.Fatalf("filtered subscriber got %+v", gotA)
	}
	if gotAll := drain(all); len(gotAll) != 3 {
		t.Fatalf("unfiltered subscriber got %d events, want 3", len(gotAll))
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	h := NewHub()
	h.buffer = 2
	slow, cancelSlow := h.Subscribe("")
	defer cancelSlow()
	fast, cancelFast := h.Subscribe("")
	defer cancelFast()

	done := make(chan struct{})
	go func() {
		for range 10 {
			h.Publish(model.Event{Type: model.EventRunLog, WorkflowID: "w"})
			<-fast // keep the fast subscriber drained
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}

	n := 0
	for range slow { // channel must be closed after the drop
		n++
	}
	if n != 2 {
		t.Fatalf("slow subscriber received %d buffered events, want 2", n)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) != 1 {
		t.Fatalf("hub has %d subscribers, want 1", len(h.subs))
	}
}

func TestServeHTTPStreamsSSE(t *testing.T) {
	h := NewHub()
	h.heartbeat = 20 * time.Millisecond
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?workflow=wf-a", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}

	go func() {
		for range 50 { // until the handler has subscribed
			h.Publish(model.Event{Type: model.EventWorkflowUpdated, WorkflowID: "wf-other"})
			h.Publish(model.Event{Type: model.EventWorkflowUpdated, WorkflowID: "wf-a", Data: map[string]string{"id": "wf-a"}})
			time.Sleep(10 * time.Millisecond)
		}
	}()

	sc := bufio.NewScanner(res.Body)
	var sawPing bool
	var event, data string
	for sc.Scan() && (event == "" || !sawPing) {
		line := sc.Text()
		switch {
		case line == ": ping":
			sawPing = true
		case strings.HasPrefix(line, "event: ") && event == "":
			event = strings.TrimPrefix(line, "event: ")
			sc.Scan()
			data = strings.TrimPrefix(sc.Text(), "data: ")
		}
	}
	if event != model.EventWorkflowUpdated {
		t.Fatalf("event %q", event)
	}
	var e model.Event
	if err := json.Unmarshal([]byte(data), &e); err != nil || e.WorkflowID != "wf-a" {
		t.Fatalf("data %q (%v)", data, err)
	}
	if !sawPing {
		t.Fatal("no heartbeat")
	}
}

func drain(ch <-chan Message) []Message {
	var out []Message
	for {
		select {
		case m := <-ch:
			out = append(out, m)
		default:
			return out
		}
	}
}
