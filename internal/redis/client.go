package redisstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	attemptsKeyPrefix     = "attempts:"
	winnerKeyPrefix       = "winner:"
	oauthStateKeyPrefix   = "oauth_state:"
	statsTotalAttemptsKey   = "stats:total_attempts"
	statsTestersKey         = "stats:testers"
	eventSubmissionClosedKey = "event:submission_closed"
	eventFirstValidAtKey     = "event:first_valid_at"
	eventFirstValidUserKey   = "event:first_valid_user"
	feedbackEntriesKey       = "feedback:entries"
	feedbackIPKeyPrefix      = "feedback:ip:"
)

const (
	maxFeedbackEntries   = 500
	maxFeedbackListSize  = 100
	feedbackRateLimitMax = 3
	feedbackRateLimitTTL = time.Hour
)

// Client wraps go-redis with domain-specific helpers for rate limiting and winners.
type Client struct {
	rdb     *redis.Client
	timeout time.Duration
}

// NewClient creates a Redis client from a URL with a configured operation timeout.
func NewClient(redisURL string, opTimeout time.Duration) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	// Pool sizing supports horizontal scaling: each API instance shares this Redis.
	opts.PoolSize = 20
	opts.MinIdleConns = 5
	opts.PoolTimeout = opTimeout
	opts.ReadTimeout = opTimeout
	opts.WriteTimeout = opTimeout

	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return &Client{rdb: rdb, timeout: opTimeout}, nil
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < c.timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout)
}

// Ping checks Redis connectivity.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	return c.rdb.Ping(ctx).Err()
}

// Close shuts down the connection pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}

func attemptsKey(userID string) string {
	return attemptsKeyPrefix + userID
}

func winnerKey(userID string) string {
	return winnerKeyPrefix + userID
}

