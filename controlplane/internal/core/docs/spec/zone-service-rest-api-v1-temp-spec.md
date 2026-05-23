# Zone Service REST API V1 (Temp Spec)

## 1) Mục tiêu

Đặc tả API RESTful cho quản trị `zone_services` trong core:
- xem trạng thái service theo zone
- bật/tắt service theo zone
- đảm bảo rule an toàn vận hành khi đổi trạng thái service

Tài liệu này chỉ mô tả contract và hành vi nghiệp vụ API, không mô tả thay đổi file/code.

---

## 2) Phạm vi V1

Trong scope:
- API quản trị service theo zone:
  - List zone services
  - Upsert trạng thái service theo zone
- Validation + business rule cho trạng thái `enabled`
- Error mapping ở mức API contract

Ngoài scope:
- Không mô tả implementation layering/repo/service
- Không mô tả migration changelist (đã ở spec migration)
- Không mô tả scheduler/rebalance runtime logic
- Không có bulk endpoint trong V1

---

## 3) Resource model

Resource chính:
- `ZoneService`
  - `id` (uuid v7)
  - `zone_id` (uuid v7)
  - `service_type` (`mail | hypervisor | k8s | ai`)
  - `enabled` (boolean)
  - `created_at`
  - `updated_at`

Nguồn dữ liệu:
- bảng `zone_services` theo `zone-migration-v1-temp-spec.md`

---

## 4) Endpoint contract

Base path admin-only:
- `/admin/core`

### 4.1 List Zone Services

- **Method:** `GET`
- **Path:** `/admin/core/zones/{zone_id}/services`

#### Rules
- `zone_id` phải là UUID hợp lệ.
- zone phải tồn tại.

#### Success
- `200 OK`

#### Errors
- `400` invalid `zone_id`
- `404` zone not found
- `500` internal error

---

### 4.2 Upsert Zone Service State

- **Method:** `PUT`
- **Path:** `/admin/core/zones/{zone_id}/services`
- **Body:**
```json
{
  "service_type": "mail",
  "enabled": true
}
```

#### Rules
- `zone_id` UUID hợp lệ (validate ở handler).
- `service_type` thuộc enum `zone_service_type` (validate ở handler).
- `enabled` bắt buộc bool (validate ở handler).
- zone phải tồn tại.
- nếu chưa có row `(zone_id, service_type)` thì tạo mới.
- nếu đã có row thì update `enabled`, `updated_at`.

#### Business safety rules V1 (chốt)
Chỉ cho phép update `zone_services` khi `zone.status = maintenance`.

Rule theo trạng thái zone:
- `maintenance`: cho phép bật/tắt service.
- `active`: không cho phép bật/tắt service.
- `draining`: không cho phép bật/tắt service.
- `disabled`: không cho phép bật/tắt service.

Khi zone không ở `maintenance`, API phải reject với `409 state conflict`.

#### Success
- `200 OK`

#### Errors
- `400` invalid request / invalid enum
- `404` zone not found
- `409` state conflict
- `500` internal error

---

## 5) Quan hệ với Zone lifecycle

Ràng buộc đồng bộ với `zone-rest-api-v1-temp-spec.md`:
- Delete zone chỉ hợp lệ khi:
  1) `zone.status = disabled`
  2) không còn `dataplane_nodes` tham chiếu
  3) tất cả `zone_services.enabled = false`

Zone service API phải đảm bảo dữ liệu `zone_services` nhất quán để phục vụ pre-condition delete này.

---

## 6) Error contract

Nhóm lỗi đề xuất cho core zone service API:
- `ErrZoneServiceInvalidInput`
- `ErrZoneServiceZoneNotFound`
- `ErrZoneServiceInvalidType`
- `ErrZoneServiceStateConflict`

Mapping HTTP:
- `ErrZoneServiceInvalidInput` -> `400`
- `ErrZoneServiceZoneNotFound` -> `404`
- `ErrZoneServiceInvalidType` -> `400`
- `ErrZoneServiceStateConflict` -> `409`

---

## 7) Security và governance

- API admin-only (`/admin/core/...`), yêu cầu authN/authZ admin scope.
- Không trả lỗi lộ chi tiết internals DB.
- Có audit event cho thay đổi trạng thái zone service (phase implement).

---

## 8) Acceptance criteria

- Có API xem service theo zone.
- Có API upsert bật/tắt theo `service_type` lấy từ body DTO.
- Enforce rule: chỉ `maintenance` mới được update zone service.
- `active/draining/disabled` phải reject `409` khi request update.
- Dữ liệu `zone_services` nhất quán để phục vụ rule delete zone.
- Response format thống nhất envelope controlplane hiện tại.

---

## 9) Phụ thuộc spec

- `controlplane/internal/core/docs/spec/zone-migration-v1-temp-spec.md`
- `controlplane/internal/core/docs/spec/zone-rest-api-v1-temp-spec.md`


## 10) Test coverage yêu cầu (chuẩn bị implement)

### 10.1 Service tests
- `ListZoneServices`:
  - zone tồn tại -> trả list
  - zone không tồn tại -> not found
  - zone_id invalid -> invalid input
- `UpsertZoneService`:
  - zone `maintenance` -> upsert pass
  - zone `active/draining/disabled` -> `state conflict`
  - service_type invalid -> invalid type
  - zone không tồn tại -> not found

### 10.2 Repository tests
- `ListZoneServicesByZoneID` trả đúng list theo zone.
- `UpsertZoneServiceByZoneAndType`:
  - insert path
  - update path (conflict key zone_id+service_type)
  - trả lại row mới nhất

### 10.3 Handler/transport tests
- `GET /admin/core/zones/{zone_id}/services`:
  - 200 / 400 / 404 / 500
- `PUT /admin/core/zones/{zone_id}/services`:
  - 200 / 400 / 404 / 409 / 500
- validate ở handler:
  - zone_id format
  - service_type enum
  - enabled bool

### 10.4 Contract consistency checks
- Không có bulk endpoint trong V1.
- service_type phải đi qua DTO body.
- Rule maintenance-only được enforce nhất quán service+handler.
