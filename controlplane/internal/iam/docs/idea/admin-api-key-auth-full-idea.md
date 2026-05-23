# Admin Authentication Mechanism (Full Idea)

## 1) Mục tiêu tổng thể

Thiết kế một cơ chế xác thực **riêng hoàn toàn cho admin** (không dùng chung flow user), có nhiều lớp bảo vệ để giảm rủi ro lộ token, replay, và chiếm quyền ở các route nhạy cảm.

Tài liệu này là **idea full cơ chế**, không chia phase/version, không phải tài liệu code implement.

---

## 2) Thành phần bảo mật của cơ chế

Cơ chế admin đầy đủ gồm các lớp:

1. **Admin API key** (single active key)  
   - Dùng làm yếu tố xác thực kỹ thuật trung tâm cho kênh `/admin`.

2. **Admin 2FA riêng**  
   - Luồng xác minh yếu tố thứ hai dành riêng admin (không reuse user 2FA).

3. **Recovery code**  
   - Dùng khi admin mất yếu tố 2FA chính.

4. **Device binding**  
   - Ràng buộc session/token admin với cặp `device_id` + `device_secret`.

5. **Request signing cho critical routes**  
   - Các route nhạy cảm bắt buộc chữ ký request để chống replay/tamper.

6. **CIDR guard + rate limit + audit**  
   - Bảo vệ network, chống brute-force, và truy vết đầy đủ.

---

## 3) Nguyên tắc nền tảng

- Admin auth là flow độc lập, không gắn user flow thông thường.
- Chỉ có 1 admin key active tại một thời điểm.
- Không lưu plaintext secret/token/recovery code.
- Client luôn nhận lỗi generic; server log nguyên nhân chi tiết.
- Mọi thao tác critical của admin phải có audit record riêng.

---

## 4) Lifecycle đầy đủ của admin auth

### 4.1 Bootstrap ban đầu

- Mục tiêu: tạo trạng thái khởi tạo an toàn khi hệ thống chưa có admin key/device trust.
- Điều kiện: chỉ cho bootstrap khi bảng apikey của admin chưa có record key.
- Kết quả bootstrap:
  - Sinh admin key đầu tiên.
  - Khởi tạo 2FA admin.
  - Sinh bộ recovery code ban đầu.
  - Đăng ký thiết bị admin đầu tiên (`device_id` + `device_secret`).
- Tất cả bootstrap event đều ghi audit riêng.

### 4.2 Login / Establish admin session

Để vào `/admin`, request phải pass lần lượt:
1. CIDR guard.
2. Verify admin key.
3. Verify 2FA challenge.
4. Verify device binding.
5. Issue cookie/session admin.

### 4.3 Runtime access

- Route admin thường: yêu cầu session admin hợp lệ.
- Route admin critical: ngoài session còn bắt buộc request signing và 2fa code .

### 4.4 Renew / Rotate

- Renew chủ động trước expiry:
  - Rotate admin key theo policy.
  - Rotate hoặc refresh device context theo policy.
  - Cấp lại session/cookie theo policy.
- Runtime session policy cho admin:
  - Access session/cookie ngắn hạn (mục tiêu mặc định: **15 phút**).
  - Có cơ chế **refresh session** để gia hạn khi admin còn hoạt động hợp lệ.
  - Refresh chỉ hợp lệ khi đủ điều kiện security hiện tại (session + device binding + policy check).
  - Refresh phải rotate lại expiry của session token/cookie theo cửa sổ ngắn hạn.
- Idle timeout policy:
  - Nếu không có hoạt động vượt ngưỡng idle cho phép (mục tiêu mặc định: **15 phút**), session tự hết hiệu lực.
  - Hết idle timeout thì buộc đăng nhập lại đầy đủ (admin key + 2FA + device checks theo policy).
- Nếu đã hết hạn:
  - Không cho truy cập admin bình thường.
  - Bắt buộc recovery flow có kiểm soát (2FA hoặc recovery code + device checks).

### 4.5 Recovery

- Khi mất 2FA chính:
  - Dùng recovery code 1 lần để lấy lại quyền.
  - Sau recovery thành công: rotate bắt buộc 2FA seed/recovery codes và invalidate session cũ.
- Nếu có dấu hiệu rủi ro trong quá trình recovery/renew:
  - Rotate admin API key ngay lập tức.
  - Invalidate toàn bộ admin session/cookie hiện tại.
  - Force logout và yêu cầu đăng nhập lại đầy đủ (admin key + 2FA + bind device mới).

### 4.6 Revocation / Incident response

- Khi nghi compromise:
  - Revoke session hiện tại.
  - Rotate admin key ngay.
  - Rotate device secret liên quan.
  - Bắt re-auth + re-2FA trên lần truy cập tiếp theo.

---

## 5) 2FA riêng cho admin (concept)

- 2FA admin là namespace riêng, không dùng chung user table/flow.
- 2FA factor cụ thể sẽ được chốt ở spec.
- Bắt buộc cho các hành động:
  - login admin mới,
  - rotate admin key,
  - thay đổi chính sách bảo mật admin,
  - thao tác hạ tầng critical.
- Challenge có thời gian hiệu lực ngắn và giới hạn retry.

---

## 6) Recovery code (concept)

- Recovery codes được tạo theo batch, dùng một lần.
- Chỉ lưu hash của từng code.
- Mỗi lần dùng recovery code thành công:
  - mark used ngay,
  - ghi audit mức cao,
  - yêu cầu regenerate bộ recovery codes mới.