// IsWinner returns true if the user has already validated the correct phrase.
func (c *Client) IsWinner(ctx context.Context, userID string) (bool, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	n, err := c.rdb.Exists(ctx, winnerKey(userID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// MarkWinner permanently records a successful validation (no expiration).
func (c *Client) MarkWinner(ctx context.Context, userID string) error {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	return c.rdb.Set(ctx, winnerKey(userID), "1", 0).Err()
}

// GetAttemptCount returns the current attempt count within the active window.
func (c *Client) GetAttemptCount(ctx context.Context, userID string) (int, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	val, err := c.rdb.Get(ctx, attemptsKey(userID)).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("invalid attempt counter: %w", err)
	}
	return count, nil
}

// IncrementAttempt atomically increments the attempt counter.
// TTL is set only on the first attempt of a window (fixed window from first try).
func (c *Client) IncrementAttempt(ctx context.Context, userID string, window time.Duration) (int, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	key := attemptsKey(userID)
	count, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}

	// Set TTL on first attempt — defines the 24h window from the first submission.
	if count == 1 {
		if err := c.rdb.Expire(ctx, key, window).Err(); err != nil {
			return int(count), err
		}
	}

	return int(count), nil
}

// RemainingAttempts calculates how many submissions remain in the current window.
func (c *Client) RemainingAttempts(ctx context.Context, userID string, maxAttempts int) (int, error) {
	count, err := c.GetAttemptCount(ctx, userID)
	if err != nil {
		return 0, err
	}
	remaining := maxAttempts - count
	if remaining < 0 {
		return 0, nil
	}
	return remaining, nil
}

// IsRateLimited returns true when the user has exhausted their attempts for the window.
func (c *Client) IsRateLimited(ctx context.Context, userID string, maxAttempts int) (bool, error) {
	count, err := c.GetAttemptCount(ctx, userID)
	if err != nil {
		return false, err
	}
	return count >= maxAttempts, nil
}

// SubmissionStats holds aggregate public counters for phrase testing activity.
type SubmissionStats struct {
	TotalAttempts int64 `json:"total_attempts"`
	UniqueTesters int64 `json:"unique_testers"`
}

// RecordSubmissionStats increments global attempt count and tracks unique testers.
// Failures are non-fatal for the validate flow — callers may log and continue.
func (c *Client) RecordSubmissionStats(ctx context.Context, userID string) error {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	pipe := c.rdb.Pipeline()
	pipe.Incr(ctx, statsTotalAttemptsKey)
	pipe.SAdd(ctx, statsTestersKey, userID)
	_, err := pipe.Exec(ctx)
	return err
}

// GetSubmissionStats returns public counters for the live site dashboard.
func (c *Client) GetSubmissionStats(ctx context.Context) (SubmissionStats, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	var stats SubmissionStats

	total, err := c.rdb.Get(ctx, statsTotalAttemptsKey).Int64()
	if err == redis.Nil {
		total = 0
	} else if err != nil {
		return stats, err
	}

	unique, err := c.rdb.SCard(ctx, statsTestersKey).Result()
	if err != nil {
		return stats, err
	}

	stats.TotalAttempts = total
	stats.UniqueTesters = unique
	return stats, nil
}

// StoreOAuthState saves a one-time CSRF token for the Discord OAuth redirect.
func (c *Client) StoreOAuthState(ctx context.Context, state string, ttl time.Duration) error {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	return c.rdb.Set(ctx, oauthStateKeyPrefix+state, "1", ttl).Err()
}

// ConsumeOAuthState validates and deletes a CSRF token (one-time use).
func (c *Client) ConsumeOAuthState(ctx context.Context, state string) (bool, error) {
	if state == "" {
		return false, nil
	}

	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	key := oauthStateKeyPrefix + state
	val, err := c.rdb.GetDel(ctx, key).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return val != "", nil
}

// Raw exposes the underlying client for health checks or testing.
func (c *Client) Raw() *redis.Client {
	return c.rdb
}

// EventClosedInfo describes why submissions were closed.
type EventClosedInfo struct {
	Closed     bool
	Reason     string // "winner" or "manual"
	FirstValid *time.Time
	FirstUser  string
}

// IsEventSubmissionClosed returns true when a camp has already won via a valid phrase.
func (c *Client) IsEventSubmissionClosed(ctx context.Context) (bool, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	n, err := c.rdb.Exists(ctx, eventSubmissionClosedKey).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetEventClosedInfo returns winner metadata when submissions were closed by a valid phrase.
func (c *Client) GetEventClosedInfo(ctx context.Context) (EventClosedInfo, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	info := EventClosedInfo{}
	closed, err := c.rdb.Exists(ctx, eventSubmissionClosedKey).Result()
	if err != nil {
		return info, err
	}
	if closed == 0 {
		return info, nil
	}

	info.Closed = true
	info.Reason = "winner"

	ts, err := c.rdb.Get(ctx, eventFirstValidAtKey).Result()
	if err == nil {
		if parsed, parseErr := time.Parse(time.RFC3339, ts); parseErr == nil {
			info.FirstValid = &parsed
		}
	}

	userID, err := c.rdb.Get(ctx, eventFirstValidUserKey).Result()
	if err == nil {
		info.FirstUser = userID
	}

	return info, nil
}

// CloseEventSubmissions records the first valid phrase and permanently closes submissions.
// Returns true when this call closed the event (first winner).
func (c *Client) CloseEventSubmissions(ctx context.Context, userID string) (bool, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	now := time.Now().UTC().Format(time.RFC3339)
	pipe := c.rdb.TxPipeline()
	setClosed := pipe.SetNX(ctx, eventSubmissionClosedKey, "1", 0)
	pipe.SetNX(ctx, eventFirstValidAtKey, now, 0)
	pipe.SetNX(ctx, eventFirstValidUserKey, userID, 0)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}

	created, err := setClosed.Result()
	if err != nil {
		return false, err
	}
	return created, nil
}

// FeedbackEntry is a public anonymous suggestion stored after the event ends.
type FeedbackEntry struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

// ListFeedback returns the most recent public feedback entries (newest first).
func (c *Client) ListFeedback(ctx context.Context, limit int) ([]FeedbackEntry, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	if limit <= 0 || limit > maxFeedbackListSize {
		limit = maxFeedbackListSize
	}

	raw, err := c.rdb.LRange(ctx, feedbackEntriesKey, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}

	entries := make([]FeedbackEntry, 0, len(raw))
	for _, item := range raw {
		var entry FeedbackEntry
		if err := json.Unmarshal([]byte(item), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// IsFeedbackRateLimited returns true when the IP exceeded the hourly feedback quota.
func (c *Client) IsFeedbackRateLimited(ctx context.Context, ip string) (bool, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	key := feedbackIPKeyPrefix + ip
	count, err := c.rdb.Get(ctx, key).Int()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return count >= feedbackRateLimitMax, nil
}

// RecordFeedback stores an anonymous public suggestion and increments the IP counter.
func (c *Client) RecordFeedback(ctx context.Context, ip, message string) (FeedbackEntry, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	entry := FeedbackEntry{
		ID:        newFeedbackID(),
		Message:   message,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	payload, err := json.Marshal(entry)
	if err != nil {
		return FeedbackEntry{}, err
	}

	pipe := c.rdb.TxPipeline()
	pipe.LPush(ctx, feedbackEntriesKey, payload)
	pipe.LTrim(ctx, feedbackEntriesKey, 0, maxFeedbackEntries-1)

	ipKey := feedbackIPKeyPrefix + ip
	pipe.Incr(ctx, ipKey)
	pipe.Expire(ctx, ipKey, feedbackRateLimitTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		return FeedbackEntry{}, err
	}

	return entry, nil
}

func newFeedbackID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(buf))
}
