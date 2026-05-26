# Auth Session Bootstrap Continuity V1 - Specification

## 1) Purpose + Problem Statement

### Purpose

Chuẩn hoá contract và hành vi cho `GET /api/v1/auth/session` theo hướng cloud-native session-continuity:

- Frontend bootstrap auth state từ server-side session truth.
- Tránh spam `401` liên tục trên DevTools/network khi user đang unauthenticated.
- Không để frontend tự quản refresh lifecycle phức tạp.

### Current Problem

Hiện tượng quan sát:

- Endpoint session bị gọi lặp dày khi chưa login.
- Backend trả `401` liên tục -> DevTools nhiễu, tốn tài nguyên, khó quan sát lỗi thật.

### Scope

- Contract `GET /api/v1/auth/session` cho user channel (`/api/v1/auth/*`).
- FE behavior cho bootstrap + retry/backoff.
- Error semantics để giảm 401-noise nhưng vẫn fail-closed security.

### Out-of-scope

- Redesign toàn bộ login/register domain logic.
- Thay đổi RBAC/authz policy downstream.
- Triển khai token exchange/service mesh auth.

---

## 2) Design Principles

1. **Server is source-of-truth** cho authenticated session.
2. **Fail-closed** cho auth invalid (vẫn trả `401` đúng nghĩa).
3. **No infinite probing** từ frontend khi đã biết user unauthenticated.
4. **Bounded retries** chỉ cho lỗi tạm thời (`5xx/network`), không retry loop cho `401`.
5. **Session continuity**: backend absorb complexity; frontend chỉ render state.

---

## 3) API Contract (V1)

### Endpoint

- `GET /api/v1/auth/session`

### Request

- Không body.
- Cookie auth runtime gửi qua `credentials: include`.

### Success (`200`)

```json
{
  "authenticated": true
}
```

### Unauthenticated (`401`)

```json
{
  "error": "unauthorized",
  "message": "unauthorized"
}
```

### Temporary failure (`503`)

```json
{
  "error": "service_unavailable",
  "message": "authentication temporarily unavailable"
}
```

### Contract rules

- Endpoint là read-only probe, không mutate state.
- Không phát token mới trong response này.
- Không yêu cầu critical-signature headers.

---

## 4) Frontend Anti-401-Spam Behavior (Normative)

FE MUST implement state machine sau:

### 4.1 Initial bootstrap

1. **Boot lần đầu**
   - Gọi `GET /api/v1/auth/session` đúng 1 lần khi app start.

2. **Nếu `200`**
   - Set `authenticated=true`.
   - Không polling session theo timer ngắn.

3. **Nếu `401`**
   - Set `authenticated=false`.
   - Ghi `sessionProbeBlockedUntil` (cooldown) tối thiểu `30s` (khuyến nghị 60s).
   - Trong cooldown, FE MUST NOT gọi lại `/auth/session` tự động.
   - Chỉ gọi lại khi có trigger rõ ràng:
     - user submit login thành công,
     - user manual refresh trang,
     - route guard explicit requires recheck sau cooldown.

4. **Nếu `5xx` hoặc network error**
   - Retry bounded tối đa 2 lần với exponential backoff (ví dụ 300ms, 1s).
   - Hết retry thì dừng, hiển thị generic error state.
   - Không chuyển thành loop vô hạn.

5. **Cross-tab dedupe (mandatory)**
   - MUST dùng `BroadcastChannel` làm cơ chế chính để phát kết quả session probe giữa các tab.
   - MUST có fallback `localStorage event` cho môi trường không hỗ trợ `BroadcastChannel`.
   - Một tab giữ vai trò leader để gọi `/api/v1/auth/session`; tab còn lại chờ kết quả tối đa 500ms trước khi tự probe.
   - Kết quả probe (`200` hoặc `401`) MUST được reuse toàn tab trong TTL 5-10s để tránh burst request.

### 4.2 Subsequent recheck policy (những lần sau)

FE MUST NOT gọi `/api/v1/auth/session` theo polling timer định kỳ ngắn. Các lần recheck sau bootstrap chỉ được phép khi có trigger rõ ràng và phải qua anti-spam gate.

1. **Allowed triggers**
   - Sau login thành công (re-hydrate state).
   - User manual hard refresh/re-open app.
   - Tab trở lại foreground sau idle dài (khuyến nghị > 15 phút).
   - Protected API trả `401` lần đầu trong phiên hiện tại và cần confirm auth state.

2. **MUST NOT triggers**
   - Route change thông thường.
   - Mỗi lần render component auth guard.
   - Timer lặp ngắn (5s/10s/30s) khi không có sự kiện nghiệp vụ.

3. **Anti-spam gate (mandatory)**
   - `sessionProbeInFlight`: nếu đang có probe, tất cả caller MUST await cùng 1 promise; không tạo probe mới.
   - `sessionProbeBlockedUntil`: sau `401`, block mọi auto-probe tới khi hết cooldown (tối thiểu 30s, khuyến nghị 60s).
   - `minRecheckInterval`: khoảng cách tối thiểu giữa 2 probe thành công (khuyến nghị 60s), trừ trigger login thành công.

4. **Retry semantics cho các lần sau**
   - `401`: không retry ngay; set unauthenticated + cooldown.
   - `5xx/network`: retry bounded tối đa 2 lần với exponential backoff, sau đó dừng.

---

## 5) Backend Guardrails

