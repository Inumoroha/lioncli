package runtime

import (
	"context"
	"testing"
	"time"
)

func TestCancellationTokenCancelsDerivedContext(t *testing.T) {
	token := NewCancellationToken()
	ctx := ContextWithToken(context.Background(), token)

	if token.IsCancelled() {
		t.Fatal("new token should not be canceled")
	}
	token.Cancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("derived context was not canceled")
	}
	if !token.IsCancelled() {
		t.Fatal("token should report canceled")
	}
}
