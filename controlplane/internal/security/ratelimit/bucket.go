package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	_ "embed"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/token_bucket.lua
var tokenBucketLua string

var (
	ErrEmptyKey         = errors.New("ratelimit: empty key")
	ErrInvalidRate      = errors.New("ratelimit: invalid rate")
	ErrInvalidCost      = errors.New("ratelimit: invalid cost")
	ErrRedisUnavailable = errors.New("ratelimit: redis client is nil")
)

var tokenBucketScript = redis.NewScript(tokenBucketLua)

// Rate defines a token bucket rate (capacity + refill per period).
type Rate struct {
	Capacity int64
	Refill   int64
	Period   time.Duration
}

func (r Rate) valid() bool {
	return r.Capacity > 0 && r.Refill > 0 && r.Period > 0
}

func (r Rate) refillPerMS() float64 {
	ms := float64(r.Period) / float64(time.Millisecond)
	if ms <= 0 {
		return 0
	}
	return float64(r.Refill) / ms
}

func (r Rate) ttl() time.Duration {
	refillDuration := time.Duration(float64(r.Period) * (float64(r.Capacity) / float64(r.Refill)))
	if refillDuration < r.Period {
		refillDuration = r.Period
	}
	return refillDuration + r.Period
}

// Result is the outcome of a rate limit check.
type Result struct {
	Allowed    bool
	Limit      int64
	Remaining  float64
	RetryAfter time.Duration
	ResetAfter time.Duration
}

// Bucket enforces a Redis-backed token bucket limiter.
type Bucket struct {
	client   *redis.Client
	failOpen bool
}

// NewBucket constructs a limiter backed by Redis.
// By default, it fails open when Redis is unavailable.
func NewBucket(client *redis.Client) *Bucket {
	return &Bucket{
		client:   client,
		failOpen: true,
	}
}

// SetFailOpen controls behavior on Redis errors.
func (b *Bucket) SetFailOpen(v bool) {
	if b == nil {
		return
	}
	b.failOpen = v
}

// Allow checks and consumes tokens for the given key.
func (b *Bucket) Allow(ctx context.Context, key string, rate Rate, cost int64) (Result, error) {
	if key == "" {
		return Result{}, ErrEmptyKey
	}
	if !rate.valid() {
		return Result{}, ErrInvalidRate
	}
	if cost <= 0 || cost > rate.Capacity {
		return Result{}, ErrInvalidCost
	}
	if b == nil || b.client == nil {
		if b != nil && !b.failOpen {
			return Result{Allowed: false, Limit: rate.Capacity}, ErrRedisUnavailable
		}
		return Result{Allowed: true, Limit: rate.Capacity}, nil
	}

	refillPerMS := rate.refillPerMS()
	if refillPerMS <= 0 {
		return Result{}, ErrInvalidRate
	}

	ttl := rate.ttl()
	res, err := tokenBucketScript.Run(
		ctx,
		b.client,
		[]string{key},
		rate.Capacity,
		refillPerMS,
		cost,
		ttl.Milliseconds(),
	).Result()
	if err != nil {
		if b.failOpen {
			return Result{Allowed: true, Limit: rate.Capacity}, nil
		}
		return Result{Allowed: false, Limit: rate.Capacity}, err
	}

	allowed, remaining, retryMS, resetMS, err := parseLuaResult(res)
	if err != nil {
		if b.failOpen {
			return Result{Allowed: true, Limit: rate.Capacity}, nil
		}
		return Result{Allowed: false, Limit: rate.Capacity}, err
	}

	return Result{
		Allowed:    allowed,
		Limit:      rate.Capacity,
		Remaining:  remaining,
		RetryAfter: time.Duration(retryMS) * time.Millisecond,
		ResetAfter: time.Duration(resetMS) * time.Millisecond,
	}, nil
}

func parseLuaResult(raw interface{}) (bool, float64, int64, int64, error) {
	arr, ok := raw.([]interface{})
	if !ok || len(arr) < 4 {
		return false, 0, 0, 0, fmt.Errorf("ratelimit: unexpected lua result: %T", raw)
	}

	allowedVal, ok := toInt64(arr[0])
	if !ok {
		return false, 0, 0, 0, fmt.Errorf("ratelimit: invalid allowed value: %T", arr[0])
	}
	remaining, ok := toFloat64(arr[1])
	if !ok {
		return false, 0, 0, 0, fmt.Errorf("ratelimit: invalid remaining value: %T", arr[1])
	}
	retryMS, ok := toInt64(arr[2])
	if !ok {
		return false, 0, 0, 0, fmt.Errorf("ratelimit: invalid retry-after value: %T", arr[2])
	}
	resetMS, ok := toInt64(arr[3])
	if !ok {
		return false, 0, 0, 0, fmt.Errorf("ratelimit: invalid reset-after value: %T", arr[3])
	}

	return allowedVal == 1, remaining, retryMS, resetMS, nil
}
