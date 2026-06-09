package l2_lua_executor

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestL2LuaExecutor kiểm thử bộ thực thi Lua script generic L2LuaExecutor.
func TestL2LuaExecutor(t *testing.T) {
	// 1. Khởi tạo mock Redis server
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	ctx := context.Background()
	executor := NewL2LuaExecutor(rdb)

	// 2. Định nghĩa script đơn giản trả về tổng 2 đối số
	simpleScript := `
		local a = tonumber(ARGV[1])
		local b = tonumber(ARGV[2])
		return a + b
	`

	// 3. Thực thi script lần 1 (Redis chưa có cache -> NOSCRIPT -> SCRIPT LOAD -> Thực thi lại)
	res, err := executor.Execute(ctx, simpleScript, []string{}, "10", "20")
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}

	val, ok := res.(int64)
	if !ok || val != 30 {
		t.Fatalf("expected 30, got %v (type %T)", res, res)
	}

	// 4. Kiểm thử xem script đã được cached thành công trên Redis
	h := sha1.New()
	h.Write([]byte(simpleScript))
	sha := hex.EncodeToString(h.Sum(nil))

	hasScript, err := rdb.ScriptExists(ctx, sha).Result()
	if err != nil {
		t.Fatalf("ScriptExists failed: %v", err)
	}
	if !hasScript[0] {
		t.Fatal("expected script to be cached on Redis")
	}

	// 5. Thực thi script lần 2 (Đọc trực tiếp từ cache EVALSHA, không tốn RTT load)
	res2, err := executor.Execute(ctx, simpleScript, []string{}, "100", "200")
	if err != nil {
		t.Fatalf("unexpected second execution error: %v", err)
	}
	if res2.(int64) != 300 {
		t.Fatalf("expected 300, got %v", res2)
	}
}
