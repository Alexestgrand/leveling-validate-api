package discord

import (
	"fmt"
	"strconv"
	"time"
)

// discordEpoch is the Discord snowflake epoch (2015-01-01T00:00:00.000Z).
const discordEpoch int64 = 1420070400000

// AccountCreatedAt extracts the account creation timestamp from a Discord user snowflake ID.
// Discord encodes the creation time in the upper bits of the 64-bit snowflake.
func AccountCreatedAt(userID string) (time.Time, error) {
	id, err := strconv.ParseUint(userID, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid discord user id: %w", err)
	}

	// Timestamp is stored in milliseconds in bits 22-63 (after shifting right by 22).
	timestampMs := (int64(id) >> 22) + discordEpoch
	return time.UnixMilli(timestampMs), nil
}

// AccountAgeDays returns the number of whole days since the Discord account was created.
func AccountAgeDays(userID string, now time.Time) (int, error) {
	createdAt, err := AccountCreatedAt(userID)
	if err != nil {
		return 0, err
	}

	age := now.Sub(createdAt)
	if age < 0 {
		return 0, nil
	}
	return int(age.Hours() / 24), nil
}

// IsAccountOldEnough checks whether the account meets the minimum age requirement.
func IsAccountOldEnough(userID string, minDays int, now time.Time) (bool, error) {
	days, err := AccountAgeDays(userID, now)
	if err != nil {
		return false, err
	}
	return days >= minDays, nil
}
