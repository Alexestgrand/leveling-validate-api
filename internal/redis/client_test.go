package redisstore

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRateLimitWindow(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	client, err := NewClient("redis://"+mr.Addr()+"/0", 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	userID := "123456789012345678"
	window := 24 * time.Hour
	maxAttempts := 2

	for i := 1; i <= maxAttempts; i++ {
		limited, err := client.IsRateLimited(ctx, userID, maxAttempts)
		if err != nil {
			t.Fatalf("IsRateLimited: %v", err)
		}
		if limited {
			t.Fatalf("attempt %d should not be limited yet", i)
		}

		count, err := client.IncrementAttempt(ctx, userID, window)
		if err != nil {
			t.Fatalf("IncrementAttempt: %v", err)
		}
		if count != i {
			t.Fatalf("count = %d, want %d", count, i)
		}
	}

	limited, err := client.IsRateLimited(ctx, userID, maxAttempts)
	if err != nil {
		t.Fatalf("IsRateLimited: %v", err)
	}
	if !limited {
		t.Fatal("should be rate limited after max attempts")
	}

	remaining, err := client.RemainingAttempts(ctx, userID, maxAttempts)
	if err != nil {
		t.Fatalf("RemainingAttempts: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining = %d, want 0", remaining)
	}
}

func TestMarkWinner(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	client, err := NewClient("redis://"+mr.Addr()+"/0", 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	userID := "winner-user"

	winner, err := client.IsWinner(ctx, userID)
	if err != nil {
		t.Fatalf("IsWinner: %v", err)
	}
	if winner {
		t.Fatal("should not be winner initially")
	}

	if err := client.MarkWinner(ctx, userID); err != nil {
		t.Fatalf("MarkWinner: %v", err)
	}

	winner, err = client.IsWinner(ctx, userID)
	if err != nil {
		t.Fatalf("IsWinner: %v", err)
	}
	if !winner {
		t.Fatal("should be winner after MarkWinner")
	}
}
