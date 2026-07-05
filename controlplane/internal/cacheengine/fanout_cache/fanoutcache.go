package fanout_cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unsafe"

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
	rdb                 *redis.Client
	channel             string
	onMessageCallback   func(key string, payload []byte, version int64)
	onReconnectCallback func()
}

// NewRedisFanout khởi tạo một RedisFanout mới.
func NewRedisFanout(rdb *redis.Client, channel string) *RedisFanout {
	return &RedisFanout{
		rdb:     rdb,
		channel: channel,
	}
}

// SetCallbacks gán các hàm xử lý tin nhắn và tái kết nối động
func (f *RedisFanout) SetCallbacks(onMessage func(key string, payload []byte, version int64), onReconnect func()) {
	f.onMessageCallback = onMessage
	f.onReconnectCallback = onReconnect
}

// Publish thực thi Lua script để tự động tăng version theo key và phát sự kiện đồng bộ.
// Trả về số phiên bản mới sinh ra và lỗi (nếu có).
func (f *RedisFanout) Publish(ctx context.Context, key string, payload []byte) (int64, error) {

	// Xây dựng version key độc lập cho từng key cụ thể theo định dạng {module_name}:version:{namespace}:{params}
	var versionKey string
	if idx := strings.IndexByte(key, ':'); idx != -1 {
		versionKey = fmt.Sprintf("%s:version:%s", key[:idx], key[idx+1:])
	} else {
		versionKey = fmt.Sprintf("cacheengine:version:%s", key)
	}

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
		return 0, fmt.Errorf("fanout_cache: unexpected version type returned from Lua script: %T", res)
	}
}

// StartSubscribe bắt đầu loop nền lắng nghe sự kiện đồng bộ và kích hoạt callback.
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
					// Redis vừa phục hồi kết nối thành công -> gọi callback dọn dẹp L1 nếu có
					if f.onReconnectCallback != nil {
						f.onReconnectCallback()
					}
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
			// Sử dụng unsafe để cast string sang []byte không sao chép bộ nhớ (Zero allocation)
			payloadBytes := unsafe.Slice(unsafe.StringData(msg.Payload), len(msg.Payload))
			if err := json.Unmarshal(payloadBytes, &fMsg); err != nil {
				continue // Bỏ qua tin nhắn lỗi định dạng
			}

			// Gọi callback để xử lý tin nhắn nhận được nếu có
			if f.onMessageCallback != nil {
				f.onMessageCallback(fMsg.Key, fMsg.Payload, fMsg.Version)
			}
		}
	}
}
