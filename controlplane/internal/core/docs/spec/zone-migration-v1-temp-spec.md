# Zone Migration V1 (Temp Spec)

## 1) Mục tiêu

Đặc tả migration cho `zone` trong `core`:
- định nghĩa bảng zone làm source-of-truth
- đảm bảo dữ liệu zone nhất quán cho runtime decisions

Đây là spec migration boundary, không phải implementation plan.

---

## 2) Phạm vi V1

Trong scope:
- thêm bảng `zones` (danh mục độc lập)
- index phục vụ query runtime theo zone

Ngoài scope:
- không thêm logic scheduler/rebalance
- không thêm API CRUD zone
- không thêm dữ liệu runtime metrics

---

## 3) Bảng `zones` (bắt buộc)

Mục đích:
- quản lý danh mục zone hợp lệ trong hệ thống

Columns v1:
- `id` (uuid v7, PK)
- `code` (text, unique, not null)  -- ví dụ: `edge-hcm-1`
- `name` (text, not null)           -- ví dụ: `HCM Edge DC-1`
- `status` (enum `zone_status`, not null)
- `created_at` (timestamptz, not null)
- `updated_at` (timestamptz, not null)

Ghi chú:
- `code` là business key ổn định để vận hành.
- `id` là PK nội bộ cho FK.


## 3.1 `zone_services` (bắt buộc)

Mục đích:
- quản lý trạng thái bật/tắt từng loại dịch vụ theo từng zone.
- làm nguồn dữ liệu để hiển thị cho người dùng zone đó đang hỗ trợ service nào.

Columns v1:
- `id` (uuid v7, PK)
- `zone_id` (uuid v7, FK -> zones.id, not null)
- `service_type` (enum `zone_service_type`, not null)
- `enabled` (boolean, not null default false)
- `created_at` (timestamptz, not null)
- `updated_at` (timestamptz, not null)

Unique constraint:
- unique `(zone_id, service_type)`

---

## 4) Enum cho zone

`zone_status`:
- `active`
- `draining`
- `maintenance`
- `disabled`

Semantics:
- `active`: zone nhận workload bình thường
- `draining`: zone không nhận assignment mới
- `maintenance`: tạm ngưng phục vụ theo kế hoạch
- `disabled`: ngừng hoạt động

---

## 5) Quan hệ zone và dataplane

Nguyên tắc ownership:
- `zone` là danh mục độc lập, không phụ thuộc vào dataplane.
- `dataplane` phải biết zone, và bắt buộc thuộc đúng **1 zone**.

Trong `dataplane_nodes`:
- không dùng `zone` text trực tiếp
- dùng `zone_id` (uuid v7, **not null**)
- FK: `zone_id -> zones.id`

Lý do:
- tránh sai chính tả zone text
- đảm bảo referential integrity
- thuận lợi query/join theo zone

---

## 6) Migration strategy v1

## 6.1 Enum migration
- thêm enum `zone_status` vào `000001_core_enums.up.sql`
- thêm enum `zone_service_type` vào `000001_core_enums.up.sql`

## 6.2 Table migration
- thêm bảng `zones` trong `000002_core_tables.up.sql`
- thêm bảng `zone_services` trong `000002_core_tables.up.sql`
- chỉnh `dataplane_nodes`:
  - thêm `zone_id` (`NOT NULL`)
  - thêm FK `dataplane_nodes_zone_id_fkey`

## 6.3 Index migration
Bắt buộc:
- unique index `zones(code)`
- index `zones(status)`
- unique index `zone_services(zone_id, service_type)`
- index `zone_services(zone_id, enabled)`
- index `dataplane_nodes(zone_id)`
- composite index `dataplane_nodes(status, zone_id)` (phục vụ chọn node theo zone+status)

---

## 7) Dữ liệu seed tối thiểu

V1 có thể seed zone cơ bản (optional):
- `edge-default` (active)

Nếu chưa seed ở migration thì yêu cầu bootstrap runtime tạo zone mặc định trước khi đăng ký dataplane.

---

## 8) Backward compatibility / transition

Target schema v1 yêu cầu `dataplane_nodes.zone_id` là `NOT NULL` (mỗi dataplane thuộc đúng 1 zone).

Nếu có dữ liệu cũ dùng `zone` text, migration cần có bước map đầy đủ sang `zones.id` trước khi áp ràng buộc `NOT NULL`.

Nếu chưa có dữ liệu cũ, áp trực tiếp schema mới với `zone_id` bắt buộc.

---

## 9) Acceptance criteria

- Có enum `zone_status`.
- Có bảng `zones` + unique `code`.
- Có bảng `zone_services` để bật/tắt service theo zone.
- `dataplane_nodes` tham chiếu `zone_id` bằng FK và `zone_id` là bắt buộc (`NOT NULL`).
- Có index đủ cho query theo zone/status và zone service availability.
- Không còn phụ thuộc zone text tự do trong runtime path mới.
