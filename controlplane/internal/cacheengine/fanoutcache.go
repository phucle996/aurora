package cacheengine

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// FanoutMessage định nghĩa cấu trúc tin nhắn trao đổi qua Redis Pub/Sub
type FanoutMessage struct {
	// Key là khóa của cache item cần xử lý
	Key string `json:"key"`
	// Version là phiên bản monotonic (UnixNano) của dữ liệu tại thời điểm cập nhật
	Version int64 `json:"version"`
	// Payload là dữ liệu JSON serialized của cache item.
	// Nếu rỗng (nil hoặc length = 0), đây là lệnh DELETE/INVALIDATE.
	// Nếu có dữ liệu, đây là lệnh UPDATE.
	Payload []byte `json:"payload,omitempty"`
}

// fanoutPublishScript thực thi tăng version của key nguyên tử và phát sự kiện lên Pub/Sub trong 1 RTT.
// Sử dụng KEYS để đảm bảo tương thích hoàn toàn với môi trường phân tán Redis Cluster.
var fanoutPublishScript = redis.NewScript(`
	local version_key = KEYS[1]
	local channel = KEYS[2]
	local raw_msg = ARGV[1]

	-- Tăng version nguyên tử cho key cụ thể (Đồng bộ thuật ngữ key-level)
	local new_version = redis.call('INCR', version_key)

	-- Giải mã JSON nhận từ Go, gán version nguyên tử mới, và publish lên Pub/Sub
	local msg = cjson.decode(raw_msg)
	msg.version = new_version
	redis.call('PUBLISH', channel, cjson.encode(msg))

	return new_version
`)

// RedisFanout quản lý việc truyền tải sự kiện đồng bộ cache giữa các instance bằng Redis Pub/Sub
type RedisFanout struct {
	rdb      *redis.Client
	channel  string
	registry *CacheRegistry
}

// NewRedisFanout khởi tạo một RedisFanout mới liên kết với CacheRegistry tương ứng
func NewRedisFanout(rdb *redis.Client, channel string, registry *CacheRegistry) *RedisFanout {
	return &RedisFanout{
		rdb:      rdb,
		channel:  channel,
		registry: registry,
	}
}

// Publish thực thi Lua script để tự động tăng version theo key và phát sự kiện đồng bộ.
// Trả về số phiên bản mới sinh ra và lỗi (nếu có).
func (f *RedisFanout) Publish(ctx context.Context, key string, payload []byte) (int64, error) {
	// Xây dựng version key độc lập cho từng key cụ thể (Đồng bộ thuật ngữ key-level)
	versionKey := fmt.Sprintf("cacheengine:version:%s", key)

	// Đóng gói tin nhắn từ Go để tự động xử lý base64/JSON encoding an toàn cho Payload []byte
	msg := FanoutMessage{
		Key:     key,
		Payload: payload,
	}
	bytes, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}

	// Thực thi Lua Script nguyên tử trên Redis
	res, err := fanoutPublishScript.Run(ctx, f.rdb, []string{versionKey, f.channel}, string(bytes)).Result()
	if err != nil {
		return 0, err
	}

	// Ép kiểu kết quả trả về từ Lua script an toàn
	switch v := res.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("cacheengine: unexpected version type returned from Lua script: %T", res)
	}
}

// StartSubscribe bắt đầu loop nền lắng nghe sự kiện đồng bộ và áp dụng vào L1 CacheRegistry.
// Hàm này giải nén JSON động, so khớp phiên bản (version check) và chống tràn bộ nhớ (OOM prevention).
func (f *RedisFanout) StartSubscribe(ctx context.Context) error {
	pubsub := f.rdb.Subscribe(ctx, f.channel)
	defer pubsub.Close()

	// Khởi chạy Ping Loop chạy ngầm để theo dõi trạng thái kết nối của Redis (Flush-on-Reconnect)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		wasDisconnected := false

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := f.rdb.Ping(ctx).Err()
				if err != nil {
					wasDisconnected = true
				} else if wasDisconnected {
					// Redis vừa phục hồi kết nối thành công -> Flush sạch RAM L1 để chống stale state
					f.registry.Flush()
					wasDisconnected = false
				}
			}
		}
	}()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}

			var fMsg FanoutMessage
			if err := json.Unmarshal([]byte(msg.Payload), &fMsg); err != nil {
				continue // Bỏ qua tin nhắn lỗi định dạng
			}

			// 1. Trường hợp xóa cache (Delete)
			if len(fMsg.Payload) == 0 {
				f.registry.InvalidateLocal(ctx, fMsg.Key)
				continue
			}

			// 2. Trường hợp cập nhật cache (Update)
			// SRE Warning: Tránh lỗi OOM bằng cách kiểm tra key có đang tồn tại trong RAM L1 cục bộ không.
			// Nếu instance hiện tại chưa từng query key này, ta bỏ qua không lưu để tiết kiệm bộ nhớ.
			val, exists := f.registry.GetLocalRaw(fMsg.Key)
			if !exists {
				continue
			}

			// Kiểm tra phiên bản đơn điệu để tránh việc tin nhắn đến trễ đè lên dữ liệu mới hơn (stale write)
			localEnv, ok := val.(*CacheEnvelope)
			if !ok || fMsg.Version <= localEnv.Version {
				continue
			}

			// Tách chuỗi key để xác định namespace (ví dụ: "zone_catalog:" -> "zone_catalog")
			parts := strings.SplitN(fMsg.Key, ":", 2)
			if len(parts) == 0 {
				continue
			}
			namespace := parts[0]

			loader := f.registry.GetLoader(namespace)
			if loader == nil || loader.Factory == nil {
				continue
			}

			// Tạo instance trống thông qua Factory tự động của registry
			ptrTarget := loader.Factory()
			if err := json.Unmarshal(fMsg.Payload, ptrTarget); err != nil {
				continue
			}

			// Giải tham chiếu con trỏ thô để lấy struct/slice nguyên bản
			rawVal := reflect.ValueOf(ptrTarget).Elem().Interface()

			// Cập nhật trực tiếp vào L1 Cache cục bộ (COW)
			f.registry.SetLocalRaw(fMsg.Key, &CacheEnvelope{
				Key:     fMsg.Key,
				Version: fMsg.Version,
				Value:   rawVal,
			}, loader.TTL)
		}
	}
}
