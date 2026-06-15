package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestThreadStorePersistsEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	store, err := NewThreadStore(path)
	if err != nil {
		t.Fatalf("NewThreadStore: %v", err)
	}

	threadID, err := store.CreateThread()
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if _, err := store.AppendEvent(threadID, "message.delta", `{"content":"hello"}`); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	reloaded, err := NewThreadStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	events := reloaded.Events(threadID, 0)
	if len(events) != 2 {
		t.Fatalf("expected thread.created + message.delta, got %d", len(events))
	}
	if !strings.Contains(events[1].Data, "hello") {
		t.Fatalf("unexpected event data: %+v", events[1])
	}
}

func TestThreadStoreWaitReturnsNewEvents(t *testing.T) {
	store, err := NewThreadStore(filepath.Join(t.TempDir(), "runtime.json"))
	if err != nil {
		t.Fatalf("NewThreadStore: %v", err)
	}
	threadID, err := store.CreateThread()
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	existing := store.Events(threadID, 0)
	if len(existing) == 0 {
		t.Fatal("expected thread.created event")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan []Event, 1)
	go func() {
		done <- store.Wait(ctx, threadID, existing[len(existing)-1].ID)
	}()

	if _, err := store.AppendEvent(threadID, "message.delta", `{"content":"later"}`); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	select {
	case events := <-done:
		if len(events) != 1 || !strings.Contains(events[0].Data, "later") {
			t.Fatalf("unexpected waited events: %+v", events)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after new event")
	}
}
