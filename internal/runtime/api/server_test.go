package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestServerThreadTurnAndEvents(t *testing.T) {
	store, err := NewThreadStore(filepath.Join(t.TempDir(), "runtime.json"))
	if err != nil {
		t.Fatalf("NewThreadStore: %v", err)
	}
	server, err := NewServer(store, func(_ context.Context, input string) (string, error) {
		return "echo: " + input, nil
	}, "127.0.0.1:0", "test-key")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	addr, err := server.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer server.Close(context.Background())

	client := &http.Client{Timeout: 2 * time.Second}
	threadReq, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/threads", nil)
	threadReq.Header.Set("Authorization", "Bearer test-key")
	threadResp, err := client.Do(threadReq)
	if err != nil {
		t.Fatalf("create thread request: %v", err)
	}
	defer threadResp.Body.Close()
	if threadResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(threadResp.Body)
		t.Fatalf("create thread status=%d body=%s", threadResp.StatusCode, body)
	}
	var threadBody struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(threadResp.Body).Decode(&threadBody); err != nil {
		t.Fatalf("decode thread: %v", err)
	}

	turnReq, _ := http.NewRequest(
		http.MethodPost,
		"http://"+addr+"/v1/threads/"+threadBody.ID+"/turns",
		strings.NewReader(`{"input":"hello"}`),
	)
	turnReq.Header.Set("X-TeaCLI-API-Key", "test-key")
	turnResp, err := client.Do(turnReq)
	if err != nil {
		t.Fatalf("turn request: %v", err)
	}
	turnResp.Body.Close()
	if turnResp.StatusCode != http.StatusAccepted {
		t.Fatalf("turn status=%d", turnResp.StatusCode)
	}

	var eventsBody string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		eventsReq, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/threads/"+threadBody.ID+"/events", nil)
		eventsReq.Header.Set("Authorization", "Bearer test-key")
		eventsResp, err := client.Do(eventsReq)
		if err != nil {
			t.Fatalf("events request: %v", err)
		}
		raw, _ := io.ReadAll(eventsResp.Body)
		eventsResp.Body.Close()
		eventsBody = string(raw)
		if strings.Contains(eventsBody, "turn.completed") && strings.Contains(eventsBody, "echo: hello") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("events did not include completed turn:\n%s", eventsBody)
}

func TestServerEventsWaitStreamsFutureEvent(t *testing.T) {
	store, err := NewThreadStore(filepath.Join(t.TempDir(), "runtime.json"))
	if err != nil {
		t.Fatalf("NewThreadStore: %v", err)
	}
	server, err := NewServer(store, func(_ context.Context, input string) (string, error) {
		return "echo: " + input, nil
	}, "127.0.0.1:0", "test-key")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	addr, err := server.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer server.Close(context.Background())

	threadID, err := store.CreateThread()
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	existing := store.Events(threadID, 0)
	after := existing[len(existing)-1].ID

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	eventsReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/v1/threads/"+threadID+"/events?wait=1&after="+strconvFormat(after), nil)
	eventsReq.Header.Set("Authorization", "Bearer test-key")

	done := make(chan string, 1)
	go func() {
		resp, err := http.DefaultClient.Do(eventsReq)
		if err != nil {
			done <- err.Error()
			return
		}
		defer resp.Body.Close()
		raw := make([]byte, 512)
		n, _ := resp.Body.Read(raw)
		done <- string(raw[:n])
	}()

	time.Sleep(50 * time.Millisecond)
	if _, err := store.AppendEvent(threadID, "message.delta", `{"content":"streamed"}`); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	select {
	case body := <-done:
		if !strings.Contains(body, "message.delta") || !strings.Contains(body, "streamed") {
			t.Fatalf("unexpected streamed body:\n%s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events wait did not stream future event")
	}
}

func TestServerCancelTurnCancelsRunner(t *testing.T) {
	store, err := NewThreadStore(filepath.Join(t.TempDir(), "runtime.json"))
	if err != nil {
		t.Fatalf("NewThreadStore: %v", err)
	}
	started := make(chan struct{})
	server, err := NewServer(store, func(ctx context.Context, input string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}, "127.0.0.1:0", "test-key")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	addr, err := server.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer server.Close(context.Background())

	threadID, err := store.CreateThread()
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	turnReq, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/threads/"+threadID+"/turns", strings.NewReader(`{"input":"wait"}`))
	turnReq.Header.Set("Authorization", "Bearer test-key")
	turnResp, err := http.DefaultClient.Do(turnReq)
	if err != nil {
		t.Fatalf("turn request: %v", err)
	}
	defer turnResp.Body.Close()
	var turnBody struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(turnResp.Body).Decode(&turnBody); err != nil {
		t.Fatalf("decode turn: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}

	cancelReq, _ := http.NewRequest(http.MethodDelete, "http://"+addr+"/v1/threads/"+threadID+"/turns/"+turnBody.ID, nil)
	cancelReq.Header.Set("Authorization", "Bearer test-key")
	cancelResp, err := http.DefaultClient.Do(cancelReq)
	if err != nil {
		t.Fatalf("cancel request: %v", err)
	}
	cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel status=%d", cancelResp.StatusCode)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events := store.Events(threadID, 0)
		var sawCanceled bool
		for _, event := range events {
			if event.Type == "turn.canceled" {
				sawCanceled = true
			}
		}
		if sawCanceled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("turn.canceled event not recorded")
}

func strconvFormat(value int64) string {
	return strconv.FormatInt(value, 10)
}
