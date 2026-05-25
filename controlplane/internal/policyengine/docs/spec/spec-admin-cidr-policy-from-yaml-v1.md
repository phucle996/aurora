# Spec: Admin CIDR Policy from YAML via PolicyEngine v1

## 1. Tổng quan
- Mục tiêu: bỏ nguồn `AdminAllowedCIDRs` từ config tĩnh, chuyển sang lấy từ policy YAML runtime qua `policyengine`.
- Bài toán: giảm restart/redeploy khi thay allowlist admin CIDR; cho SRE thay đổi bằng cập nhật policy file + hot reload.
- Scope: chỉ áp dụng cho luồng `admin_cidr` middleware; không thay đổi rate-limit semantics trong spec này.

## 2. Mục tiêu
- Runtime goals:
  - Admin CIDR allowlist đọc từ snapshot policy active trong RAM.
  - Không restart process khi thay CIDR list.
- Operational goals:
  - SRE cập nhật bằng YAML policy flow hiện có.
  - Có log/metric đủ để biết phiên bản CIDR policy đang enforce.
- Measurable targets:
  - p95 thời gian áp policy mới cho admin_cidr: `<= 10s` cross-instance.
  - config-drift (instance khác checksum) kéo dài quá 10 phút = alert.

## 3. Non-Goals
- Không thiết kế lại toàn bộ admin auth flow.
- Không thay đổi source-of-truth cho các config khác ngoài admin CIDR.
- Không mở endpoint runtime để chỉnh CIDR trực tiếp.

## 4. Thuật ngữ và định nghĩa
- `Admin CIDR policy`: tập CIDR được phép truy cập admin API.
- `Policy snapshot`: bản policy active hiện tại trong RAM của policyengine.
- `Enforce mode`: middleware chặn request ngoài allowlist.

## 5. Kiến trúc tổng thể
- Control plane:
  - SRE cập nhật policy YAML.
- Data plane:
  - `policyengine` reload YAML -> cập nhật snapshot active.
  - `admin_cidr` middleware đọc policy CIDR từ snapshot active (không đọc config tĩnh).
- Boundary:
  - `policyengine`: load/validate/sync snapshot.
  - `policyengine/config-yaml`: typed struct chứa schema YAML và mapping key -> field.
  - `middleware/admin_cidr`: enforce theo snapshot đã chuẩn hóa.
  - `app/module`: wiring dependency giữa middleware và policyengine service.

### 5.1 Typed YAML Config Model (bắt buộc)
- Tạo file model tại `internal/policyengine/runtime/configyaml/policies.go` để định nghĩa struct parse YAML.
- Mục tiêu:
  - Tránh parse trực tiếp bằng `map[string]interface{}` trong middleware.
  - Khóa contract field-level rõ như config hiện tại.
- Parse strategy (theo kiểu `internal/config/config.go`):
  - YAML được unmarshal vào typed struct trước khi đi tiếp pipeline xử lý.
  - Validation chạy trên typed struct (không validate trên map thô).
  - Caller/consumer chỉ nhận typed variable/runtime model đã compile.
- Source file runtime tại root project: `policies.yaml`.
- Nguyên tắc:
  - YAML parser gán vào typed struct trước.
  - Service validate typed struct.
  - Middleware chỉ dùng biến typed đã compile/normalize từ snapshot.
  - Cấm contract `map[string]interface{}` đi qua boundary caller -> middleware.

## 6. Luồng xử lý
1. Policy YAML mới được publish.
2. Policyengine reload thành công, snapshot mới active.
3. Middleware admin CIDR lấy `admin_cidr.allowlist` từ snapshot.
4. Request vào admin route:
   - resolve client IP theo trusted chain,
   - check thuộc allowlist CIDR hay không,
   - không thuộc -> reject.

### 6.1 YAML shape và parse contract (bắt buộc)
- Runtime source file: `policies.yaml`.
- Typed parse model: `internal/policyengine/runtime/configyaml/policies.go`.
- YAML tối thiểu:
  - `version: v1`
  - `policies.admin_cidr.enabled: bool`
  - `policies.admin_cidr.mode: enforce|monitor` (optional, default `enforce`)
  - `policies.admin_cidr.allowlist: []string` (CIDR list)

