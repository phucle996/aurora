# Admin Auth Renew Flow (Temp Spec)

## 1) Purpose + Scope

### Purpose
Định nghĩa behavior source-of-truth cho cơ chế gia hạn admin runtime session theo mô hình **short-lived + rolling refresh + idle timeout**.

### In-scope
- `POST /admin/auth/refresh` contract và semantics.
- Runtime rules cho `admin_api_token`, `device_id`, `device_secret` trong cửa sổ ngắn hạn.
- Idle-timeout semantics (không hoạt động quá ngưỡng thì buộc re-auth).
- Security/failure semantics cho refresh.

### Out-of-scope
- UI refresh scheduling policy chi tiết.
- Rotation lifecycle của admin API key (spec riêng).
- Recovery/revoke incident flow chi tiết (spec riêng).

---

## 2) Core Policy

- Runtime admin session MUST short-lived, mục tiêu mặc định: **15 phút**.
- Refresh MUST chỉ gia hạn khi session hiện tại còn hợp lệ.
- Refresh MUST không được fail-open khi thiếu bất kỳ fragment bắt buộc nào.
- Nếu admin **idle > 15 phút** (không còn runtime state hợp lệ), MUST yêu cầu login lại đầy đủ (`admin_api_key + 2FA + device checks`).
- Realtime session tracking cho admin được giữ ở Redis runtime record; DB chỉ nhận finalize tracking khi session kết thúc.

---

## 3) API Contract

### Endpoint
- `POST /admin/auth/refresh`

### Request
- Không yêu cầu body.
- Yêu cầu cookies runtime hiện tại:
  - `admin_api_token`
  - `device_id`
  - `device_secret`

### Success response
- `200 OK`
- Body:
```json
{ "ok": true }
```
- Response header:
  - `X-Session-Expires-In: <seconds>`
  - Giá trị là số giây còn lại của runtime session sau khi refresh thành công.
- Set lại cookies runtime:
  - `admin_api_token` mới (exp mới = now + 15m)
  - `device_id` (giữ nguyên)
  - `device_secret` (giữ nguyên raw fragment phía client, server-side TTL được gia hạn)

### Error response semantics
- `401`: thiếu/invalid/expired runtime fragments, hoặc runtime secret không còn hợp lệ (idle timeout).
- `429`: rate limit.
- `5xx`: dependency/runtime failure.

---

## 4) Flow Behavior

### Main flow
1. Request đi qua middleware auth admin runtime (verify đủ 3 fragments).
2. Request MUST pass `AdminCIDR` + `AdminCriticalActionSignatureGuard` (device signing).
3. Verify JWT admin token hợp lệ và `claims.device_id == cookie.device_id`.
4. Verify `device_secret` khớp runtime store theo `device_id`.
5. Nếu pass, gia hạn TTL runtime store cho device context thêm 15 phút và cập nhật `last_seen_at` trong Redis runtime record.
6. Issue `admin_api_token` mới với expiry 15 phút.
7. Handler set lại cookies runtime theo expiry mới.
8. Trả `200`.

### Idle-timeout branch
- Nếu runtime store không còn record `device_secret` cho `device_id` hiện tại,
  - refresh MUST trả `401` generic,
  - client MUST login lại đầy đủ.

### Expired-token branch
- Nếu `admin_api_token` đã expired tại thời điểm refresh,
  - refresh MUST trả `401` generic,
  - không được tự bypass bằng refresh khi token đã chết.

---

## 5) Data & Boundary Rules

### Source-of-truth
- Runtime validity cho admin session dựa trên cả:
  - token claims/signature/exp,
  - device runtime secret state (Redis).
- Realtime activity (`last_seen_at`) lấy từ Redis runtime record.
- DB là nguồn lịch sử bền vững và chỉ được cập nhật khi session finalize.

### Boundary
- Repository/DB không tham gia trực tiếp renew hot-path nếu policy không yêu cầu.
- SQL MUST không xuất hiện ở handler/service renew nếu chỉ cần runtime verify.
- Runtime cache TTL là control điểm cho idle timeout.
- Renew hot-path MUST không ghi `last_seen_ip/ua` vào DB theo từng request.

---

## 6) Security Rules

- Không log raw `admin_api_token`, `device_secret`, `admin_api_key`, `mfa_code`.
- Response lỗi auth MUST generic (`unauthorized`).
- Refresh endpoint MUST đi qua cùng auth boundary như admin runtime routes.
- Refresh endpoint được phân loại critical action và MUST có `AdminCriticalActionSignatureGuard` + `AdminCIDR`.
- Refresh token issuance MUST dùng secret provider hiện hành, không hardcode secret.
- Cookie flags (`HttpOnly`, `Secure`, `SameSite`, `Path`, `Domain`) MUST đồng nhất policy admin channel.

---

## 7) Failure Semantics

- Missing cookie fragment => `401`.
- Signature invalid / device mismatch / secret mismatch => `401`.
- Runtime TTL expired (idle timeout) => `401`.
- Secret provider unavailable => `5xx` fail-closed.
- Redis unavailable trong bước verify/touch => `5xx` fail-closed.

---

## 8) Observability

- Audit events tối thiểu:
  - `admin_refresh_success`
  - `admin_refresh_denied`
- Failure reason tags (nội bộ):
  - `missing_fragment`
  - `token_expired`
  - `token_invalid`
  - `device_secret_missing`
  - `device_secret_mismatch`
  - `dependency_unavailable`

---

## 9) Client Coordination Contract

- Client MUST NOT đọc cookie runtime trực tiếp (HttpOnly policy).
- Client SHOULD dùng `X-Session-Expires-In` để lập lịch refresh kế tiếp.
- Ngưỡng khuyến nghị cho client: nếu `X-Session-Expires-In <= 300` thì SHOULD gọi `POST /admin/auth/refresh`; nếu `> 300` thì chưa refresh.
- Nếu không đọc được header này, client MAY fallback timer bảo thủ (ví dụ refresh định kỳ 5 phút khi tab active).
- Khi refresh trả `401`, client MUST chuyển trạng thái unauthenticated và điều hướng về login flow.

---

## 10) Acceptance Criteria

- [ ] Runtime token/cookie mặc định 15 phút.
- [ ] `POST /admin/auth/refresh` chỉ pass khi đủ 3 fragments hợp lệ.
- [ ] Refresh success cấp token expiry mới 15 phút.
- [ ] Refresh success trả header `X-Session-Expires-In` hợp lệ (đơn vị giây).
- [ ] Idle quá 15 phút bị `401` và buộc login lại.
- [ ] Không có nhánh refresh fail-open.
- [ ] Cookie policy sau refresh nhất quán với login policy.
