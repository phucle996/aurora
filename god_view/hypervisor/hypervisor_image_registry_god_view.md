# Hypervisor Image Registry - Workflow God View

> [!IMPORTANT]
> Mỗi Zone là một datacenter độc lập. Image bytes và Proxmox template của Zone
> A không được tham chiếu hoặc copy qua Zone B. Không tạo `zone-image-service`.

## Ownership và dữ liệu

| Thành phần | Trách nhiệm |
|---|---|
| Admin UI | Quản lý metadata image theo Zone và theo dõi import/delete; không hiển thị node |
| Controlplane Hypervisor | Sở hữu `image_artifacts` và một outbox duy nhất `hypervisor_outbox_records` |
| Zone image storage | Bucket MinIO của chính Zone; object key do Controlplane tạo từ image UUID/revision/SHA |
| Job Orchestrator | CDC outbox, publish command Kafka và settle result trong PostgreSQL |
| Dataplane của Zone | Đọc object ở Zone MinIO, import vào Proxmox và trả template VMID |
| Cloud Console | Chỉ đọc `AVAILABLE` catalog trong Zone đã chọn để tạo VM |
| Grafana | Visualize node/capacity/health telemetry; không phải Admin UI |

Image artifact là immutable theo `(zone_id, code, revision)`. `name` là display
label tự do; `code` mới là stable identifier. Xóa thành công là hard delete sau
Zone ACK, không có `deleted_at` hoặc trạng thái `DELETED`.

## Luồng command

```mermaid
sequenceDiagram
    participant Admin as Admin UI
    participant CP as Controlplane Hypervisor
    participant DB as PostgreSQL hypervisor
    participant JO as Job Orchestrator
    participant K as Kafka
    participant DP as Dataplane Zone
    participant S3 as MinIO Zone bucket
    participant PVE as Proxmox Zone

    Admin->>CP: POST metadata /admin/hypervisor/zones/{zone}/images
    CP->>DB: INSERT image_artifacts(state=UPLOADING)
    Admin->>S3: Upload bytes through the existing Zone storage boundary
    Admin->>CP: POST .../images/{id}/import
    CP->>DB: Atomic state=IMPORTING + hypervisor_outbox_records
    DB-->>JO: CDC shared hypervisor outbox
    JO->>K: hypervisor.image.import keyed by image_id
    K-->>DP: Zone-scoped command
    DP->>S3: Verify object key, size and SHA-256
    DP->>PVE: Import image and create/verify template
    DP->>K: ImageImportResultV1(template_vmid, SHA)
    K-->>JO: Durable result
    JO->>DB: state=AVAILABLE + template VMID, settle same outbox

    Admin->>CP: DELETE .../images/{id}
    CP->>DB: state=DELETING + same shared outbox
    JO->>K: hypervisor.image.delete
    K-->>DP: Zone-scoped delete command
    DP->>PVE: Remove template/image reference
    DP->>S3: Remove object after provider delete succeeds
    DP->>K: ImageDeleteResultV1
    JO->>DB: Hard delete image row, settle same outbox
```

The image bytes path is a Zone storage concern; the hypervisor outbox carries
metadata/command only and never embeds image bytes. Kafka uses at-least-once
delivery, manual commit and DLQ. Image import/delete validates zone binding,
image ID, revision and SHA before any Proxmox mutation. Duplicate commands are
safe because the Dataplane adopts the existing template/object by immutable
image identity.
