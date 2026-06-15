package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Runner func(ctx context.Context, prompt string) (string, error)

type Manager struct {
	path        string
	runner      Runner
	workerCount int

	mu      sync.Mutex
	cond    *sync.Cond
	tasks   map[string]*DurableTask
	running map[string]context.CancelFunc
	started bool
	closed  bool
	wg      sync.WaitGroup
}

type storeFile struct {
	Tasks []*DurableTask `json:"tasks"`
}

var idCounter uint64

func NewManager(path string, runner Runner, workerCount int) (*Manager, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	if runner == nil {
		return nil, fmt.Errorf("runtime task runner is nil")
	}
	if workerCount <= 0 {
		workerCount = WorkerCountFromEnv()
	}
	m := &Manager{
		path:        path,
		runner:      runner,
		workerCount: max(1, workerCount),
		tasks:       make(map[string]*DurableTask),
		running:     make(map[string]context.CancelFunc),
	}
	m.cond = sync.NewCond(&m.mu)
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func DefaultPath() string {
	dir := strings.TrimSpace(os.Getenv("TEACLI_TASK_DIR"))
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv("PAICLI_TASK_DIR"))
	}
	if dir == "" {
		if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
			dir = filepath.Join(cfg, "teacli", "tasks")
		} else if home, err := os.UserHomeDir(); err == nil && home != "" {
			dir = filepath.Join(home, ".teacli", "tasks")
		} else {
			dir = filepath.Join(".", ".teacli", "tasks")
		}
	}
	return filepath.Join(dir, "tasks.json")
}

func WorkerCountFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("TEACLI_TASK_WORKERS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("PAICLI_TASK_WORKERS"))
	}
	if raw == "" {
		return 2
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 2
	}
	return n
}

func (m *Manager) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.closed {
		return
	}
	m.started = true
	for i := 0; i < m.workerCount; i++ {
		m.wg.Add(1)
		go m.workerLoop()
	}
	m.cond.Broadcast()
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	for _, cancel := range m.running {
		cancel()
	}
	m.cond.Broadcast()
	m.mu.Unlock()

	m.wg.Wait()
	return nil
}

func (m *Manager) Enqueue(prompt string) (DurableTask, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return DurableTask{}, fmt.Errorf("task prompt cannot be empty")
	}
	now := time.Now().UTC()
	task := &DurableTask{
		ID:        "task_" + randomHex(6),
		Status:    StatusEnqueued,
		Prompt:    prompt,
		CreatedAt: now,
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return DurableTask{}, fmt.Errorf("task manager is closed")
	}
	for {
		if _, exists := m.tasks[task.ID]; !exists {
			break
		}
		task.ID = "task_" + randomHex(6)
	}
	m.tasks[task.ID] = task
	if err := m.saveLocked(); err != nil {
		delete(m.tasks, task.ID)
		return DurableTask{}, err
	}
	m.cond.Broadcast()
	return *task, nil
}

func (m *Manager) List(limit int) []DurableTask {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]DurableTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		out = append(out, *task)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *Manager) Find(id string) (DurableTask, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[strings.TrimSpace(id)]
	if !ok {
		return DurableTask{}, false
	}
	return *task, true
}

func (m *Manager) Cancel(id string) bool {
	id = strings.TrimSpace(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok || task.Terminal() {
		return false
	}
	if cancel := m.running[id]; cancel != nil {
		cancel()
		task.Error = "cancel requested"
		_ = m.saveLocked()
		return true
	}
	m.markTerminalLocked(task, StatusCanceled, task.Result, "user canceled")
	_ = m.saveLocked()
	m.cond.Broadcast()
	return true
}

func (m *Manager) workerLoop() {
	defer m.wg.Done()
	for {
		task := m.claimNext()
		if task == nil {
			return
		}
		m.runTask(task)
	}
}

func (m *Manager) claimNext() *DurableTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		if m.closed {
			return nil
		}
		var next *DurableTask
		for _, candidate := range m.tasks {
			if candidate.Status != StatusEnqueued {
				continue
			}
			if next == nil || candidate.CreatedAt.Before(next.CreatedAt) {
				next = candidate
			}
		}
		if next != nil {
			now := time.Now().UTC()
			next.Status = StatusRunning
			next.StartedAt = &now
			_ = m.saveLocked()
			return cloneTaskPtr(next)
		}
		m.cond.Wait()
	}
}

func (m *Manager) runTask(task *DurableTask) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if current, ok := m.tasks[task.ID]; ok && current.Status == StatusRunning {
		m.running[task.ID] = cancel
	}
	m.mu.Unlock()

	result, err := m.runner(ctx, task.Prompt)
	cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.running, task.ID)
	current, ok := m.tasks[task.ID]
	if !ok || current.Status == StatusCanceled {
		_ = m.saveLocked()
		return
	}
	if errors.Is(err, context.Canceled) {
		m.markTerminalLocked(current, StatusCanceled, result, "task canceled")
	} else if err != nil {
		m.markTerminalLocked(current, StatusFailed, result, err.Error())
	} else {
		m.markTerminalLocked(current, StatusCompleted, result, "")
	}
	_ = m.saveLocked()
}

func (m *Manager) markTerminalLocked(task *DurableTask, status Status, result, errText string) {
	now := time.Now().UTC()
	task.Status = status
	task.Result = result
	task.Error = errText
	task.FinishedAt = &now
	if task.StartedAt != nil {
		task.DurationMS = max(0, now.Sub(*task.StartedAt).Milliseconds())
	}
}

func (m *Manager) load() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	raw, err := os.ReadFile(m.path)
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
	for _, task := range data.Tasks {
		if task == nil || task.ID == "" {
			continue
		}
		if task.Status == StatusRunning {
			task.Status = StatusEnqueued
			task.StartedAt = nil
		}
		task.Status = ParseStatus(string(task.Status))
		m.tasks[task.ID] = task
	}
	return nil
}

func (m *Manager) saveLocked() error {
	tasks := make([]*DurableTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		copy := *task
		tasks = append(tasks, &copy)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})
	raw, err := json.MarshalIndent(storeFile{Tasks: tasks}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func cloneTaskPtr(task *DurableTask) *DurableTask {
	if task == nil {
		return nil
	}
	copy := *task
	return &copy
}

func randomHex(bytesLen int) string {
	n := atomic.AddUint64(&idCounter, 1)
	return fmt.Sprintf("%012x", uint64(time.Now().UnixNano())^n)
}
