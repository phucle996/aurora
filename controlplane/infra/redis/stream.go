package redis

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	DefaultStreamName        = "events"
	defaultIdempotencyPrefix = "stream:idem"
)

var (
	ErrInvalidStreamName     = errors.New("redis stream: invalid stream name")
	ErrInvalidPayload        = errors.New("redis stream: invalid payload")
	ErrInvalidIdempotencyTTL = errors.New("redis stream: invalid idempotency ttl")
	ErrPublisherUnavailable  = errors.New("redis stream: publisher unavailable")
)

type StreamMessage struct {
	Stream         string
	Payload        map[string]string
	IdempotencyKey string
}

func (m StreamMessage) Validate() error {
	if strings.TrimSpace(m.Stream) == "" {
		return ErrInvalidStreamName
	}
	if len(m.Payload) == 0 {
		return ErrInvalidPayload
	}
	if strings.TrimSpace(m.IdempotencyKey) == "" {
		return ErrInvalidPayload
	}
	return nil
}

type StreamPublisher interface {
	Publish(ctx context.Context, msg StreamMessage, idempotencyTTL time.Duration) (streamID string, published bool, err error)
}

type RedisStreamPublisher struct {
	rdb *goredis.Client
}

func NewRedisStreamPublisher(rdb *goredis.Client) *RedisStreamPublisher {
	return &RedisStreamPublisher{rdb: rdb}
}

var publishIdempotentScript = goredis.NewScript(`
local idemKey = KEYS[1]
local streamKey = KEYS[2]
local idemTTL = tonumber(ARGV[1])

local setOk = redis.call("SET", idemKey, "1", "NX", "EX", idemTTL)
if not setOk then
  return ""
end

local xaddArgs = {streamKey, "*"}
for i = 2, #ARGV, 2 do
  table.insert(xaddArgs, ARGV[i])
  table.insert(xaddArgs, ARGV[i+1])
end

return redis.call("XADD", unpack(xaddArgs))
`)

func (p *RedisStreamPublisher) Publish(ctx context.Context, msg StreamMessage, idempotencyTTL time.Duration) (string, bool, error) {
	if p == nil || p.rdb == nil {
		return "", false, ErrPublisherUnavailable
	}
	if err := msg.Validate(); err != nil {
		return "", false, err
	}
	if idempotencyTTL <= 0 {
		return "", false, ErrInvalidIdempotencyTTL
	}

	stream := strings.TrimSpace(msg.Stream)
	if stream == "" {
		stream = DefaultStreamName
	}
	idempotencyKey := strings.TrimSpace(msg.IdempotencyKey)
	idemRedisKey := fmt.Sprintf("%s:%s", defaultIdempotencyPrefix, idempotencyKey)

	keys := make([]string, 0, len(msg.Payload))
	for k := range msg.Payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	argv := make([]interface{}, 0, 1+len(keys)*2)
	argv = append(argv, strconv.FormatInt(int64(idempotencyTTL/time.Second), 10))
	for _, key := range keys {
		argv = append(argv, key, msg.Payload[key])
	}

	result, err := publishIdempotentScript.Run(ctx, p.rdb, []string{idemRedisKey, stream}, argv...).Result()
	if err != nil {
		return "", false, err
	}
	streamID, _ := result.(string)
	if strings.TrimSpace(streamID) == "" {
		return "", false, nil
	}
	return streamID, true, nil
}
