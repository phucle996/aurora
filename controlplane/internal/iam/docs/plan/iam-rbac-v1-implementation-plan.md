# IAM RBAC V1 - Implementation Plan

## 1) Mục tiêu triển khai

Triển khai RBAC V1 cho IAM theo bộ spec:
- `internal/iam/docs/spec/iam-rbac-v1-spec.md`
- `internal/iam/docs/spec/iam-rbac-cache-sync-v1-spec.md`
- `internal/iam/docs/spec/iam-rbac-admin-mutation-v1-spec.md`

Done definition:
- Authorization đi theo permission-first, deny-by-default.
- Có đủ role/permission/binding mutation path với audit + invalidate.
- Cache sync đa replica có invalidate bus + periodic self-heal.
- Có metrics/log đủ để vận hành production.

Out-of-scope:
- ABAC/ReBAC.
- Multi-tenant fine-grain policy engine.
- UI admin console hoàn chỉnh.

---

## 2) Current state vs target state

**Current state**
- IAM đã có auth flows và critical guards nhưng AuthZ chưa chuẩn hóa theo RBAC source-of-truth xuyên suốt.
- Chưa có bộ contract/implementation đầy đủ cho role/permission/binding + cache sync lifecycle trong module.
- Observability cho RBAC chưa được chuẩn hóa theo metric set production.

**Target state**
- Có RBAC subsystem độc lập trong IAM: domain/repo/service/transport rõ layer.
- Authorization middleware gọi RBAC service theo permission string.
- Mutation RBAC tự động invalidate cache local + publish invalidation liên instance.
- Có worker sync định kỳ để self-heal khi miss event bus.

---

## 3) Implementation changes (grouped by subsystem)

### Domain
**Files (THÊM/SỬA)**
- `internal/iam/domain/entity/rbac.go`
- `internal/iam/domain/repo/rbac_repo.go`
- `internal/iam/domain/service/rbac_service.go`

| Function/Type | Change | Before | After | Impact |
|---|---|---|---|---|
| RBAC entities | SỬA | Chưa chuẩn hóa đủ cho V1 contract | Chuẩn hóa `Role`, `Permission`, `RolePermission`, `PrincipalRole`, `RoleEntry` | Domain model thống nhất |
| RBAC repository interface | SỬA | Thiếu một số contract mutation/sync | Chốt đầy đủ read/write contract cho authorize + mutation | Giảm drift service-repo |
| RBAC service interface | SỬA | Thiếu contract outcome rõ | Chuẩn hóa `Authorize`, mutation methods, invalidate/warmup hooks | Rõ behavior và testability |

### Repository
**Files (SỬA/THÊM)**
- `internal/iam/repository/rbac_repo.go`
- (nếu thiếu) migration RBAC liên quan trong `internal/iam/migrations/*`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `ListRoleEntries(...)` | SỬA | Chưa tối ưu cho warmup/cache-aside | Trả role + permissions phục vụ authorize path | Giảm query phân mảnh |
| `GetRoleByName(...)` | SỬA | Thiếu chuẩn lỗi rõ | Chuẩn hóa lỗi not-found/system-fail | Map deny/error rõ hơn |
| Mutation methods | SỬA | Chưa đồng bộ toàn bộ operation spec | V1 hiện tại hoàn chỉnh create/update/delete role, assign/revoke permission, assign/revoke role ở `platform` scope; tenant/workspace assignment triển khai phase tiếp theo | Tránh hiểu nhầm phạm vi đã ship |
| Tx boundaries | THÊM | Chưa chốt rõ atomicity | Chốt transaction cho mutation quan hệ | Tránh partial writes |

### Service
**Files (SỬA/THÊM)**
- `internal/iam/service/rbac_service.go`
- `internal/iam/service/role_registry.go`
- `internal/iam/service/rbac_permission_cache.go`
- `internal/iam/service/rbac_cache_bus.go`
- `internal/iam/service/rbac_cache_sync.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `Authorize(...)` | THÊM/SỬA | Chưa chốt outcome đầy đủ | Trả `allow|deny|error` + reason nội bộ | Đồng nhất behavior runtime |
| Cache-aside role resolve | SỬA | Chưa đủ metric/reason | Check local -> shared -> DB -> populate | Tối ưu hot path authorize |
| Mutation orchestration | SỬA | Invalidate semantics chưa chuẩn hóa | Commit DB -> invalidate -> publish event -> audit | HA consistency tốt hơn |
| `RbacCacheSync.Start/Stop` | SỬA | Lifecycle chưa chốt theo module | Tick 30s + jitter nhỏ + self-heal theo epoch/version | Ổn định multi-replica |
| Error taxonomy | THÊM | Chưa phân loại rõ deny/error/cache_degraded | Chuẩn hóa reason cho logs/metrics | Quan sát sự cố nhanh hơn |

### Transport (HTTP)
**Files (SỬA/THÊM)**
- `internal/iam/transport/http/handler/rbac_handler.go`
- `internal/iam/transport/http/request/rbac_request.go`
- `internal/iam/route.go`
- middleware authz liên quan (nếu cần)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| RBAC admin endpoints | SỬA | Chưa chuẩn theo spec contract | Chốt input validate + error mapping + generic response | API nhất quán |
| Permission guard integration | THÊM/SỬA | Check quyền chưa permission-first toàn diện | Route/middleware check theo permission string | Tách role khỏi handler logic |
| Error mapping | SỬA | Chưa đồng nhất deny/system error | `403` deny, `5xx` system, generic message | Không leak nội bộ |

### Module wiring + lifecycle
**Files (SỬA)**
- `internal/iam/module.go`
- `internal/iam/bootstrap.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Module init | SỬA | RBAC components chưa wire đủ | Wire repo/service/registry/bus/cache-sync theo config | Sẵn sàng runtime |
| Module Bootstrap | SỬA | Chưa start đầy đủ sync worker | Start `RbacCacheSync` trong bootstrap lifecycle | Đồng bộ cache liên instance |
| Module Stop | SỬA | Stop path chưa rõ ràng | Stop sync worker sạch khi shutdown | Tránh goroutine leak |