#### Ví dụ YAML đầy đủ (reference)
```yaml
version: v1
policies:
  admin_cidr:
    enabled: true
    mode: enforce
    allowlist:
      - 127.0.0.1/32
      - 10.0.0.0/8
      - 192.168.0.0/16
```

#### Mapping parse vào typed struct
- `version` -> `PoliciesFile.Version`
- `policies.admin_cidr` -> `PoliciesFile.Policies.AdminCIDR`
- `policies.admin_cidr.enabled` -> `AdminCIDRPolicy.Enabled`
- `policies.admin_cidr.mode` -> `AdminCIDRPolicy.Mode`
- `policies.admin_cidr.allowlist` -> `AdminCIDRPolicy.Allowlist`

### 6.2 Parse -> validate -> nạp biến runtime
- B1: Adapter đọc source meta (`path`, `mtime/version`, `size`) trước.
- B2: Nếu meta không đổi so với snapshot marker, skip read/parse để giảm I/O.
- B3: Nếu meta đổi, đọc bytes file và unmarshal vào typed struct `PoliciesFile`.
- B4: Validate typed struct:
  - `version == v1`
  - `enabled=true` => `allowlist` non-empty
  - mọi phần tử `allowlist` parse CIDR hợp lệ.
- B5: Compile ra runtime var/model cho middleware dùng:
  - giữ list string chuẩn hóa,
  - parse sẵn CIDR blocks để tránh parse lại ở hot path.
- B6: Atomic swap snapshot active.
- B7: Middleware chỉ dùng runtime var/model đã compile; không nhận map generic.

### 6.3 Boundary rule cho middleware
- `internal/http/middleware/admin_cidr` chỉ đổi nguồn input, không đổi logic enforce.
- Cấm truyền `map[string]interface{}` từ caller vào middleware.
- Caller chỉ truyền typed var/model đã qua parse+validate.

## 7. API Contract
> Không thêm HTTP API mới.

### 7.1 Internal Contract: Policy Keys
- Symbol/path: `policies.admin_cidr`
- Typed source struct (trong `internal/policyengine/runtime/configyaml`):
  - `AdminCIDRPolicy.Enabled bool    `yaml:"enabled"``
  - `AdminCIDRPolicy.Mode string     `yaml:"mode"``
  - `AdminCIDRPolicy.Allowlist []string `yaml:"allowlist"``
- YAML keys (required):
  - `enabled` (bool)
  - `allowlist` ([]string CIDR, bắt buộc non-empty)
- YAML keys (optional):
  - `mode` (`enforce|monitor`, default `enforce`)
- Invariants:
  - `allowlist` luôn phải non-empty (kể cả khi `enabled=false`, để tránh apply nhầm cấu hình rỗng).
  - tất cả phần tử `allowlist` phải parse được CIDR.
  - Caller không được truyền `map[string]interface{}` vào middleware/service API; chỉ truyền typed var/model.
- Error mapping:
  - policy invalid -> không activate snapshot mới (keep last-known-good).

### 7.2 Middleware Contract
- Symbol/path: `internal/http/middleware/admin_cidr`
- Input:
  - client IP đã resolve.
  - allowlist CIDR từ policy snapshot active.
- Output:
  - allow -> pass
  - deny -> HTTP 403 (generic message)
- Fail behavior:
  - khi policy unavailable: fail-closed cho admin critical routes.

## 8. Data Model
- Domain policy keys:
  - `policies.admin_cidr.enabled: bool`
  - `policies.admin_cidr.mode: string`
  - `policies.admin_cidr.allowlist: []string`
- Typed runtime model (khuyến nghị):
  - `CompiledAdminCIDRPolicy` chứa:
    - `Enabled bool`
    - `Mode string`
    - `AllowlistCIDR []string`
    - `AllowlistBlocks []netip.Prefix` (đã parse sẵn để middleware dùng trực tiếp)
- Ownership:
  - định nghĩa key ở policy docs/spec.
  - parser gán YAML -> typed struct trong `config-yaml`.
  - service compile typed struct -> runtime model.
  - middleware đọc runtime model, không đọc map thô.
  - caller boundary chỉ nhận/đẩy typed model, không pass-through map generic.

