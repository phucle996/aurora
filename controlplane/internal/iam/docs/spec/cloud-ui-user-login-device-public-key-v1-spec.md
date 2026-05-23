# Cloud UI User Login Device Public Key V1 - Specification

## 1) Scope

### 1.1 Source of Truth
- **SoT duy nhất cho thay đổi này**: user login contract `POST /api/v1/auth/login` của IAM (không dùng admin login contract).
- Mục tiêu: Cloud UI gửi `device_public_key` thật từ browser khi login user; backend validate + bind vào device record.

### 1.2 In-scope
- Mở rộng request contract user login để nhận `device_public_key`.
- Chuẩn hóa format key: base64 của ed25519 public key (32 bytes).
- FE cloud-ui sinh keypair và gửi public key trong request login.
- BE IAM validate key format, fingerprint key, ghi vào device binding tại flow `AuthService.Login`.
- Quy tắc fallback khi client chưa hỗ trợ WebCrypto.

### 1.3 Out-of-scope
- Không thay đổi admin login `/admin/auth/login`.
- Không thay đổi refresh/logout contract.
- Không định nghĩa đầy đủ challenge-sign flow (nonce sign/verify) trong version này.
- Không đổi semantics cookie/token issuance hiện tại.

---

## 2) Actors / Roles

- **End User (Cloud UI)**: đăng nhập bằng username/password.
- **Cloud UI SignIn Form**: sinh device keypair, gửi `device_public_key`.
- **IAM Auth Handler**: bind request + map lỗi HTTP.
- **IAM Auth Service**: validate business + upsert device binding.
- **Device Repository/DB**: lưu record device + public key/fingerprint.

Permission boundary:
- Chỉ user hợp lệ (credential đúng, status hợp lệ) mới được bind/update device key.

---

## 3) API Contract (Behavior)

### 3.1 Endpoint
- `POST /api/v1/auth/login`

### 3.2 Request
```json
{
  "username": "string",
  "password": "string",
  "device_public_key": "base64-ed25519-public-key"
}
```

### 3.3 Request Semantics
- `username`: required, giữ rule hiện tại.
- `password`: required, giữ rule hiện tại.
- `device_public_key`: **required in V1**.
  - Chuẩn accepted:
    - decode được bằng base64 std hoặc raw std,
    - sau decode đúng `32 bytes` (ed25519 public key size),
    - canonical lưu/so sánh bằng base64 std.

### 3.4 Success Response
- Giữ nguyên contract hiện tại của user login:
  - HTTP status, cookie set, header/session semantics không đổi.
- Không trả private key hay secret key trong response body.

### 3.5 Error Behavior
- Nếu `device_public_key` thiếu/invalid:
  - trả lỗi theo nhóm invalid argument của flow login user,
  - message generic, không leak chi tiết cryptographic internals.
- Nếu lưu device binding fail:
  - trả lỗi dependency/auth unavailable theo envelope chuẩn hiện tại.

---

## 4) User Story + Acceptance Criteria

### US-ULOGIN-PK-001
As a cloud user, I want login request to include a real device public key, so that the backend can bind session/device to a cryptographic device identity.

#### AC-ULOGIN-PK-001 (Happy path)
- Given browser hỗ trợ WebCrypto ed25519
- When user submit form login hợp lệ
- Then cloud-ui gửi `device_public_key` base64 trong payload
- And backend login thành công như cũ (set cookie/session)
- And device record được bind với public key + fingerprint.

#### AC-ULOGIN-PK-002 (Invalid key)
- Given payload có `device_public_key` không decode được hoặc sai length
- When request tới login endpoint
- Then backend từ chối request theo error envelope login
- And không tạo session/token runtime mới.

#### AC-ULOGIN-PK-003 (Browser unsupported)
- Given browser không hỗ trợ generate ed25519 key theo chính sách FE
- When user thực hiện login
- Then FE hiển thị thông báo lỗi hành động rõ ràng
- And không gửi request login thiếu `device_public_key`.

#### AC-ULOGIN-PK-004 (Security)
- Given login request/response đi qua FE/BE logs
- Then không log private key, không log raw device secret/token.

---

## 5) Business Rules

- **BR-ULOGIN-PK-001**
  - Statement: User login V1 bắt buộc có `device_public_key` hợp lệ.
  - Owner: System Security.
  - Enforcement: Handler validation + Service normalization.