### Observability
**Files (THÊM/SỬA)**
- `internal/iam/metrics/rbac.go` (THÊM)
- `internal/iam/metrics/module_register.go` (SỬA)
- logging call-sites trong service/sync loop (SỬA)

| Metric | Change | Purpose |
|---|---|---|
| `iam_rbac_authorize_total{result}` | THÊM | Theo dõi allow/deny/error |
| `iam_rbac_authorize_deny_total{reason}` | THÊM | Phân tích deny theo nguyên nhân |
| `iam_rbac_cache_hit_total{layer}` | THÊM | Theo dõi hiệu quả cache |
| `iam_rbac_cache_miss_total{layer}` | THÊM | Cảnh báo áp lực DB |
| `iam_rbac_invalidation_total{kind,result}` | THÊM | Theo dõi invalidate path |
| `iam_rbac_sync_total{result}` | THÊM | Theo dõi sức khỏe sync worker |

### Docs
**Files (SỬA/THÊM)**
- `internal/iam/docs/flow/*` RBAC flow docs (THÊM)
- `internal/iam/docs/runbook/iam-rbac-cache-sync-incident.md` (THÊM)

| Document | Change | Impact |
|---|---|---|
| RBAC runtime flow | THÊM | Làm rõ authorize/mutate/invalidate sequence |
| RBAC cache incident runbook | THÊM | Hướng dẫn xử lý cache drift/bus failure |

---

## 4) Contract changes

- Bổ sung RBAC permission contract nội bộ cho route guard.
- Bổ sung/chuẩn hóa RBAC admin mutation endpoints theo handler hiện hữu.
- Chuẩn hóa outcome mapping:
  - `deny` -> `403`,
  - `system error` -> `5xx`.
- Bổ sung lifecycle contract: RBAC sync worker start/stop cùng IAM module.

---

## 5) Test plan + acceptance

### Required tests

**Service unit tests**
- `Authorize`:
  - allow khi permission match,
  - deny khi thiếu permission,
  - error khi DB/cache dependency fail.
- Mutation:
  - commit thành công -> invalidate + publish,
  - publish fail -> mutation vẫn commit + metric/log đúng.

**Cache sync tests**
- Nhận event `role` invalidate đúng scope.
- Nhận event `all` invalidate full local.
- Miss event nhưng epoch tăng -> self-heal invalidate.
- Stop signal dừng loop sạch.

**Repository integration tests**
- CRUD role/permission/binding đúng transaction semantics.
- Query role entry trả đúng permission set.

**Transport tests**
- RBAC admin endpoint validation/error mapping.
- Permission guard trả `403` generic khi deny.

### Acceptance checklist
- [ ] Authorization chạy permission-first, deny-by-default.
- [ ] Handler không hardcode role string để authorize action.
- [ ] Mutation RBAC có audit + invalidate + publish theo contract.
- [ ] Scope invariant được chốt ở spec: `workspace` luôn phải có `tenant`.
- [ ] V1 runtime chỉ bật assignment `platform` scope; tenant/workspace assignment là phase rollout sau.
- [ ] Replica tự-heal khi miss invalidate event.
- [ ] Metrics/log đủ cho vận hành production.
- [ ] IAM module shutdown không còn goroutine sync treo.

---

## 6) Rollout & operations

### Rollout strategy
- Phase 1: ship RBAC core + metrics + shadow checks (không block route cũ).
- Phase 2: bật enforcement từng nhóm route critical.
- Phase 3: mở rộng toàn bộ admin actions.

### Backward compatibility
- Trong phase chuyển tiếp, giữ guard cũ song song RBAC check ở chế độ monitor nếu cần.
- Khi confidence đủ cao, chuyển guard cũ về fallback tối thiểu.

### Operational signals
- Alert khi:
  - authorize error rate vượt ngưỡng,
  - cache miss ratio tăng bất thường,
  - sync failures liên tục,
  - epoch drift kéo dài.

### Runbook baseline
- Có runbook cho các tình huống:
  - Redis pub/sub unavailable,
  - cache stale nghi ngờ,
  - deny spike sau deploy policy,
  - rollback enforcement nhanh.

---

## 7) Risks & mitigations

- Risk: permission taxonomy bùng nổ, khó governance.
  - Mitigation: permission registry + review gate khi thêm permission mới.

- Risk: invalidate all quá nhiều gây DB burst.
  - Mitigation: ưu tiên invalidate granular + jitter sync + tuning TTL.

- Risk: route migrate dở dang gây inconsistent authz.
  - Mitigation: rollout theo phase + checklist route coverage.

- Risk: Redis degrade làm sync chậm.
  - Mitigation: fallback DB read + epoch self-heal + alert nhanh.

- Risk: deny spike ảnh hưởng vận hành.
  - Mitigation: canary enforcement + monitor deny reason + fast rollback switch.
