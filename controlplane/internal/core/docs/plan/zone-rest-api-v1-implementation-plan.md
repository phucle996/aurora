# Zone REST API V1 - Implementation Plan

## Mục tiêu

Plan này chuyển hóa từ:
- `controlplane/internal/core/docs/spec/zone-rest-api-v1-temp-spec.md`

Mục tiêu implement:
- API admin cho zone: create, update status, delete
- enforce state machine + delete pre-conditions
- bám đúng layering hiện tại của controlplane

---

## 1) Scope chốt

Trong scope:
- `POST /admin/core/zones`
- `PATCH /admin/core/zones/{zone_id}/status`
- `DELETE /admin/core/zones/{zone_id}`
- Validation, business rule, error mapping, logging ở handler layer
- Service logic cho transition + delete pre-condition
- Repository SQL cho zone + zone_services + dataplane_nodes dependency checks

Ngoài scope:
- không thêm API list/get zone ở phase này
- không thêm workflow runtime scheduler
- không thêm thay đổi ngoài module core liên quan zone REST

---

## 2) Danh sách file thay đổi chi tiết

## 2.1 Thêm mới file

### A. Domain contracts

1) `controlplane/internal/core/domain/repo_interface/zone_repo.go`
- Thêm interface repository cho zone.
- Hàm dự kiến:
  - `CreateZone(ctx, zone entity.Zone) error`
  - `GetZoneByID(ctx, id uuid.UUID) (*entity.Zone, error)`
  - `UpdateZoneStatus(ctx, id uuid.UUID, status entity.ZoneStatus) error`
  - `DeleteZone(ctx, id uuid.UUID) error`
  - `HasDataplaneNodesByZone(ctx, zoneID uuid.UUID) (bool, error)`
  - `HasEnabledZoneServicesByZone(ctx, zoneID uuid.UUID) (bool, error)`

2) `controlplane/internal/core/domain/service/zone_service.go`
- Thêm service contract cho zone REST use-cases.
- Hàm dự kiến:
  - `CreateZone(ctx, code, name string, status *entity.ZoneStatus) (*entity.Zone, error)`
  - `UpdateZoneStatus(ctx, zoneID string, status entity.ZoneStatus) (*entity.Zone, error)`
  - `DeleteZone(ctx, zoneID string) error`

### B. Entity/Model

3) `controlplane/internal/core/domain/entity/zone.go`
- Thêm entity `Zone` + enum `ZoneStatus`.
- Không thêm helper transition ở entity; transition logic xử lý trực tiếp trong service.

4) `controlplane/internal/core/model/zone.go`
- Thêm DB model cho `zones`.
- Thêm mapper entity<->model:
  - `ZoneEntityToModel(...)`
  - `ZoneModelToEntity(...)`

### C. Repository implementation (code guide theo từng func)

File: `controlplane/internal/core/repository/zone_repo.go`

Nguyên tắc chung cho tất cả func:
- nhận input từ service
- build SQL theo schema `core`
- execute query
- map DB error -> `core/errorx`
- trả kết quả về service

1) `CreateZone(ctx, zone entity.Zone) error`
- input: `entity.Zone`
- flow:
  - `ZoneEntityToModel(zone)`
  - `INSERT INTO zones (id, code, name, status, created_at, updated_at)`
  - map unique `zones.code` -> `ErrZoneCodeAlreadyExists`
- output: `nil` hoặc error

2) `GetZoneByID(ctx, id uuid.UUID) (*entity.Zone, error)`
- input: `id`
- flow:
  - `SELECT id, code, name, status, created_at, updated_at FROM zones WHERE id=$1 LIMIT 1`
  - scan vào `model.Zone`
  - `ZoneModelToEntity(model)`
- output:
  - có row -> `*entity.Zone`
  - không có row -> `nil, nil`

3) `UpdateZoneStatus(ctx, id uuid.UUID, status entity.ZoneStatus) error`
- input: `id`, `status`
- flow:
  - `UPDATE zones SET status=$2, updated_at=now() WHERE id=$1`
  - check `rows_affected`
- output:
  - `rows_affected=0` -> `ErrZoneNotFound`
  - ngược lại -> `nil`

4) `DeleteZone(ctx, id uuid.UUID) error`
- input: `id`
- flow:
  - `DELETE FROM zones WHERE id=$1`
  - check `rows_affected`
- output:
  - `rows_affected=0` -> `ErrZoneNotFound`
  - ngược lại -> `nil`

5) `HasDataplaneNodesByZone(ctx, zoneID uuid.UUID) (bool, error)`
- input: `zoneID`
- flow:
  - `SELECT EXISTS(SELECT 1 FROM dataplane_nodes WHERE zone_id=$1)`
- output: `true/false`

6) `HasEnabledZoneServicesByZone(ctx, zoneID uuid.UUID) (bool, error)`
- input: `zoneID`
- flow:
  - `SELECT EXISTS(SELECT 1 FROM zone_services WHERE zone_id=$1 AND enabled=true)`
- output: `true/false`

### D. Service implementation

6) `controlplane/internal/core/service/zone_service.go`
- Business logic:
  - normalize/validate input create
  - apply default status `active` khi create không truyền status
  - validate UUID zone_id cho update/delete
  - enforce state machine
  - enforce delete pre-condition:
    - zone.status == disabled
    - has dataplane_nodes == false
    - has enabled zone_services == false

