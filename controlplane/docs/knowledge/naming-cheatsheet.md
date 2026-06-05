# Naming Cheatsheet (Ultra-Short)

Mục tiêu: đọc trong 30-60s trước khi code để giữ naming nhất quán.

## 1) Core Rules

- Rõ nghĩa hơn ngắn.
- Một khái niệm = một tên canonical.
- Tên phải phản ánh đúng layer ownership.
- Reuse trước, tạo mới sau.

## 2) Package

- lowercase, không underscore, không camelCase.
- theo responsibility duy nhất.
- ✅ `ratelimit`, `observability`, `devicehint`
- ❌ `utils`, `common`, `misc`

## 3) File

- snake_case, theo feature/chức năng.
- ✅ `auth_handler.go`, `refresh_token_service.go`, `http_header.go`
- ❌ `final_auth.go`, `new_handler.go`, `abc.go`

## 4) Function

- public: PascalCase + verb rõ.
- private: camelCase + verb + object.
- ✅ `RateLimitPreAuth`, `buildRateLimitRules`, `validateDeviceHint`
- ❌ `DoWork`, `Process`, `Handle` (thiếu ngữ cảnh)

## 5) Type / Interface

- danh từ rõ domain/capability.
- ✅ `AuthService`, `DeviceRepo`, `EvaluateResult`
- ❌ `DataManager`, `BaseProcessor`

## 6) Constants / Keys

- dùng canonical constants, không hardcode string rải rác.
- header key: `controlplane/pkg/constant/http_header.go`
- context key: `controlplane/pkg/constant/context_key.go`
- cookie key: `controlplane/pkg/constant/cookie.go`

## 7) Boundary Naming (must)

- Handler: transport intent (`Login`, `Refresh`, `RotateKey`)
- Service: business action (`AuthenticateUser`, `RotateAdminKey`)
- Repo: persistence action (`FindByEmail`, `SaveSession`, `RevokeToken`)
- Không trộn boundary trong tên.

## 8) Quick Do/Don't

- ✅ `trackingDeviceID`, `routePattern`, `subjectKeyHash`
- ❌ `data`, `obj`, `tmp2`, `info`
- ✅ generic public error + internal structured reason
- ❌ leak internal/security reason ra client

## 9) Pre-Commit Naming Check

1. Đã có canonical symbol chưa?
2. Tên phản ánh đúng layer chưa?
3. Có đang hardcode key thay vì reuse constant không?
4. Có từ mơ hồ (`new`, `final`, `tmp`, `misc`) không?

Nếu chưa chắc: đọc

- `controlplane/docs/knowledge/skill-knowledge-base.md`
- `controlplane/docs/knowledge/reuse-registry.md`
- `controlplane/docs/knowledge/naming-conventions.md`