- Recovery code không thay thế hoàn toàn bảo mật thiết bị; vẫn kết hợp device check theo policy.

---

## 7) Device binding theo `device_id` + `device_secret`

- Khi đăng nhập admin thành công, hệ thống cấp thêm 2 thành phần thiết bị và set vào cookie:
  - cookie `device_id`: định danh thiết bị admin.
  - cookie `device_secret`: shared secret riêng của thiết bị.
- Kết hợp với cookie `admin_token`, cơ chế xác thực được "phân mảnh" thành 3 phần:
  1. `admin_token`
  2. `device_id`
  3. `device_secret`
- Nguyên tắc: thiếu 1 trong 3 phần thì không pass verify cho admin access/critical flow.
- Về lưu trữ server-side:
  - Không lưu plaintext `device_secret`.
  - Chỉ lưu hash/fingerprint để verify.
- Luồng thiết bị:
  1. Sau khi login admin thành công, hệ thống bind danh tính thiết bị theo policy.
  2. Device verification ở mỗi phiên đăng nhập admin.
  3. Device secret rotation định kỳ hoặc khi có nghi vấn compromise.
- Chính sách:
  - Unknown device không được vào critical routes ngay.
  - Có cơ chế quarantine/challenge tăng cường với thiết bị mới hoặc rủi ro cao.

---

## 8) Request signing cho critical routes

Các route critical (ví dụ rotate key, thay đổi chính sách bảo mật, thao tác dữ liệu nhạy cảm) yêu cầu chữ ký request.

### 8.1 Mục tiêu signing

- Chống sửa nội dung request trên đường truyền.
- Chống replay request cũ.
- Tăng non-repudiation cho hành động critical.

### 8.2 Thành phần signing (concept)

- Canonical payload gồm các trường ngữ cảnh request theo policy.
- Ví dụ có thể bao gồm:
  - HTTP method
  - request path
  - body digest
  - timestamp
  - nonce
  - device_id
- Chữ ký được tạo từ khóa phía client/device và được server verify theo thông tin đã bind.

### 8.3 Verify policy

- Reject nếu timestamp lệch cửa sổ cho phép.
- Reject nếu nonce đã dùng (anti-replay store).
- Reject nếu signature mismatch.
- Ghi audit với lý do kỹ thuật chi tiết, nhưng response client generic.
- Renew flow có cửa sổ grace theo policy.
- Quá grace thì bắt buộc full re-auth.

### 8.4 2FA bắt buộc cho Sensitive/Critical Actions

Với các action nhạy cảm, request phải pass đủ 3 lớp đồng thời:
1. Admin session/token hợp lệ.
2. Request signing hợp lệ.
3. 2FA step-up code hợp lệ (bắt buộc).

Nhóm action áp dụng step-up 2FA bắt buộc (ví dụ):
- rotate admin key
- đổi policy bảo mật admin
- thao tác dữ liệu hoặc cấu hình hạ tầng mức critical

Quy tắc 2FA step-up:
- Dùng challenge mới, thời gian hiệu lực ngắn.
- Code chỉ dùng 1 lần cho 1 challenge (one-time).
- Không reuse code/challenge cũ.

Khi thiếu hoặc sai 2FA step-up:
- Từ chối request với response generic.
- Ghi audit reason nội bộ chi tiết.

---

## 9) Cookie/session policy cho admin

- Cookie chỉ dùng cho admin scope/path.
- `HttpOnly`, `Secure`, `SameSite` nghiêm ngặt theo deployment.
- Áp dụng cho toàn bộ bộ ba cookie: `admin_token`, `device_id`, `device_secret`.
- TTL cookie/token cho runtime access nên dùng cửa sổ ngắn (mục tiêu mặc định: **15 phút**).
- Có refresh flow để gia hạn rolling khi admin còn active, không dùng cookie dài hạn cố định.
- Session idle quá ngưỡng phải tự invalidate và yêu cầu đăng nhập lại.
- Khi rotate/recovery/revoke: invalidate session cũ bắt buộc.

---

## 10) Logging, audit, observability

- Tách bảng audit admin riêng cho critical actions.
- Log có phân loại nguyên nhân thất bại (cidr_denied, auth_failed, sign_invalid, nonce_replay, device_untrusted...).
- Không log raw key/token/device_secret/recovery code.
- Metrics/alerts tập trung vào:
  - tần suất auth fail,
  - replay/sign fail,
  - recovery-code usage,
  - rotate/revoke events,
  - truy cập từ CIDR bất thường.

---

## 11) Trạng thái vận hành (state machine mức ý tưởng)

Admin security lifecycle:
- `uninitialized`
- `bootstrapped`
- `active`
- `expiring_soon`
- `expired`
- `recovery_required`
- `compromised`
- `rotated`

State chuyển bởi các sự kiện: bootstrap success, verify success/fail, expiry reached, recovery success, incident trigger, rotate completed.

---

## 12) Kết quả mong muốn sau tài liệu idea

Dựa trên idea full này, spec cần chốt rõ:
- Contract lifecycle bootstrap/login/runtime/critical/recovery/rotate/revoke.
- Contract 2FA admin và recovery code.
- Contract device binding và device secret rotation.
- Contract request signing + anti-replay.
- Contract audit/log/error policy nhất quán toàn bộ admin channel.
