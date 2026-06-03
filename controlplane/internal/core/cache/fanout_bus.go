// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/cache/fanout_bus.go
//            Redis Pub/Sub Fanout Bus — Provider chung cho content fanout cache đa replica
// ======================================================================================================
//
// 📜 THIẾT KẾ:
//   - RedisFanoutBus là transport layer dùng chung: nhận payload bất kỳ → serialize → PUBLISH.
//   - Để chống Out-of-Order Message Processing, mỗi tin nhắn fanout mang theo một Version (int64) tăng dần.
//   - Version được tăng qua Redis INCR ("core:zone:version") để đảm bảo tính nhất quán trên HA.
//
// ======================================================================================================

package coreCache

import (
	"context"
	"encoding/json"
	"strconv"

	"controlplane/pkg/logger"

	goredis "github.com/redis/go-redis/v9"
)

// FanoutOp là loại tác động được fanout.
type FanoutOp string

const (
	FanoutOpUpsert FanoutOp = "upsert"
	FanoutOpDelete FanoutOp = "delete"
)

// FanoutMessage là envelope được publish qua Redis Pub/Sub.
type FanoutMessage struct {
	Op      FanoutOp        `json:"op"`
	Version int64           `json:"version"` // Phiên bản tăng tuần tự
	Payload json.RawMessage `json:"payload"`
}

// FanoutHandler là hàm được gọi khi nhận được message từ channel.
type FanoutHandler func(op FanoutOp, payload json.RawMessage, version int64)

// RedisFanoutBus là transport layer Redis Pub/Sub dùng chung.
type RedisFanoutBus struct {
	rdb     *goredis.Client
	channel string
}

// NewRedisFanoutBus khởi tạo một fanout bus trên một Redis channel cụ thể.
func NewRedisFanoutBus(rdb *goredis.Client, channel string) *RedisFanoutBus {
	return &RedisFanoutBus{rdb: rdb, channel: channel}
}

// IncrVersion tăng version trên Redis key tương ứng.
func (b *RedisFanoutBus) IncrVersion(ctx context.Context, key string) (int64, error) {
	if b.rdb == nil {
		return 0, goredis.Nil
	}
	return b.rdb.Incr(ctx, key).Result()
}

// GetVersion lấy version hiện tại từ Redis key tương ứng.
func (b *RedisFanoutBus) GetVersion(ctx context.Context, key string) (int64, error) {
	if b.rdb == nil {
		return 0, nil
	}
	val, err := b.rdb.Get(ctx, key).Result()
	if err == goredis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

// Publish serialize payload kèm version rồi PUBLISH lên Redis channel.
func (b *RedisFanoutBus) Publish(ctx context.Context, op FanoutOp, payload any, version int64) error {
	if b.rdb == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	msg := FanoutMessage{Op: op, Version: version, Payload: raw}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := b.rdb.Publish(ctx, b.channel, data).Err(); err != nil {
		logger.SysWarnFields("fanout_bus", "failed to publish fanout message", err, logger.Fields{
			"channel": b.channel,
			"op":      string(op),
			"version": version,
		})
		return err
	}
	return nil
}

// Subscribe lắng nghe fanout messages và route đến handler.
func (b *RedisFanoutBus) Subscribe(ctx context.Context, handler FanoutHandler) error {
	if b.rdb == nil || handler == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	logger.SysInfoFields("fanout_bus", "starting fanout subscriber", logger.Fields{"channel": b.channel})

	pubsub := b.rdb.Subscribe(ctx, b.channel)
	defer pubsub.Close()
	ch := pubsub.Channel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			var fanout FanoutMessage
			if err := json.Unmarshal([]byte(msg.Payload), &fanout); err != nil {
				logger.SysWarnFields("fanout_bus", "received malformed fanout message", err, logger.Fields{"channel": b.channel})
				continue
			}
			handler(fanout.Op, fanout.Payload, fanout.Version)
		}
	}
}
