# Admin Auth Session Bootstrap V1 - Specification

## 1) Purpose + Scope

### Purpose
Định nghĩa behavior source-of-truth cho endpoint bootstrap session admin để frontend xác định trạng thái đăng nhập theo chuẩn production-safe: server là nguồn sự thật, frontend chỉ giữ state hiển thị.

### In-scope
- Contract `GET /admin/auth/session` cho admin UI bootstrap.
- Quy tắc phối hợp với flow hiện có: login thành công, runtime API trả `401` thì buộc quay lại trạng thái chưa đăng nhập.
- Quy tắc boundary giữa session-check endpoint và refresh/critical endpoints.

### Out-of-scope
- Thay đổi contract `POST /admin/auth/login`, `POST /admin/auth/refresh`, `POST /admin/auth/logout`.
- Thiết kế lại cơ chế critical action signature.
- Đổi chính sách TTL hoặc rotation policy admin API key.

---

## 2) Terminology / Actors

- **Admin UI**: frontend gọi endpoint session để hydrate auth state lúc app khởi động.
- **Controlplane IAM**: backend xác thực runtime fragments và trả session snapshot.
- **Runtime fragments**: `admin_api_token`, `device_id`, `device_secret`.
- **Authenticated state**: FE coi người dùng đã đăng nhập khi endpoint session trả `200`.
- **Unauthenticated state**: FE buộc quay về login khi endpoint session hoặc protected API trả `401`.

---

## 3) API Contract

### Endpoint
- `GET /admin/auth/session`

### Request
- Không có body.
- Yêu cầu cookies runtime hợp lệ (`admin_api_token`, `device_id`, `device_secret`) theo cùng boundary auth admin runtime.

### Success response
- HTTP `200 OK`
- Body (canonical tối giản cho FE bootstrap):

```json
{
  "authenticated": true
}
```

- Response MUST NOT phát sinh token/cookie mới.

### Error response semantics
- `401 Unauthorized`: thiếu/invalid/expired runtime fragments.
- `429 Too Many Requests`: rate limit.
- `503 Service Unavailable`: dependency auth tạm thời không sẵn sàng.
- `5xx`: internal failure khác.

---

## 4) Flow Behavior

### Main flow
1. Admin UI khởi động và gọi `GET /admin/auth/session` một lần để bootstrap trạng thái.
2. Request đi qua chain auth runtime admin (cookie fragments + runtime secret verify).
3. Nếu pass, backend trả `200` + payload `authenticated=true`.
4. FE set session state thành authenticated và mở protected routes.

### Runtime invalidation flow
1. FE gọi các protected admin APIs bình thường.
2. Nếu bất kỳ request protected trả `401`, FE MUST clear auth state local.
3. FE MUST redirect về `/auth/admin` và hiển thị notice session expired theo policy UI.

### Failure branches
- Session endpoint trả `401`: FE set unauthenticated ngay, không retry loop vô hạn.
- Session endpoint trả `5xx`: FE có thể retry ngắn hạn giới hạn số lần; nếu vẫn lỗi thì giữ unauthenticated + lỗi generic.

### Preconditions
- Admin channel cookies đang có trên browser.
- Cấu hình `credentials: include` cho fetch client.

### Postconditions
- `200`: FE và backend cùng nhìn nhận session còn hợp lệ.
- `401`: FE và backend cùng nhìn nhận session không hợp lệ.

---

## 5) Data & Boundary Rules

- Source-of-truth cho auth validity là backend IAM runtime verification.
- Session endpoint là read-only auth probe; MUST NOT mutate DB state.
- Session endpoint MUST NOT yêu cầu critical-signature headers.
- Refresh semantics (rolling renew) nếu có, vẫn thuộc endpoint refresh riêng và không được trộn vào session probe.
- Session endpoint v1 MUST NOT fetch thêm business data từ service/repository.

---

## 6) Security Rules

- Endpoint MUST fail-closed khi thiếu/invalid fragments.
- Không log secrets/tokens/raw cookie values.
- Lỗi trả client phải generic (`unauthorized`, `service unavailable`) theo error-envelope/policy hiện hành.
- Session endpoint phải đi qua auth middleware runtime admin; không mở công khai.

---

## 7) Failure Semantics

- **Redis/runtime verify fail**: trả `503` hoặc `5xx`, không fail-open.
- **Cookie mismatch/token invalid**: trả `401` generic.
- **Network/client abort**: FE coi là bootstrap thất bại tạm thời, không tự coi authenticated.
- **Retry policy**: FE retry bounded (ví dụ tối đa 2 lần) cho lỗi tạm thời; không retry cho `401`.

---

## 8) Non-functional Baseline

- Session endpoint SHOULD là lightweight read path, target p95 < 100ms trong môi trường nội bộ bình thường.
- Không thêm DB write vào hot path bootstrap.
- Không tạo thêm high-cardinality labels từ payload phản hồi.

---

## 9) Acceptance Criteria

- [ ] Có `GET /admin/auth/session` với semantics `200/401/5xx` rõ ràng.
- [ ] FE bootstrap auth state bằng session endpoint, không giả định authenticated khi chưa có server confirmation.
- [ ] FE xử lý `401` từ protected APIs bằng cách clear state + redirect login.
- [ ] Session endpoint không yêu cầu critical-signature headers.
- [ ] Không rò rỉ secret/token trong log hoặc response.
- [ ] Build/test liên quan auth flow pass theo kế hoạch kiểm thử.
