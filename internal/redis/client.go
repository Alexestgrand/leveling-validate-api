package redisstore

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	attemptsKeyPrefix = "attempts:"
	winnerKeyPrefix   = "winner:"
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

// Raw exposes the underlying client for health checks or testing.
func (c *Client) Raw() *redis.Client {
	return c.rdb
}
