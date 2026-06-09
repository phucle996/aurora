/*
================================================================================
COMPONENT CONTRACT: L2 Lua Executor (Generic Script Runner)
================================================================================
Scope: Tầng L2 Lua Script Execution Boundary (Redis Engine)
Source of Truth (SoT): Mã script tĩnh và trạng thái dữ liệu nguyên tử tại Redis Node.
Boundary:
- Nhận input: luaScript (string), keys ([]string), args (...interface{}) trực tiếp từ Callsite.
- Thư viện không tự động sinh sinh (generate) hay lưu trữ tĩnh các Lua script.
- Giới hạn: Chạy đơn luồng nguyên tử trên Redis để tránh race condition (TOCTOU).

Position in Component Hierarchy:
[Callsite / Service / Middleware] ──(Định nghĩa script & params)──> [L2LuaExecutor]
                                                                        │
                                                            (Thực thi EVALSHA / EVAL)
                                                                        │
                                                                        ▼
                                                            [L2 Cache (Redis Node)]

Invariants (HA & Security):
1. EVALSHA Caching: Tự động tính toán SHA1 hash của script để thực hiện EVALSHA trước.
   Giúp giảm tối đa dung lượng payload truyền qua mạng trên mỗi request.
2. Fallback Re-load: Nếu Redis báo lỗi "NOSCRIPT", Executor tự động gửi lệnh
   SCRIPT LOAD lên Redis và thực thi lại lệnh EVALSHA mà không làm gián đoạn Caller.
3. Redis Cluster Hash Tag Compatibility: Các khóa trong tham số `keys` phải được định
   dạng bọc chung Hash Tag dạng {namespace:param} để cùng thuộc một Slot.
4. Fail-Close Policy: Đối với các tác vụ bảo mật, nếu Executor lỗi kết nối hoặc Redis crash,
   phải trả lỗi nghiêm trọng lên Callsite để kích hoạt cơ chế phòng thủ từ chối request.
================================================================================
*/

package l2_lua_executor

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"strings"

	"github.com/redis/go-redis/v9"
)

// L2LuaExecutor định nghĩa interface cho bộ thực thi Lua Script generic tại L2 Redis.
type L2LuaExecutor interface {
	// Execute nhận trực tiếp script nguồn, keys, và args để chạy nguyên tử trên Redis.
	Execute(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error)
}

// redisLuaExecutor triển khai L2LuaExecutor sử dụng go-redis client.
type redisLuaExecutor struct {
	rdb *redis.Client
}

// NewL2LuaExecutor khởi tạo một bộ thực thi Lua script generic mới.
func NewL2LuaExecutor(rdb *redis.Client) L2LuaExecutor {
	return &redisLuaExecutor{rdb: rdb}
}

// Execute thực thi mã Lua script bằng cơ chế EVALSHA tối ưu.
func (e *redisLuaExecutor) Execute(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	// 1. Tính toán mã SHA1 hash của script để dùng làm định danh EVALSHA
	h := sha1.New()
	h.Write([]byte(script))
	sha := hex.EncodeToString(h.Sum(nil))

	// 2. Thử thực hiện EVALSHA trước để tránh truyền tải toàn bộ mã nguồn script
	res, err := e.rdb.EvalSha(ctx, sha, keys, args...).Result()
	if err != nil && isNoScriptError(err) {
		// 3. Fallback: Nếu Redis chưa lưu cache script (NOSCRIPT) -> Nạp script vào Redis memory
		_, loadErr := e.rdb.ScriptLoad(ctx, script).Result()
		if loadErr != nil {
			return nil, loadErr
		}
		// 4. Thực thi lại EVALSHA sau khi đã nạp script thành công
		res, err = e.rdb.EvalSha(ctx, sha, keys, args...).Result()
	}

	return res, err
}

// isNoScriptError kiểm tra xem lỗi trả về có phải là lỗi thiếu script cache trên Redis (NOSCRIPT) hay không.
func isNoScriptError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "NOSCRIPT")
}