### E. Transport (HTTP admin)

7) `controlplane/internal/core/transport/http/dto/req/zone_request.go`
- Request DTO cho:
  - create zone
  - update zone status

8) `controlplane/internal/core/transport/http/handler/zone_handler.go`
- Handler admin cho 3 endpoint.
- Bind/validate request.
- Call zone service.
- Map errors -> 400/404/409/500.
- Log ở handler layer theo convention hệ thống.

9) `controlplane/internal/core/route.go` (hoặc file route module core tương đương)
- Register route admin:
  - `POST /admin/core/zones`
  - `PATCH /admin/core/zones/:zone_id/status`
  - `DELETE /admin/core/zones/:zone_id`

### F. Module wiring

10) `controlplane/internal/core/module.go`
- Wiring fail-fast:
  - validate cfg/db dependencies
  - init zone repo
  - init zone service
  - init zone handler

### G. Error contract

11) `controlplane/internal/core/errorx/error.go`
- Thêm block lỗi zone trong cùng file (không tạo file mới):
  - `ErrZoneInvalidInput`
  - `ErrZoneCodeAlreadyExists`
  - `ErrZoneNotFound`
  - `ErrZoneInvalidTransition`
  - `ErrZoneDeletePreconditionFailed`

### H. Tests

12) `controlplane/internal/core/test/svc_test/zone_service_test.go`
- Unit test service logic.

13) `controlplane/internal/core/test/repo_test/zone_repo_test.go` (hoặc integration style hiện có)
- Test repo query behavior (unique conflict, counts, update/delete).

14) `controlplane/internal/core/test/transport_test/zone_handler_test.go`
- Test mapping HTTP status/body theo error contract.

---

## 2.2 Sửa file hiện có

1) `controlplane/internal/app/module.go`
- Nếu core module được global compose tại đây:
  - expose handler/route dependencies mới của core zone API.

2) `controlplane/internal/app/route.go`
- Nếu route core bind ở tầng global:
  - mount route admin core zones vào router chính.

3) `controlplane/internal/core/docs/flow/...` (optional)
- Nếu cần thêm flow tài liệu zone API sau khi code chạy ổn.

---

## 3) Mapping spec -> implementation rules

## 3.1 Create zone

- enforce unique code
- normalize code lowercase+trim
- default status `active` nếu request không truyền
- response `201`

## 3.2 Update status

- validate `zone_id` UUID
- validate status enum
- enforce transition table:
  - active -> draining|maintenance|disabled
  - draining -> active|maintenance|disabled
  - maintenance -> active|disabled
  - disabled -> active
- response `200`

## 3.3 Delete zone

Chỉ delete khi đồng thời:
- status == disabled
- không còn dataplane_nodes
- không còn zone_services enabled

Fail bất kỳ điều kiện nào -> `409`

---

## 4) Error mapping chi tiết

- `ErrZoneInvalidInput` -> `400`
- `ErrZoneNotFound` -> `404`
- `ErrZoneCodeAlreadyExists` -> `409`
- `ErrZoneInvalidTransition` -> `400` (hoặc `409`, chốt 1 giá trị nhất quán)
- `ErrZoneDeletePreconditionFailed` -> `409`
- unknown -> `500`

---

## 5) Trình tự triển khai đề xuất

1. Thêm entity/model/repo interface zone.
2. Implement repository SQL.
3. Implement service logic + rule state machine/delete preconditions.
4. Thêm errorx constants và map lỗi.
5. Thêm handler + dto + routes.
6. Wiring module/global route.
7. Viết test svc/repo/transport.
8. Chạy test core + app route smoke.

---

## 6) Acceptance checklist

- 3 endpoint admin zone hoạt động đúng contract spec.
- Unique `code` enforced.
- Transition policy enforced.
- Delete pre-condition enforced (`disabled` + no nodes + no enabled services).
- Error mapping HTTP đúng.
- Test pass cho core service/repo/handler.


## D) Kế hoạch test chi tiết

### D.1 Service tests (`internal/core/test/svc_test/zone_service_test.go`)
- `CreateZone`:
  - tạo mới thành công
  - status default `active`
  - invalid input -> `ErrZoneInvalidInput`
  - duplicate code -> `ErrZoneCodeAlreadyExists`
- `UpdateZoneStatus`:
  - transition hợp lệ pass
  - transition không hợp lệ -> `ErrZoneInvalidTransition`
  - zone không tồn tại -> `ErrZoneNotFound`
- `DeleteZone`:
  - pass khi `disabled` + no node + no enabled service
  - fail pre-condition từng nhánh -> `ErrZoneDeletePreconditionFailed`

### D.2 Repository tests (`internal/core/test/repo_test/zone_repo_test.go`)
- `CreateZone` map unique violation.
- `GetZoneByID` not found trả nil.
- `UpdateZoneStatus` rows=0 -> `ErrZoneNotFound`.
- `DeleteZone` rows=0 -> `ErrZoneNotFound`.
- `HasDataplaneNodesByZone` và `HasEnabledZoneServicesByZone` đủ true/false case.

### D.3 Handler tests (`internal/core/test/transport_test/zone_handler_test.go`)
- map status code:
  - 201/200 success
  - 400 invalid request
  - 404 not found
  - 409 conflict
  - 500 internal
- verify không set field ngoài contract response.
