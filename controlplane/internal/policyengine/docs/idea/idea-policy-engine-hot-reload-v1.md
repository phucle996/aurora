# Idea: Policy Engine Hot Reload (Chosen: Option B)

## 1. Bối cảnh
Controlplane cần policy engine runtime có thể cập nhật nóng mà không restart process, không downtime, phù hợp môi trường cloud-native CCU cao và vẫn chạy được ở systemd/docker.

Mô hình vận hành mục tiêu: giống APISIX ở hành vi runtime — policy cập nhật từ config source, process tự reload an toàn, request path luôn đọc snapshot in-memory.

## 2. Ý tưởng chính
`internal/policyengine` là module độc lập, **không dùng DB làm SoT policy**.

Nguồn policy duy nhất là **file YAML** (ConfigMap mount trên Kubernetes, managed file trên systemd, bind mount trên Docker). Engine parse + validate policy từ file, sau đó atomic swap snapshot in-memory.

Core invariant:
- Không bao giờ apply policy invalid.
- Luôn giữ `last-known-good` để không gián đoạn traffic.
- Request path chỉ đọc policy trong RAM, không truy vấn DB.

## 3. Mục tiêu
- Hot reload không restart.
- Zero-downtime khi đổi policy.
- Strong safety: invalid policy không phá runtime.
- Runtime consistency tốt giữa instances trong cùng region.
- Hỗ trợ nhiều môi trường deploy: Kubernetes, Linux systemd, Docker.
- Quan sát vận hành đầy đủ bằng log/metrics: version/checksum/state/reload lifecycle.

## 4. Phạm vi
### Trong phạm vi
- Module `internal/policyengine` với contract rõ: load, validate, activate, observe.
- Policy schema versioning + strict validation.
- Atomic snapshot swap + last-known-good fallback.
- Auto reload mechanism (watch/poll/event theo môi trường).
- Log/metrics là kênh quan sát chính cho SRE.
- Multi-environment distribution patterns (k8s/systemd/docker) theo adapter.
- Rollback nhanh về bản policy trước đó (theo file revision/checksum).

### Ngoài phạm vi
- Không thiết kế multi-region consensus ở phase hiện tại.
- Không mở route/handler riêng cho quan sát và manual reload endpoint.
- Không lưu policy runtime vào DB.
- Không dùng DB làm source-of-truth cho policy.

## 5. Luồng nghiệp vụ dự kiến
1. SRE cập nhật policy YAML theo schema chuẩn.
2. Policy file được publish vào runtime source của môi trường:
   - Kubernetes: `kubectl apply` ConfigMap.
   - systemd: cập nhật managed file path.
   - Docker: cập nhật file bind mount.
3. Engine instance nhận thay đổi qua adapter watch/poll file.
4. Parse -> validate schema -> validate semantic rules.
5. Hợp lệ: tạo snapshot mới (version/checksum), atomic swap `active`.
6. Không hợp lệ: reject, giữ `last-known-good`, phát cảnh báo.
7. Runtime log/metrics luôn phản ánh policy đang active.
8. Khi cần: rollback về file revision/checksum trước bằng thao tác vận hành chuẩn.

## 6. Quyết định kiến trúc (Chốt)
**Chọn Option B - YAML source adapter per environment.**

Thiết kế chốt:
- 1 engine core chung cho mọi môi trường.
- 1 `PolicySourceAdapter` contract để tách khác biệt runtime.
- Adapter implementations:
  - K8s ConfigMap file adapter.
  - systemd managed-file adapter.
  - Docker bind-mount adapter.
- Không đổi business modules; chỉ wire adapter tại `internal/app/module.go`.

## 7. Phạm vi thay đổi chính (theo Option B)
- `internal/policyengine/runtime`: thêm/chuẩn hóa contract `PolicySourceAdapter`.
- `internal/policyengine/runtime`: refactor engine core + tách adapters theo môi trường.
- `internal/policyengine/module.go`: nhận adapter dependency và dựng module fail-fast.
- `internal/app/module.go`: chọn adapter theo runtime config và inject vào policyengine.

## 8. Trade-off đã chấp nhận
- Chấp nhận effort trung bình để đổi lại boundary sạch và khả năng chạy đa môi trường.
- Chấp nhận có adapter layer thay vì hard-code 1 flow file poll duy nhất.
- Không chọn hướng DB-based để giữ nguyên tắc YAML-only SoT.

## 9. Xác minh tính khả thi theo codebase hiện tại
### Kết luận
**FEASIBLE_WITH_REFACTOR**

### Code Survey
- `internal/policyengine/module.go`: có module bootstrap service.
- `internal/policyengine/runtime/types/policy.go`: có `PolicySet` runtime shape.
- `internal/policyengine/runtime/engine_service.go`: có contract `Current`/`Reload`.
- `internal/policyengine/runtime/engine_service.go`: đã có baseline reload YAML + checksum + atomic swap.

### Bằng chứng code
- Nền tảng module đã có và không phụ thuộc DB cho policy runtime.
- Reload safety baseline (last-known-good + atomic swap) đã có điểm tựa.

### Gap chính cần xử lý
- Nâng validator từ basic lên strict schema + semantic constraints.
- Chuẩn hóa `PolicySourceAdapter` để tách behavior theo môi trường.
- Bổ sung file revision metadata + rollback workflow rõ cho SRE.
- Chuẩn hóa observability: log fields + metrics labels để trace reload lifecycle.

## 10. Ràng buộc và giả định
- Single-region HA là target hiện tại.
- DB chỉ lưu nghiệp vụ doanh nghiệp, không chứa policy runtime SoT.
- Mỗi môi trường deploy có cách phân phối YAML file khác nhau.
- Team SRE cần workflow đồng nhất: publish file -> verify logs/metrics -> rollback khi cần.

## 11. Tiêu chí hoàn thành
- Policy engine chạy ổn định và nhất quán trên Kubernetes/systemd/docker.
- Update policy YAML không cần restart, không downtime.
- Invalid policy không ảnh hưởng active traffic.
- Có thể rollback nhanh theo version/checksum/revision file.
- Có log/metric đủ để SRE xác nhận trạng thái active policy trên từng instance.

## 12. Quyết định vận hành ban đầu
- Polling interval mặc định cho production: `2s`; cho phép override bằng env `POLICY_ENGINE_POLL_INTERVAL`; guardrail min `500ms`, max `10s`.
- Schema versioning: chốt cứng `version: v1` từ đầu; nếu version không khớp thì reject và giữ `last-known-good`.
- SLA đồng bộ cross-instance (single-region): mục tiêu `<= 8s` p95 (bao gồm source propagation + poll detect + apply).
- Readiness khi chưa load policy lần đầu: fail readiness trong production để tránh nhận traffic với policy rỗng; chỉ cho phép degrade mode ở non-prod qua cờ cấu hình.
