package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ThreadStore struct {
	path   string
	mu     sync.Mutex
	nextID int64

	threads map[string]time.Time
	events  []Event
	waiters map[string][]chan struct{}
}

type storeFile struct {
	NextID  int64             `json:"next_id"`
	Threads map[string]string `json:"threads"`
	Events  []Event           `json:"events"`
}

var threadCounter uint64

func NewThreadStore(path string) (*ThreadStore, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	store := &ThreadStore{
		path:    path,
		nextID:  1,
		threads: make(map[string]time.Time),
		waiters: make(map[string][]chan struct{}),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func DefaultPath() string {
	dir := strings.TrimSpace(os.Getenv("TEACLI_RUNTIME_DIR"))
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv("PAICLI_RUNTIME_DIR"))
	}
	if dir == "" {
		if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
			dir = filepath.Join(cfg, "teacli", "runtime")
		} else if home, err := os.UserHomeDir(); err == nil && home != "" {
			dir = filepath.Join(home, ".teacli", "runtime")
		} else {
			dir = filepath.Join(".", ".teacli", "runtime")
		}
	}
	return filepath.Join(dir, "runtime.json")
}

func (s *ThreadStore) CreateThread() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := "thread_" + randomHex(6)
	for {
		if _, exists := s.threads[id]; !exists {
			break
		}
		id = "thread_" + randomHex(6)
	}
	s.threads[id] = time.Now().UTC()
	if _, err := s.appendEventLocked(id, "thread.created", fmt.Sprintf(`{"thread_id":"%s"}`, id)); err != nil {
		return "", err
	}
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *ThreadStore) Exists(threadID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.threads[strings.TrimSpace(threadID)]
	return ok
}

func (s *ThreadStore) AppendEvent(threadID, eventType, data string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[threadID]; !ok {
		return 0, fmt.Errorf("runtime thread not found: %s", threadID)
	}
	id, err := s.appendEventLocked(threadID, eventType, data)
	if err != nil {
		return 0, err
	}
	err = s.saveLocked()
	s.notifyLocked(threadID)
	return id, err
}

func (s *ThreadStore) Events(threadID string, afterID int64) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for _, event := range s.events {
		if event.ThreadID == threadID && event.ID > afterID {
			out = append(out, event)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *ThreadStore) Wait(ctx context.Context, threadID string, afterID int64) []Event {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if events := s.Events(threadID, afterID); len(events) > 0 {
			return events
		}

		ch := s.subscribe(threadID)
		select {
		case <-ch:
		case <-ctx.Done():
			s.unsubscribe(threadID, ch)
			return nil
		}
	}
}

func (s *ThreadStore) subscribe(threadID string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan struct{})
	s.waiters[threadID] = append(s.waiters[threadID], ch)
	return ch
}

func (s *ThreadStore) unsubscribe(threadID string, ch chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiters := s.waiters[threadID]
	for i, waiter := range waiters {
		if waiter == ch {
			waiters = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(waiters) == 0 {
		delete(s.waiters, threadID)
		return
	}
	s.waiters[threadID] = waiters
}

func (s *ThreadStore) notifyLocked(threadID string) {
	waiters := s.waiters[threadID]
	delete(s.waiters, threadID)
	for _, waiter := range waiters {
		close(waiter)
	}
}

func (s *ThreadStore) appendEventLocked(threadID, eventType, data string) (int64, error) {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return 0, fmt.Errorf("runtime event type cannot be empty")
	}
	if strings.TrimSpace(data) == "" {
		data = "{}"
	}
	id := s.nextID
	s.nextID++
	s.events = append(s.events, Event{
		ID:        id,
		ThreadID:  threadID,
		Type:      eventType,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	})
	return id, nil
}

func (s *ThreadStore) load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var data storeFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	s.nextID = data.NextID
	if s.nextID <= 0 {
		s.nextID = 1
	}
	for id, rawTime := range data.Threads {
		created, err := time.Parse(time.RFC3339Nano, rawTime)
		if err == nil {
			s.threads[id] = created
		}
	}
	s.events = data.Events
	for _, event := range s.events {
		if event.ID >= s.nextID {
			s.nextID = event.ID + 1
		}
	}
	return nil
}

func (s *ThreadStore) saveLocked() error {
	threads := make(map[string]string, len(s.threads))
	for id, created := range s.threads {
		threads[id] = created.Format(time.RFC3339Nano)
	}
	raw, err := json.MarshalIndent(storeFile{NextID: s.nextID, Threads: threads, Events: s.events}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func randomHex(bytesLen int) string {
	n := atomic.AddUint64(&threadCounter, 1)
	return fmt.Sprintf("%012x", uint64(time.Now().UnixNano())^n)
}
