package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Runner func(ctx context.Context, input string) (string, error)

type Server struct {
	store   *ThreadStore
	runner  Runner
	apiKey  string
	server  *http.Server
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func ConfiguredAPIKey() string {
	return strings.TrimSpace(getenvFirst("TEACLI_RUNTIME_API_KEY", "PAICLI_RUNTIME_API_KEY"))
}

func NewServer(store *ThreadStore, runner Runner, addr string, apiKey string) (*Server, error) {
	if store == nil {
		return nil, fmt.Errorf("runtime thread store is nil")
	}
	if runner == nil {
		return nil, fmt.Errorf("runtime runner is nil")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("runtime API requires TEACLI_RUNTIME_API_KEY")
	}
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1:0"
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{store: store, runner: runner, apiKey: apiKey, ctx: ctx, cancel: cancel, running: make(map[string]context.CancelFunc)}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/threads", s.handleThreads)
	mux.HandleFunc("/v1/threads/", s.handleThreadPath)
	s.server = &http.Server{Addr: addr, Handler: mux}
	return s, nil
}

func (s *Server) Start() (string, error) {
	listener, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return "", err
	}
	addr := listener.Addr().String()
	go func() {
		_ = s.server.Serve(listener)
	}()
	return addr, nil
}

func (s *Server) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.cancel()
	return s.server.Shutdown(ctx)
}

func (s *Server) handleThreads(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/threads" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	id, err := s.store.CreateThread()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "object": "thread"})
}

func (s *Server) handleThreadPath(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	threadID, suffix, turnID := parseThreadPath(r.URL.Path)
	if threadID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if !s.store.Exists(threadID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thread_not_found"})
		return
	}
	switch {
	case r.Method == http.MethodPost && suffix == "turns":
		s.handleTurn(w, r, threadID)
	case r.Method == http.MethodDelete && suffix == "turns" && turnID != "":
		s.handleCancelTurn(w, r, threadID, turnID)
	case r.Method == http.MethodGet && suffix == "events":
		s.handleEvents(w, r, threadID)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	}
}

func (s *Server) handleTurn(w http.ResponseWriter, r *http.Request, threadID string) {
	var body struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input := strings.TrimSpace(body.Input)
	if input == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "input_required"})
		return
	}
	turnID := "turn_" + strconv.FormatInt(time.Now().UnixNano(), 16)
	_ = s.appendJSONEvent(threadID, "turn.started", map[string]string{"turn_id": turnID, "input": input})
	go s.runTurn(s.ctx, threadID, turnID, input)
	writeJSON(w, http.StatusAccepted, map[string]string{"id": turnID, "object": "turn", "status": "running"})
}

func (s *Server) handleCancelTurn(w http.ResponseWriter, _ *http.Request, threadID, turnID string) {
	if s.cancelTurn(turnID) {
		_ = s.appendJSONEvent(threadID, "turn.cancel_requested", map[string]string{"turn_id": turnID})
		writeJSON(w, http.StatusAccepted, map[string]string{"id": turnID, "object": "turn", "status": "canceling"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "turn_not_running"})
}

func (s *Server) runTurn(parent context.Context, threadID, turnID, input string) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	s.registerTurn(turnID, cancel)
	defer s.unregisterTurn(turnID)
	defer cancel()

	result, err := s.runner(ctx, input)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			_ = s.appendJSONEvent(threadID, "turn.canceled", map[string]string{"turn_id": turnID, "status": "canceled"})
			return
		}
		_ = s.appendJSONEvent(threadID, "turn.failed", map[string]string{"turn_id": turnID, "error": err.Error()})
		return
	}
	if ctx.Err() != nil {
		_ = s.appendJSONEvent(threadID, "turn.canceled", map[string]string{"turn_id": turnID, "status": "canceled"})
		return
	}
	_ = s.appendJSONEvent(threadID, "message.delta", map[string]string{"turn_id": turnID, "content": result})
	_ = s.appendJSONEvent(threadID, "turn.completed", map[string]string{"turn_id": turnID, "status": "completed"})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, threadID string) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	wait := parseBool(r.URL.Query().Get("wait"))
	events := s.store.Events(threadID, after)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	writeEvents := func(events []Event) int64 {
		for _, event := range events {
			fmt.Fprintf(w, "id: %d\n", event.ID)
			fmt.Fprintf(w, "event: %s\n", event.Type)
			fmt.Fprintf(w, "data: %s\n\n", event.Data)
			after = event.ID
		}
		if flusher != nil {
			flusher.Flush()
		}
		return after
	}

	writeEvents(events)
	if !wait {
		return
	}

	for r.Context().Err() == nil {
		waitCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		events = s.store.Wait(waitCtx, threadID, after)
		cancel()
		if len(events) == 0 {
			fmt.Fprint(w, ": heartbeat\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			continue
		}
		writeEvents(events)
	}
}

func (s *Server) registerTurn(turnID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running[turnID] = cancel
}

func (s *Server) unregisterTurn(turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, turnID)
}

func (s *Server) cancelTurn(turnID string) bool {
	s.mu.Lock()
	cancel := s.running[turnID]
	s.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Server) authorized(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	direct := r.Header.Get("X-TeaCLI-API-Key")
	legacy := r.Header.Get("X-PaiCLI-API-Key")
	return auth == "Bearer "+s.apiKey || direct == s.apiKey || legacy == s.apiKey
}

func (s *Server) appendJSONEvent(threadID, eventType string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.store.AppendEvent(threadID, eventType, string(raw))
	return err
}

func parseThreadPath(path string) (threadID, suffix, turnID string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || len(parts) > 5 || parts[0] != "v1" || parts[1] != "threads" {
		return "", "", ""
	}
	if len(parts) == 5 && parts[3] != "turns" {
		return "", "", ""
	}
	if len(parts) == 5 {
		return parts[2], parts[3], parts[4]
	}
	return parts[2], parts[3], ""
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func getenvFirst(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}
