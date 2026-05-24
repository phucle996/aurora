# Review: Access + User Device Runtime Flow (single-key model)

## Scope
- File chính: `internal/http/middleware/access.go`
- Cache SoT: `internal/iam/cache/user_device_runtime_cache.go`
- Mục tiêu: xác minh flow runtime auth sau khi chuyển sang key model
  `iam:user:device:runtime:<user_id>:<device_id>`.

## Current flow (đã chốt)
1. Access parse JWT từ `Authorization: Bearer`.
2. Verify chữ ký theo secret rotation candidates.
3. Check blacklist `jti` trong Redis.
4. Runtime verify (nếu bật runtime cache):
   - đọc cookie `device_id` + `device_secret`;
   - so khớp `device_id` cookie với `claims.DeviceID`;
   - lookup runtime record O(1) bằng `(claims.Subject, claims.DeviceID)`;
   - match `device_secret` hash + `jti` (current/previous trong grace window).
5. Pass thì inject context identity và cho request đi tiếp.

## Key/Value contract
- Key chính runtime:
  - `iam:user:device:runtime:<user_id>:<device_id>`
- Value: JSON `UserDeviceRuntime` gồm các field chính:
  - `user_id`, `device_id`, `device_secret_hash`, `current_jti`,
    `previous_jti`, `previous_issued_at`, `current_issued_at`,
    `tracked_device_id`, `status`, `version`, `last_seen_*`.

## Rotate semantics
- `RotateFragmentForUserDevice(...)` dùng Lua atomic:
  - đọc key cũ `(user_id, old_device_id)`;
  - CAS theo `expected_jti` (nếu provided);
  - ghi payload mới sang key `(user_id, new_device_id)`;
  - xóa key cũ nếu đổi `device_id`.
- Tránh race “2 request rotate cùng lúc cùng pass”.

## Security semantics
- Fail-closed cho lỗi verify/signature/runtime lookup.
- Unauthorized path clear cookie `device_id` + `device_secret`.
- Không leak lý do mismatch chi tiết ra client.

## Operational notes
- Không còn phụ thuộc `tracking_id` claim để runtime lookup.
- `tracked_device_id` vẫn giữ để liên kết sang persistent device DB.
- `ScanByUser` chỉ còn phục vụ nghiệp vụ liệt kê/quan sát device runtime.

## Risk checklist
- Redis down -> auth runtime fail-closed (đúng chủ đích).
- Grace window quá rộng -> tăng cửa chấp nhận `previous_jti`.
- Grace window quá ngắn -> dễ reject khi rotate sát thời điểm request.

## Conclusion
- Flow hiện tại đã đồng nhất theo single-key `user_id + runtime_device_id`.
- Access verify runtime là O(1), không scan-by-user trên đường nóng.
- Tracking-based lookup đã loại khỏi auth-path chính.
