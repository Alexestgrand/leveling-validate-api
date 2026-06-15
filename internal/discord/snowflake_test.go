package discord

import (
	"strconv"
	"testing"
	"time"
)

func TestAccountCreatedAt(t *testing.T) {
	ts, err := AccountCreatedAt("418148799423090688")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
	if ts.After(time.Now()) {
		t.Fatal("creation time should be in the past")
	}
}

func TestIsAccountOldEnough(t *testing.T) {
	ok, err := IsAccountOldEnough("418148799423090688", 5, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("old account should meet minimum age")
	}

	recentID := encodeSnowflake(time.Now().Add(-24 * time.Hour))
	ok, err = IsAccountOldEnough(recentID, 5, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("1-day-old account should not meet 5-day minimum")
	}
}

func encodeSnowflake(createdAt time.Time) string {
	ms := createdAt.UnixMilli() - discordEpoch
	id := uint64(ms) << 22
	return strconv.FormatUint(id, 10)
}
