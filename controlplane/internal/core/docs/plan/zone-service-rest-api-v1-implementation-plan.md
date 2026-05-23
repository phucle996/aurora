# Zone Service REST API V1 - Implementation Plan

## A) Mục tiêu implement

Implement API quản trị `zone_services` theo spec:
- `GET /admin/core/zones/:zone_id/services`
- `PUT /admin/core/zones/:zone_id/services` (DTO body chứa `service_type`, `enabled`)

Rule chốt:
- chỉ cho phép update `zone_services` khi `zone.status = maintenance`
- `active/draining/disabled` phải reject `409 state conflict`

---

## B) Nguyên tắc triển khai

- Không tạo file layer mới cho zone service.
- Mở rộng trực tiếp bộ file zone đang có ở từng layer.
- Handler validate input format/enum; service xử lý business rule.

---

## C) Danh sách thay đổi file + function cụ thể

1) `controlplane/internal/core/domain/entity/zone.go`
- Mở rộng entity zone hiện có:
  - `type ZoneServiceType string`
  - enum values: `mail | hypervisor | k8s | ai`
  - `type ZoneService struct { ... }`

2) `controlplane/internal/core/model/zone.go`
- Mở rộng model zone hiện có:
  - `type ZoneService struct { ... }`
- Thêm mapper:
  - `ZoneServiceEntityToModel(...)`
  - `ZoneServiceModelToEntity(...)`

3) `controlplane/internal/core/domain/repo/zone_repo.go`
- Mở rộng `ZoneRepository` hiện có:
  - `ListZoneServicesByZoneID(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error)`
  - `UpsertZoneServiceByZoneAndType(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error)`

4) `controlplane/internal/core/repository/zone_repo.go`
- Implement thêm vào `ZoneRepoImpl`:
  - `ListZoneServicesByZoneID(...)`
  - `UpsertZoneServiceByZoneAndType(...)`

5) `controlplane/internal/core/domain/service/zone_service.go`
- Mở rộng `ZoneService` interface hiện có:
  - `ListZoneServices(ctx context.Context, zoneID string) ([]coreEntity.ZoneService, error)`
  - `UpsertZoneService(ctx context.Context, zoneID string, serviceType string, enabled bool) (*coreEntity.ZoneService, error)`

6) `controlplane/internal/core/service/zone_service.go`
- Implement thêm methods:
  - `ListZoneServices(...)`
  - `UpsertZoneService(...)`
- Business rule:
  - zone tồn tại
  - chỉ cho phép update khi `zone.status == maintenance`
  - ngược lại trả `ErrZoneServiceStateConflict`

7) `controlplane/internal/core/transport/http/dto/req/zone_request.go`
- Mở rộng DTO zone request hiện có:
  - `type UpsertZoneServiceRequest struct { ServiceType string `json:"service_type"`; Enabled bool `json:"enabled"` }`

8) `controlplane/internal/core/transport/http/handler/zone_handler.go`
- Mở rộng handler zone hiện có:
  - `ListZoneServices(c *gin.Context)`
  - `UpsertZoneService(c *gin.Context)`
- Validate ở handler:
  - `zone_id` UUID hợp lệ
  - `service_type` enum hợp lệ
  - body bind hợp lệ

9) `controlplane/internal/core/errorx/errorx.go`
- Thêm errors:
  - `ErrZoneServiceInvalidInput`
  - `ErrZoneServiceZoneNotFound`
  - `ErrZoneServiceInvalidType`
  - `ErrZoneServiceStateConflict`

10) `controlplane/internal/core/route.go`
- Thêm routes:
  - `GET /admin/core/zones/:zone_id/services`
  - `PUT /admin/core/zones/:zone_id/services`

11) `controlplane/internal/core/module.go`
- Dùng chung `ZoneRepository`, `ZoneService`, `ZoneHandler` hiện có.
- Không thêm object layer mới, chỉ dùng methods mới.

---

## D) Rule implement theo từng function

1) `ZoneHandler.UpsertZoneService(...)`
- bind body DTO `service_type`, `enabled`
- validate `zone_id` + `service_type`
- call service
- map errors: `400/404/409/500`

2) `ZoneService.UpsertZoneService(...)`
- check zone tồn tại
- enforce `zone.status == maintenance`
- call repo upsert

3) `ZoneRepoImpl.UpsertZoneServiceByZoneAndType(...)`
- dùng `INSERT ... ON CONFLICT (zone_id, service_type) DO UPDATE ... RETURNING ...`

---

## E) Test cập nhật

- Mở rộng test zone hiện có (không tạo bộ layer test mới):
  - svc test: maintenance-only rule
  - repo test: list + upsert zone_services
  - handler test: validate + mapping `400/404/409`

---

## F) Acceptance checklist

- [ ] Không có bulk endpoint trong V1.
- [ ] `service_type` nhận qua DTO body.
- [ ] Chỉ `maintenance` được update zone service.
- [ ] `active/draining/disabled` reject `409`.
- [ ] Logic nằm trong bộ file zone hiện có.
- [ ] Test pass cho các nhánh chính.


## G) Kế hoạch test chi tiết

### G.1 Service tests (`internal/core/test/svc_test/zone_service_zone_services_test.go`)
- `ListZoneServices`:
  - zone tồn tại -> list success
  - zone_id invalid -> `ErrZoneServiceInvalidInput`
  - zone không tồn tại -> `ErrZoneServiceZoneNotFound`
- `UpsertZoneService`:
  - `maintenance` pass
  - `active/draining/disabled` -> `ErrZoneServiceStateConflict`
  - invalid type -> `ErrZoneServiceInvalidType`

### G.2 Repository tests (`internal/core/test/repo_test/zone_service_repo_test.go`)
- `ListZoneServicesByZoneID` true path.
- `UpsertZoneServiceByZoneAndType` insert path.
- `UpsertZoneServiceByZoneAndType` update path (same zone_id+service_type).

### G.3 Handler tests (`internal/core/test/transport_test/zone_service_handler_test.go`)
- `GET /admin/core/zones/:zone_id/services`:
  - 200 / 400 / 404 / 500 mapping
- `PUT /admin/core/zones/:zone_id/services`:
  - 200 / 400 / 404 / 409 / 500 mapping
- verify handler validate:
  - `zone_id` UUID
  - `service_type` enum trong body
  - bind request body hợp lệ

### G.4 Regression checks
- Không ảnh hưởng zone CRUD đã có.
- Route mới không đè route cũ.
- Error envelope giữ chuẩn hiện hành.
