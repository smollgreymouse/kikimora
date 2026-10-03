package supervisor

import (
	"testing"
	"time"
)

func TestBackoffSequenceAndStableReset(t *testing.T) {
	var b Backoff
	if b.Next() != time.Second || b.Next() != 2*time.Second {
		t.Fatal("unexpected backoff")
	}
	b.MarkReady(time.Unix(10, 0))
	b.ResetIfStable(time.Unix(70, 0))
	if b.Attempt != 0 {
		t.Fatalf("not reset: %d", b.Attempt)
	}
}