- **BR-ULOGIN-PK-002**
  - Statement: Public key canonical form phải là base64 std của 32-byte ed25519 key.
  - Owner: IAM Service.
  - Enforcement: Service function normalize/validate key.

- **BR-ULOGIN-PK-003**
  - Statement: Device binding phải lưu fingerprint SHA-256 của canonical public key.
  - Owner: IAM Service/Repository.
  - Enforcement: Service tính fingerprint trước upsert repo.

- **BR-ULOGIN-PK-004**
  - Statement: Không thay đổi token/session issuance semantics hiện tại của user login.
  - Owner: IAM Product Contract.
  - Enforcement: Auth service flow giữ nguyên sau bước bind key.

---

## 6) Main Flow

1. User nhập `username/password` tại cloud-ui signin.
2. FE lấy/generate ed25519 keypair phía client.
3. FE export public key -> base64 std -> set `device_public_key` vào payload login.
4. Handler bind request, chuyển vào service.
5. Service validate credential như hiện tại.
6. Service normalize/validate `device_public_key`.
7. Service upsert login device kèm public key + fingerprint.
8. Service issue access/refresh/runtime như hiện tại.
9. Handler trả success response như hiện tại.

---

## 7) Exception Flows

### EX-ULOGIN-PK-001: FE không tạo được keypair
- Trigger: WebCrypto không hỗ trợ / generate thất bại.
- Behavior: FE block submit, hiển thị message generic "Thiết bị không hỗ trợ xác thực khóa công khai".
- No backend side effect.

### EX-ULOGIN-PK-002: Payload thiếu public key
- Trigger: bug FE hoặc request thủ công.
- Behavior: handler reject invalid argument.
- No token/cookie issuance.

### EX-ULOGIN-PK-003: Device upsert lỗi DB
- Trigger: dependency failure.
- Behavior: trả authentication unavailable, không issue session mới.

---

## 8) Edge Cases

- EC-001: Public key decode được nhưng length != 32 bytes -> reject invalid argument.
- EC-002: Base64 raw std input -> accept, canonicalize sang base64 std.
- EC-003: User đăng nhập đa tab cùng lúc với cùng key -> upsert device idempotent theo fingerprint/user scope.
- EC-004: User rotate browser profile (key mới) -> tạo/bind device record mới theo rule repo hiện tại.

---

## 9) State Transition Impact

- Không đổi user account status transition.
- Bổ sung trạng thái dữ liệu device-binding:
  - `unbound` -> `bound_with_public_key` tại lần login hợp lệ.
- Session lifecycle (`login -> refresh -> logout`) giữ nguyên.

---

## 10) Security + Privacy Constraints

- Private key phải ở client, non-exportable nếu nền tảng cho phép.
- BE chỉ nhận public key, không nhận private key/challenge signature ở V1.
- Message lỗi generic (không lộ key parsing chi tiết cho end-user).
- Không log secret/token/private key trong FE/BE logs.

---

## 11) Non-goals

- Không triển khai challenge-response signature verification trong V1.
- Không migrate toàn bộ device lịch sử cũ về có public key ngay lập tức.
- Không thay đổi admin auth flows.

---

## 12) Traceability (Requirement -> Rule -> Flow)

- US-ULOGIN-PK-001 -> BR-ULOGIN-PK-001/002/003/004 -> Main Flow step 2-8.
- AC-ULOGIN-PK-001 -> Main Flow happy path.
- AC-ULOGIN-PK-002 -> EX-ULOGIN-PK-002 + EC-001/002.
- AC-ULOGIN-PK-003 -> EX-ULOGIN-PK-001.
- AC-ULOGIN-PK-004 -> Security constraints.

---

## 13) Open Questions (Need Product/Tech Lead Confirmation)

1. Browser compatibility baseline bắt buộc là gì (Chrome/Edge/Safari versions)?
2. Nếu browser không hỗ trợ ed25519 WebCrypto: hard-block login hay cho fallback policy đặc biệt?
3. Repo-level uniqueness cho `public_key_fingerprint` là `(user_id,fingerprint)` hay global?
4. Device name source: có bắt buộc hostname client thay vì placeholder không (liên quan ghi chú hiện tại trong admin service)?

