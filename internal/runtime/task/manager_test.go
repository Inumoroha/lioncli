package task

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagerRunsAndPersistsTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	manager, err := NewManager(path, func(_ context.Context, prompt string) (string, error) {
		return "done: " + prompt, nil
	}, 1)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()
	manager.Start()

	task, err := manager.Enqueue("build project")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	completed := waitForTask(t, manager, task.ID, StatusCompleted)
	if !strings.Contains(completed.Result, "build project") {
		t.Fatalf("unexpected result: %q", completed.Result)
	}

	reloaded, err := NewManager(path, func(context.Context, string) (string, error) {
		return "", nil
	}, 1)
	if err != nil {
		t.Fatalf("reload manager: %v", err)
	}
	defer reloaded.Close()
	if found, ok := reloaded.Find(task.ID); !ok || found.Status != StatusCompleted {
		t.Fatalf("reloaded task mismatch: ok=%v task=%+v", ok, found)
	}
}

func TestManagerRecordsFailures(t *testing.T) {
	manager, err := NewManager(filepath.Join(t.TempDir(), "tasks.json"), func(context.Context, string) (string, error) {
		return "", errors.New("boom")
	}, 1)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()
	manager.Start()

	task, err := manager.Enqueue("fail")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	failed := waitForTask(t, manager, task.ID, StatusFailed)
	if failed.Error != "boom" {
		t.Fatalf("unexpected error: %q", failed.Error)
	}
}

func TestManagerCancelsRunningTask(t *testing.T) {
	started := make(chan struct{})
	manager, err := NewManager(filepath.Join(t.TempDir(), "tasks.json"), func(ctx context.Context, prompt string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}, 1)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()
	manager.Start()

	task, err := manager.Enqueue("wait")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	if !manager.Cancel(task.ID) {
		t.Fatal("Cancel returned false")
	}
	waitForTask(t, manager, task.ID, StatusCanceled)
}

func TestManagerCancelRunningTaskWaitsForRunnerToReturn(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	manager, err := NewManager(filepath.Join(t.TempDir(), "tasks.json"), func(ctx context.Context, prompt string) (string, error) {
		close(started)
		<-ctx.Done()
		<-release
		return "", ctx.Err()
	}, 1)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()
	manager.Start()

	task, err := manager.Enqueue("wait")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	if !manager.Cancel(task.ID) {
		t.Fatal("Cancel returned false")
	}
	if got, ok := manager.Find(task.ID); !ok || got.Status != StatusRunning || got.Error != "cancel requested" {
		t.Fatalf("running task should stay running until runner exits, got ok=%v task=%+v", ok, got)
	}

	close(release)
	waitForTask(t, manager, task.ID, StatusCanceled)
}

func TestManagerCancelQueuedTaskMarksCanceledImmediately(t *testing.T) {
	block := make(chan struct{})
	manager, err := NewManager(filepath.Join(t.TempDir(), "tasks.json"), func(ctx context.Context, prompt string) (string, error) {
		select {
		case <-block:
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}, 1)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()
	manager.Start()

	first, err := manager.Enqueue("first")
	if err != nil {
		t.Fatalf("Enqueue first: %v", err)
	}
	waitForTask(t, manager, first.ID, StatusRunning)

	second, err := manager.Enqueue("second")
	if err != nil {
		t.Fatalf("Enqueue second: %v", err)
	}
	if !manager.Cancel(second.ID) {
		t.Fatal("Cancel queued returned false")
	}
	canceled, ok := manager.Find(second.ID)
	if !ok || canceled.Status != StatusCanceled {
		t.Fatalf("queued task should be canceled immediately, ok=%v task=%+v", ok, canceled)
	}
	close(block)
}

func TestHandleCommand(t *testing.T) {
	manager, err := NewManager(filepath.Join(t.TempDir(), "tasks.json"), func(context.Context, string) (string, error) {
		return "ok", nil
	}, 1)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()

	out := HandleCommand(manager, "add sample task")
	if !strings.Contains(out, "background task queued") {
		t.Fatalf("unexpected add output: %s", out)
	}
	if out := HandleCommand(manager, "list"); !strings.Contains(out, "sample task") {
		t.Fatalf("unexpected list output: %s", out)
	}
}

func waitForTask(t *testing.T, manager *Manager, id string, want Status) DurableTask {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, ok := manager.Find(id)
		if ok && task.Status == want {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := manager.Find(id)
	t.Fatalf("task %s did not reach %s; latest=%+v", id, want, task)
	return DurableTask{}
}