1. **Keep 401 semantics**
   - Không đổi `401` thành `200` giả để “đẹp log”.

2. **Rate-limit nhẹ cho session endpoint**
   - Thêm route-level limit riêng để chặn client lỗi/polling bug.
   - Gợi ý baseline: nhỏ nhưng không làm hại UX bootstrap.

3. **Structured observability**
   - Metric tách riêng cho endpoint:
     - `auth_session_probe_total{status}`
     - `auth_session_probe_401_total`
     - `auth_session_probe_5xx_total`
   - Log sampling cho `401` nhằm giảm noise (ví dụ 10-20%).

4. **No DB write in hot path**
   - Session probe giữ read-only/low-latency.

5. **Recommended endpoint-specific limiter baseline**
   - Session endpoint nên có bucket riêng (không dùng chung bucket nhạy cảm của login mutation).
   - Baseline vận hành khuyến nghị:
     - per-IP burst: `20 req / 30s`
     - per-IP sustained: `60 req / 5m`
     - per device/session key burst: `10 req / 30s`
   - Mục tiêu của limiter này là chặn bug loop client; không dùng để auth decision.

---

## 6) SLI / SLO / Alert Baseline

### 6.1 SLI definitions

- `session_probe_rps`: requests/sec của `GET /api/v1/auth/session`.
- `session_probe_status_ratio{code}`: tỉ lệ theo status (`200/401/429/5xx`).
- `session_probe_per_user_30m`: số lần probe trên mỗi user-session trong cửa sổ 30 phút.
- `session_probe_per_ip_5m`: số lần probe trên mỗi IP trong cửa sổ 5 phút.
- `session_probe_latency_ms`: latency endpoint theo p50/p95/p99.
- `session_probe_retry_attempts`: số retry trung bình cho lỗi `5xx/network`.

### 6.2 SLO targets (production baseline)

- Latency:
  - p50 < `40ms`
  - p95 < `100ms`
  - p99 < `200ms`
- Availability (`2xx + 401 + 429` coi là expected auth outcomes):
  - >= `99.9%` theo 30 ngày.
- Error budget cho `5xx`:
  - <= `0.1%` tổng session probe requests / 30 ngày.
- Session probe volume hygiene:
  - p50 `session_probe_per_user_30m` <= `2`
  - p95 `session_probe_per_user_30m` <= `5`
  - p99 `session_probe_per_user_30m` <= `12`

### 6.3 Alert thresholds (initial)

1. **Spam regression alert**
   - Trigger khi p95 `session_probe_per_user_30m` > `8` liên tục 15 phút.
   - Severity: warning.

2. **Severe spam alert**
   - Trigger khi p99 `session_probe_per_user_30m` > `20` liên tục 10 phút.
   - Severity: critical.

3. **False-positive risk alert**
   - Trigger khi `401` ratio tăng > `+30%` so với baseline cùng khung giờ trong 30 phút,
     đồng thời `login_success_total` không giảm tương ứng.
   - Mục tiêu: phát hiện deny oan thay vì user thật logout.

4. **Limiter-noise alert (`429`)**
   - Trigger khi `429` ratio của `/auth/session` > `2%` trong 10 phút.
   - Hành động: kiểm tra loop FE trước, chỉ nới limiter sau khi xác nhận không có bug client.

5. **Backend degradation alert (`5xx`)**
   - Trigger khi `5xx` ratio > `0.5%` trong 5 phút hoặc > `0.2%` trong 30 phút.
   - Severity: critical nếu kéo dài > 15 phút.

### 6.4 Sampling policy for logs

- `200`: sample `1-5%`.
- `401`: sample `10-20%`.
- `429`: sample `50-100%` (ưu tiên giữ cao để debug loop).
- `5xx`: sample `100%`.

---

## 7) Acceptance Criteria

- [ ] FE bootstrap chỉ gọi `/api/v1/auth/session` 1 lần khi app start.
- [ ] Khi nhận `401`, FE vào cooldown, không loop request.
- [ ] Retry bounded chỉ áp cho `5xx/network`, không áp cho `401`.
- [ ] Backend giữ semantics `200/401/503` rõ ràng, fail-closed.
- [ ] Có metric/log tách riêng cho session probe để theo dõi spam regression.
- [ ] p95 `session_probe_per_user_30m` <= `5` sau rollout 7 ngày.
- [ ] `429` ratio của `/auth/session` <= `2%` ở traffic bình thường.
- [ ] `5xx` ratio của `/auth/session` <= `0.1%` theo rolling 24h.

---

## 8) Rollout Plan (Safe)

1. Ship FE cooldown/backoff + dedupe trước (phase 1).
2. Bật metrics + dashboard cho SLI mục 6.1 (phase 1 song song).
3. Theo dõi 72h, chốt baseline `session_probe_per_user_30m` (phase 2).
4. Bật limiter endpoint-specific + alert `429`/spam regression (phase 2).
5. Sau 7 ngày ổn định, lock SLO targets và freeze rollout (phase 3).
6. Chỉ khi cần mới mở rộng payload bootstrap (`user`, `permissions`, `feature_flags`) (phase 4).

---

## 9) Notes for Current Codebase

- `AuthHandler.Session` hiện trả `200` stub (`ok`) cần được align với auth runtime thực.
- Route `/api/v1/auth/session` phải đi qua đúng auth middleware boundary.
- Spec này áp cho user channel; admin channel đã có spec riêng (`/admin/auth/session`).