## 9. Reliability & Failure Handling
- Invalid CIDR list:
  - reject snapshot mới.
- Reload failure:
  - giữ `last-known-good`.
- Redis propagation lag:
  - middleware vẫn dùng snapshot local; convergence theo SLA policyengine.
- Dependency loss:
  - policyengine down ở startup -> fail-fast app bootstrap.

### 9.1 Race condition và kiểm soát
- Race A: poll trigger và Redis event trigger đồng thời.
  - Kiểm soát: service swap path tuần tự bằng lock; không có partial snapshot.
- Race B: file đang update dở (truncated/partial) lúc reload.
  - Kiểm soát: parse/validate fail => reject snapshot mới, giữ `last-known-good`.
- Race C: duplicate/out-of-order Redis events.
  - Kiểm soát: idempotent gate theo checksum/meta; unchanged thì skip.
- Race D: reload storm do burst trigger liên tiếp.
  - Kiểm soát: cooldown in-memory 5s sau reload thành công.
- Race E: middleware đọc đúng lúc snapshot swap.
  - Kiểm soát: copy/lock discipline; reader luôn thấy state nhất quán (cũ hoặc mới).
- Race F: cross-instance drift tạm thời.
  - Kiểm soát: event propagation + poll fallback; hội tụ theo SLA p95 <= 10s.

## 10. Security
- Client-facing error message generic, không leak allowlist details.
- Không log full CIDR list ở mức Info thường xuyên.
- Log chỉ checksum/version khi reload.

## 11. Scalability & Performance
- Hot path check mục tiêu:
  - CIDR lookup sau compile structure phải O(1)/gần O(1) theo set + CIDR blocks đã parse.
- Không parse CIDR trên từng request.
- Khi policy đổi, compile lại list một lần rồi swap runtime structure.
- Không truy cập `map[string]interface{}` trong hot path middleware.

## 12. SLO / SLA
- Admin CIDR decision latency overhead p95: `< 1ms`/request.
- Cross-instance policy convergence p95: `<= 10s`, p99 `<= 20s`.
- Availability policy read path: `99.95%`/tháng.

## 13. Source of Truth (SoT) by Caller
- Caller: `admin_cidr` middleware
  - SoT: policyengine active snapshot (`policies.admin_cidr`).
  - Fallback: last-known-good snapshot.
  - Conflict resolution: latest valid snapshot checksum wins.
- Caller: SRE
  - SoT: policy YAML + policyengine logs/metrics checksum.

## 14. Observability
- Metrics tối thiểu:
  - `policyengine_active_version_info{version,checksum}`
  - `policyengine_reload_total{result}`
  - `policyengine_propagation_lag_seconds`
- Logs quan trọng:
  - `policyengine.reload.success`
  - `policyengine.reload.failed`
  - `policyengine.source.degraded`
- Middleware deny metric (khuyến nghị):
  - `admin_cidr_denied_total{route}`

## 15. Deployment & Rollout
- B1: giữ config cũ + thêm read path từ policy (shadow compare).
- B2: bật enforce từ policy YAML cho 1 nhóm admin route.
- B3: remove config source cũ sau khi ổn định.
- Rollback:
  - re-apply policy checksum trước đó (last-known-good vẫn giữ).

## 16. Testing Strategy
- Unit:
  - validate key `admin_cidr` schema và CIDR parse.
  - parse YAML -> typed struct (`config-yaml`) đúng field mapping.
  - compile allowlist runtime structure từ typed struct.
- Integration:
  - policy reload -> middleware dùng list mới không restart.
  - deny/allow đúng theo CIDR list.
- Failure drills:
  - policy malformed CIDR -> snapshot không đổi.
  - propagation delay -> eventual convergence.
  - concurrent trigger (poll + event) -> không panic, không partial snapshot.
  - truncated file read -> reject new snapshot, middleware vẫn dùng last-known-good.

## 17. Open Questions
- Trusted proxy chain hiện tại của admin middleware lấy từ đâu và có cần đưa vào policy YAML cùng admin_cidr không?
- Rollout có cần phase `monitor` trước `enforce` cho toàn bộ admin routes không?
