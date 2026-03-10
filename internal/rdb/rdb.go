package rdb

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	queueKey      = "inreview:queue"
	inProgressPfx = "inreview:inprogress:"
	cachePfx      = "inreview:cache:"
	rateLimitPfx  = "inreview:rl:"

	inProgressTTL      = 20 * time.Minute // exceeds max sync timeout (15 min)
	CacheTTL           = 30 * time.Minute
	LeaderboardCacheTTL = 10 * time.Minute
)

type Client struct {
	rdb *redis.Client
}

func New(redisURL string) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parsing redis URL: %w", err)
	}
	c := &Client{rdb: redis.NewClient(opts)}
	if err := c.rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("connecting to redis: %w", err)
	}
	return c, nil
}

func (c *Client) Close() error { return c.rdb.Close() }

// ── Cache ─────────────────────────────────────────────────────────────────────

func (c *Client) Get(ctx context.Context, key string) ([]byte, bool) {
	val, err := c.rdb.Get(ctx, cachePfx+key).Bytes()
	if err != nil {
		return nil, false
	}
	return val, true
}

func (c *Client) Set(ctx context.Context, key string, val []byte, ttl time.Duration) {
	c.rdb.Set(ctx, cachePfx+key, val, ttl)
}

func (c *Client) Del(ctx context.Context, keys ...string) {
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = cachePfx + k
	}
	c.rdb.Del(ctx, full...)
}

// ── Queue ─────────────────────────────────────────────────────────────────────

// QPush enqueues a repo full name. Call QMarkInProgress first to reserve the slot.
func (c *Client) QPush(ctx context.Context, fullName string) error {
	return c.rdb.RPush(ctx, queueKey, fullName).Err()
}

// QPop blocks until a job is available or timeout elapses. Returns ("", false) on timeout.
func (c *Client) QPop(ctx context.Context, timeout time.Duration) (string, bool) {
	res, err := c.rdb.BLPop(ctx, timeout, queueKey).Result()
	if err != nil || len(res) < 2 {
		return "", false
	}
	return res[1], true
}

func (c *Client) QMarkInProgress(ctx context.Context, fullName string) {
	c.rdb.Set(ctx, inProgressPfx+fullName, 1, inProgressTTL)
}

func (c *Client) QMarkDone(ctx context.Context, fullName string) {
	c.rdb.Del(ctx, inProgressPfx+fullName)
}

func (c *Client) QIsInProgress(ctx context.Context, fullName string) bool {
	return c.rdb.Exists(ctx, inProgressPfx+fullName).Val() > 0
}

// QPosition returns the 0-based position of fullName in the pending queue,
// or -1 if the item is not in the queue (either being actively synced or idle).
func (c *Client) QPosition(ctx context.Context, fullName string) int64 {
	pos, err := c.rdb.LPos(ctx, queueKey, fullName, redis.LPosArgs{}).Result()
	if err != nil {
		return -1
	}
	return pos
}

// ── Rate limiting ─────────────────────────────────────────────────────────────

// RateLimit returns a chi-compatible middleware that limits requests per IP.
// max is the request count allowed per window duration.
func (c *Client) RateLimit(max int, window time.Duration) func(http.Handler) http.Handler {
	windowSecs := int64(window.Seconds())
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := realIP(r)
			win := time.Now().Unix() / windowSecs
			key := fmt.Sprintf("%s%s:%d", rateLimitPfx, ip, win)

			pipe := c.rdb.Pipeline()
			incr := pipe.Incr(r.Context(), key)
			pipe.Expire(r.Context(), key, window*2)
			if _, err := pipe.Exec(r.Context()); err != nil {
				// Redis error — let the request through rather than blocking everyone.
				next.ServeHTTP(w, r)
				return
			}

			if incr.Val() > int64(max) {
				http.Error(w, "rate limited", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func realIP(r *http.Request) string {
	if xfwd := r.Header.Get("X-Forwarded-For"); xfwd != "" {
		return strings.TrimSpace(strings.SplitN(xfwd, ",", 2)[0])
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		return ip[:idx]
	}
	return ip
}
