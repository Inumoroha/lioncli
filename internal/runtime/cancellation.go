package runtime

import (
	"context"
	"sync"
)

type CancellationToken struct {
	done chan struct{}
	once sync.Once
}

func NewCancellationToken() *CancellationToken {
	return &CancellationToken{done: make(chan struct{})}
}

func (t *CancellationToken) Cancel() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		close(t.done)
	})
}

func (t *CancellationToken) Done() <-chan struct{} {
	if t == nil {
		return nil
	}
	return t.done
}

func (t *CancellationToken) IsCancelled() bool {
	if t == nil {
		return false
	}
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

func ContextWithToken(parent context.Context, token *CancellationToken) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	if token == nil {
		return parent
	}
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-token.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}
