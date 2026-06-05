# Controlplane Naming Conventions (AI Teaching Guide)

Mục tiêu: chuẩn hóa naming cho package, file, function, type, variable, constants để AI code không drift và không tạo tên mơ hồ.

## 1) Naming Principles

1. Rõ nghĩa hơn ngắn.
2. Tên phản ánh đúng boundary ownership.
3. Một khái niệm chỉ dùng một tên canonical trong cùng module.
4. Không viết tắt khó đoán.
5. Ưu tiên nhất quán với Go idioms + cấu trúc repo hiện tại.

## 2) Package Naming

## Rules

- Dùng lowercase, ngắn gọn, không underscore, không camelCase.
- Tên package phản ánh responsibility duy nhất.
- Không đặt package theo implementation detail tạm thời.
- Không dùng tên chung chung kiểu `util`, `common`, `helper` ở level module core.

## Patterns

- Domain contracts: `domain/entity`, `domain/repo`, `domain/service`
- App/service impl: `service`
- Repo impl: `repo_impl` (hoặc theo convention module đang dùng)
- Transport: `transport/http/handler`, `transport/http/dto`
- Shared constants: `pkg/constant`

## Do

- `ratelimit`, `observability`, `devicehint`, `apperr`

## Don't

- `utils`, `misc`, `tmp`, `newservice`

## 3) File Naming

## Rules

- snake_case, mô tả nội dung chính.
- 1 file nên có 1 trọng tâm chức năng.
- Tên file theo noun/feature, không theo trạng thái cá nhân.

## Patterns

- Handler: `<feature>_handler.go` (ví dụ: `auth_handler.go`)
- Service: `<feature>_service.go` (ví dụ: `refresh_token_service.go`)
- Repo: `<feature>_repo.go`
- Model: `<entity>.go` (ví dụ: `zone.go`, `secret.go`)
- Test: `<file_under_test>_test.go`

## Do

- `admin_api_key_auth.go`
- `origin_csrf.go`
- `accesslog.go` (đã canonical)

## Don't

- `final_handler.go`, `new_auth.go`, `abc.go`

## 4) Function Naming

## Rules

- Public function: PascalCase, bắt đầu bằng động từ hành động rõ.
- Private function: camelCase, động từ + object rõ nghĩa.
- Tránh tên quá generic: `Process`, `Handle`, `DoWork` nếu không có ngữ cảnh.
- Tên phải phản ánh effect chính và boundary.

## Verb Patterns

- Handler entrypoint: `HandleX`, hoặc pattern hiện hữu module (ví dụ method theo route handler).
- Middleware factory: `RateLimitPreAuth`, `RateLimitPostAuth`, `Access`, `AccessLog`.
- Builders/parsers: `buildRateLimitRules`, `parseRetryAfter`.
- Validators/checkers: `validateX`, `isXAllowed`.
- Mappers/converters: `toDomainEntity`, `toDBModel`.

## Signature Clarity Rules

- Tên params rõ nghĩa domain (`trackingDeviceID`, `routePattern`, `policyType`).
- Không dùng `data`, `info`, `obj` khi có thể cụ thể hơn.
- Nếu bool param khó hiểu, cân nhắc option/config struct.

## 5) Type / Interface Naming

## Rules

- Type là danh từ rõ domain.
- Interface mô tả capability, không mô tả implementation.
- Tránh hậu tố không cần thiết (`Manager`, `Processor`) nếu không thêm rõ nghĩa.

## Patterns

- Entity: `AuthSession`, `RefreshToken`, `RuntimeSecret`
- DTO: `LoginRequest`, `RotateKeyRequest`
- Result/contract: `EvaluateResult`, `StackedResult`
- Interface repo/service:
  - `AuthRepo`, `DeviceRepo`
  - `AuthService`, `RefreshTokenService`

## 6) Constants / Keys Naming

## Rules

- Exported constants: PascalCase theo domain prefix.
- Context/header/log keys phải có naming nhất quán và đặt ở canonical file.
- Không hardcode key string rải rác.

## Patterns

- Header key constants: trong `pkg/constant/http_header.go`
- Context keys: trong `pkg/constant/context_key.go`
- Cookie keys: trong `pkg/constant/cookie.go`

## 7) Variable Naming

## Rules

- Biến ngắn chỉ dùng cho scope rất hẹp (`i`, `err`, `ctx`).
- Domain variable phải rõ nghĩa (`userID`, `subjectKeyHash`, `retryAfter`).
- Tránh viết tắt mơ hồ (`tk`, `cfg2`, `tmpData`).

## 8) Error Naming

## Rules

- Error variable/type mô tả nguyên nhân nghiệp vụ/kỹ thuật.
- Public-facing error message generic khi liên quan security.
- Internal reason giữ ở structured log, không leak ra client.

## Patterns

- `ErrInvalidCredentials`
- `ErrRateLimitExceeded`
- `ErrRedisUnavailable`

## 9) Naming by Layer (Boundary-aware)

- Handler layer:
  - tên theo transport intent (`Login`, `Refresh`, `RotateKey`).
- Service layer:
  - tên theo business action (`AuthenticateUser`, `RotateAdminKey`).
- Repo layer:
  - tên theo persistence action (`FindByEmail`, `SaveSession`, `RevokeToken`).
- DB/model layer:
  - tên theo schema entity, không kéo transport term vào.

## 10) Anti-Patterns (cấm)

1. Một tên cho nhiều nghĩa khác nhau trong cùng module.
2. Tạo tên mới cho khái niệm đã có canonical name.
3. Hardcode key/header/cookie string trong middleware/handler khi đã có constant.
4. Dùng hậu tố `New`, `V2`, `Final` trong tên file/function để tránh conflict tạm thời.
5. Tên không thể suy ra layer ownership.

## 11) AI Checklist Before Proposing New Names

1. Tên này đã tồn tại canonical ở module/package chưa?
2. Tên có phản ánh đúng layer ownership không?
3. Có thể reuse constants/symbol hiện có thay vì tạo mới không?
4. Tên có rõ action/object/contract chưa?
5. Có gây mâu thuẫn với naming hiện tại trong repo không?

Nếu có bất kỳ câu trả lời "không chắc", phải tra:

- `controlplane/docs/knowledge/skill-knowledge-base.md`
- `controlplane/docs/knowledge/reuse-registry.md`
trước khi chốt tên.
