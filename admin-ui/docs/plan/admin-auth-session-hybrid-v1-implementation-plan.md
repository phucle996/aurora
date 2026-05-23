# Admin UI Auth Session Hybrid V1 - Implementation Plan

## 1) Mục tiêu triển khai

Triển khai auth state hybrid cho admin UI: bootstrap bằng `GET /admin/auth/session`, và runtime invalidation bằng cơ chế global `401 -> unauthenticated -> redirect /auth/admin`.
Done definition: UI không còn dùng `/admin/auth/refresh` để check session lúc init; login thành công vào dashboard ổn định; state bị reset đúng khi backend trả `401`.
Không nằm trong scope: đổi contract backend IAM, thay đổi thiết kế login UI/visual, thêm cơ chế persistent local auth ngoài cookie runtime.

---

## 2) Current state vs target state

### Current state
- UI từng dùng `/admin/auth/refresh` để suy luận session bootstrap.
- Endpoint refresh thuộc critical flow nên có thể gây mismatch với nhu cầu probe session của UI.

### Target state
- UI bootstrap auth bằng `GET /admin/auth/session`.
- UI giữ guard runtime: protected API trả `401` => clear state + redirect login.
- Login success chỉ điều hướng dashboard khi session bootstrap xác nhận `authenticated=true`.

---

## 3) Implementation changes (grouped by subsystem)

### A) API client

**Files**
- `SỬA` `admin-ui/src/lib/admin-session.ts`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `getAdminSession` | SỬA | Dựa vào refresh endpoint | Gọi `GET /admin/auth/session` và parse `data.authenticated` | Bootstrap state đúng backend source-of-truth |
| `AdminSession` type | SỬA | Session shape chứa metadata giả lập | Shape tối giản theo contract `{ authenticated: true }` | Tránh phụ thuộc dữ liệu không có trong API |

### B) Session state container

**Files**
- `SỬA` `admin-ui/src/hooks/useAdminSession.tsx`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `resolveAdminSession` | SỬA | Có nhánh refresh scheduling side-effect | Chỉ tập trung bootstrap session + bounded retry | Giảm race và logic dư thừa |
| unauthorized subscription flow | SỬA | Giữ local clear state | Giữ nguyên semantics `401 => unauthenticated` | Đồng bộ với guard runtime toàn cục |

### C) Login flow

**Files**
- `SỬA` `admin-ui/src/pages/auth/Login.tsx`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `onSubmit` success branch | SỬA | Login OK điều hướng sau check chưa chặt | Login OK -> `refreshSession()` -> chỉ navigate khi `authenticated=true` | Tránh bounce về login ngay sau submit |

### D) Global fetch unauthorized

**Files**
- `SỬA` `admin-ui/src/lib/fetch.ts`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `request` 401 handling | SỬA | Có ngoại lệ endpoint cũ | Chuẩn hóa: mọi protected request `401` (trừ login) emit unauthorized | Đảm bảo runtime invalidation nhất quán |

### E) Docs

**Files**
- `THÊM` `admin-ui/docs/plan/admin-auth-session-hybrid-v1-implementation-plan.md`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | THÊM | Chưa có execution plan riêng UI | Có blueprint riêng cho FE ownership | Tách boundary IAM docs vs UI docs rõ ràng |

### F) Tests

**Files**
- `SỬA`/`THÊM` theo test setup hiện có trong `admin-ui` (session hook + login flow)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Session bootstrap tests | THÊM | Chưa lock contract endpoint mới | Verify init-auth / init-unauth / retry behavior | Chặn regress session probe |
| Runtime 401 invalidation tests | THÊM | Chưa lock fully hybrid behavior | Verify unauthorized event clears state + redirect | Chặn regress guard runtime |

---

## 4) Contract changes

- Không đổi public contract backend ở tài liệu UI này.
- UI sử dụng contract đã chốt ở IAM spec:
  - `GET /admin/auth/session` -> `200` với `{ "authenticated": true }`
  - `401` generic khi session invalid.

---

## 5) Test plan + acceptance

### Test plan
- **Happy path**
  - App init gọi `/admin/auth/session` thành công -> render protected page.
  - Login thành công -> session refresh thành công -> vào dashboard.
- **Error path**
  - App init nhận `401` -> render login gate.
  - Protected API nhận `401` -> state unauthenticated + redirect `/auth/admin`.
- **Edge path**
  - Network lỗi tạm thời -> bounded retry, không loop vô hạn.
  - Concurrent pending requests không gây double-redirect nhiễu UX.

### Acceptance checklist
- [ ] UI bootstrap session qua `/admin/auth/session`.
- [ ] Không dùng `/admin/auth/refresh` để check session lúc init.
- [ ] 401 runtime invalidation hoạt động nhất quán.
- [ ] `npm run build` pass.

---

## 6) Rollout & operations

- Enable path: deploy IAM endpoint trước, sau đó deploy UI hybrid flow.
- Fallback: nếu endpoint mới chưa sẵn sàng, UI giữ login gate và không giả định authenticated.
- Monitoring:
  - Tỷ lệ lỗi gọi `/admin/auth/session` tại frontend logs/telemetry.
  - Tỷ lệ redirect `/auth/admin` sau unauthorized events.

---

## 7) Risk & mitigation

1. **Risk**: Deploy lệch phiên bản BE/FE gây `404` endpoint.
   - **Mitigation**: release ordering BE-first; UI fallback unauthenticated.
2. **Risk**: Parse response envelope sai shape.
   - **Mitigation**: parse defensive + test mock payload theo envelope thực tế.
3. **Risk**: Unauthorized event bị emit quá nhiều trong burst.
   - **Mitigation**: đảm bảo clear state idempotent và redirect `replace=true`.
4. **Risk**: Retry quá mạnh khi backend lỗi tạm thời.
   - **Mitigation**: giới hạn retry count + backoff ngắn.
5. **Risk**: Regression UX login bounce.
   - **Mitigation**: gate navigate dashboard theo `authenticated=true` sau refreshSession.
