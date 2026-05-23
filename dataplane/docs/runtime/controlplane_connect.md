# Dataplane ↔ Controlplane Connect (Idea)

## Mục tiêu

Tài liệu này mô tả ý tưởng kết nối `dataplane` với `controlplane` qua gRPC ở mức phương thức tổng quan:
- không phải spec
- không phải implementation plan
- không phải flow chi tiết endpoint

Mục đích là chốt tư duy kết nối, trust model, và runtime behavior trước khi thiết kế contract cụ thể.

---

## 1) Vai trò hai phía

- `controlplane`
  - nguồn điều phối trung tâm
  - phát hành intent/job/command
  - xác thực danh tính dataplane node
  - theo dõi health/capability/state của dataplane

- `dataplane`
  - runtime executor gần resource
  - nhận command/job từ controlplane
  - báo trạng thái thực thi / heartbeat / lỗi
  - giữ boundary thực thi và retry cục bộ

---

## 2) Kết nối gRPC: phạm vi sử dụng

Trong mô hình này, gRPC không dùng cho heartbeat thường xuyên.

- gRPC dùng cho:
  - controlplane đẩy job cho dataplane execute
  - dataplane gọi ngược để confirm completion (non-mail job)

- gRPC không dùng cho:
  - heartbeat định kỳ 5s của tất cả dataplane nodes

Heartbeat sẽ đi qua Redis stream để giảm áp lực RPC khi số node dataplane lớn.

---

## 3) Connection model đề xuất (ý tưởng phase 1)

1. Controlplane push job sang dataplane để execute.
2. Dataplane publish heartbeat vào Redis stream mỗi `5s` (không spam RPC heartbeat).
3. Dataplane execute job local.
4. Sau khi execute xong **non-mail job**:
   - dataplane gọi RPC về controlplane để confirm completion
   - controlplane confirm + xóa job để giải phóng `job_id` unique

Ý nghĩa:
- giảm số lượng RPC định kỳ khi nhiều dataplane
- vẫn giữ RPC cho điểm cần đồng bộ trạng thái job cuối cùng
- lifecycle job rõ ràng theo `job_id` duy nhất

---

## 4) Security / Trust model

Nền tảng kết nối nên dùng mTLS:

- dataplane xác thực controlplane cert (CA trust)
- controlplane xác thực dataplane client cert
- identity node lấy từ SAN/CN cert hoặc metadata signed

Nguyên tắc:
- không trust chỉ theo IP
- rotate cert theo lifecycle rõ
- revoke identity khi node bị compromise

---

## 5) Runtime behavior cần có

- reconnect strategy:
  - exponential backoff + jitter cho kênh RPC quan trọng
- heartbeat model:
  - publish Redis stream mỗi `5s`
  - controlplane/consumer tự tính timeout theo stream lag
- idempotency:
  - job phải có `job_id` unique
  - confirm RPC phải replay-safe theo `job_id`
- bounded execution:
  - limit concurrency
  - cancellation và timeout rõ cho từng task

---

## 6) Message pattern mức ý tưởng

Không chốt proto ở tài liệu này, nhưng pattern nên gồm:

- JobDispatch (controlplane -> dataplane)
- HeartbeatStreamEvent (dataplane -> redis stream, mỗi 5s)
- JobCompletionConfirm RPC (dataplane -> controlplane, non-mail job)
- JobCompletionAck (controlplane -> dataplane)

---

## 7) Failure modes cần nghĩ trước

- controlplane restart
- network partition
- duplicate command delivery
- stale command sau reconnect
- long-running task bị mất callback

Mọi case trên cần replay-safe + idempotent handling.

---

## 8) Điều chưa chốt (để phase spec)

- contract payload heartbeat stream (fields bắt buộc + retention)
- timeout/lease policy để coi node là stale
- auth metadata ngoài mTLS có cần thêm ký số payload không
- mapping error code chuẩn cho JobCompletionConfirm RPC

---

## 9) Kết luận ý tưởng

Hướng hợp lý cho phase đầu:
- controlplane chỉ push job để dataplane execute
- heartbeat dataplane đi qua Redis stream mỗi 5s
- sau khi execute non-mail job xong, dataplane gọi RPC confirm để controlplane xóa job và giải phóng `job_id`
- giữ execution path replay-safe theo `job_id` và boundary transport/service rõ ràng
