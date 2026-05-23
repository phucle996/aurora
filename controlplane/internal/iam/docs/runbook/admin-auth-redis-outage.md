# Admin Auth Redis Outage Runbook

## 1) Mục tiêu

Runbook này hướng dẫn vận hành khi Redis gặp sự cố ảnh hưởng đến luồng admin auth (`/admin`) và critical admin actions.

Phạm vi: admin login, admin runtime auth middleware, critical signature guard, critical step-up 2FA (recovery code path).

---

## 2) Tóm tắt tác động

Khi Redis lỗi (timeout, connection refused, packet loss cao):
- Admin login có thể fail do không lưu được `device_secret` runtime.
- Request `/admin` đã login có thể fail vì không verify được `device_secret` fragment.
- Critical actions có thể fail vì không lock được nonce replay.
- Critical step-up bằng recovery code có thể fail vì lock consume recovery code phụ thuộc Redis.

Hệ thống hiện hành áp dụng **fail-closed** cho các bước security-critical phụ thuộc Redis.

Baseline vận hành cho admin flow:
- `p95 latency < 800ms`
- `error rate < 1%`

---

## 3) Failure mode matrix (policy)

### 3.1 `AdminAPIKeyAuth`

- Dependency Redis: verify `device_secret` theo `device_id`.
- Redis outage behavior: **deny request**.
- HTTP result: `503` (authentication temporarily unavailable).

### 3.2 `AdminCriticalActionSignatureGuard`

- Dependency Redis: nonce replay lock (`SETNX`).
- Redis outage behavior: **deny critical action**.
- HTTP result: `503`.

### 3.3 `AdminCriticalActionStepUp2FA`

- `totp` path: không phụ thuộc Redis lock (có cache RAM + DB fallback).
- `recovery_code` path: cần Redis lock trước consume.
- Redis lock fail behavior: **deny request** (theo middleware mapping hiện tại).

### 3.4 `AdminLogin`

- Dependency Redis: set runtime `device_secret(hash)` với TTL.
- Redis set fail behavior: **login fail** (không cấp session fragment không đầy đủ).

---

## 4) Triệu chứng thường gặp

- Tăng đột biến `503` ở `/admin/*`.
- Login admin thất bại dù credential/MFA đúng.
- Critical action fail không ổn định theo thời điểm.
- Log hạ tầng Redis timeout/connection errors tăng.

---

## 5) Quy trình xử lý sự cố

## Bước 1 — Xác nhận incident

- Check health Redis (ping, connect, auth, latency).
- Check error rate của route admin:
  - `/admin/auth/login`
  - `/admin/auth/logout`
  - critical admin routes
- Nếu p95 > 800ms hoặc error rate > 1% kéo dài theo window cảnh báo nội bộ, coi là degraded.
- Xác nhận có correlation giữa lỗi app và Redis downtime/latency.

## Bước 2 — Đánh giá mức độ ảnh hưởng

- Xác định scope:
  - chỉ 1 node Redis,
  - toàn cụm Redis,
  - hoặc network partition giữa app và Redis.
- Nếu ảnh hưởng toàn cụm: raise severity theo policy incident nội bộ.

## Bước 3 — Thông báo vận hành

- Thông báo rõ: admin auth/critical actions đang fail-closed để bảo toàn security.
- Tránh hướng dẫn bypass tạm thời làm giảm security.

## Bước 4 — Khôi phục Redis

- Thực hiện runbook Redis nội bộ (restart/failover/network fix).
- Ưu tiên khôi phục ổn định kết nối app -> Redis trước.

## Bước 5 — Verify sau khôi phục

- Admin login success trở lại.
- `/admin` runtime auth pass với session hợp lệ.
- Critical action pass với signature + step-up hợp lệ.
- Error rate 5xx/timeout quay về baseline.

---

## 6) Recovery checklist (đóng incident)

- [ ] Redis health ổn định (ping + latency + error rate).
- [ ] Admin login success rate về baseline.
- [ ] `/admin` auth middleware không còn spike `503`.
- [ ] Critical actions hoạt động bình thường.
- [ ] p95 admin critical path quay về `< 800ms`.
- [ ] Error rate admin critical path quay về `< 1%`.
- [ ] Không còn network/timeout alarm liên quan Redis.
- [ ] Gửi post-incident note cho team liên quan.

---

## 7) Ghi chú kiến trúc liên quan

- Một số cache hiện dùng RAM local per-replica (TTL ngắn), không thay thế Redis cho security lock/runtime fragment.
- Redis là bắt buộc cho:
  - runtime `device_secret` verification,
  - nonce replay lock,
  - recovery-code consume lock.
- Thiết kế hiện tại ưu tiên security nhất quán: mất dependency critical => deny (fail-closed).
