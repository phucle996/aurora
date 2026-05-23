# Controlplane Graceful Full Shutdown (Full Idea)

## Bài toán
Hiện controlplane cần một chuẩn graceful full shutdown rõ ràng, thống nhất và kiểm chứng được trước khi xem là production-ready.

## Mục tiêu của ý tưởng
- Xây dựng shutdown lifecycle có thứ tự deterministic.
- Giảm request drop khi dừng process.
- Đảm bảo stop idempotent và best-effort khi có lỗi từng bước.

## Kỳ vọng behavior mục tiêu
- Có shutdown order chuẩn: readiness off -> HTTP drain -> gRPC stop -> modules stop -> telemetry shutdown -> infra close.
- Có timeout semantics rõ cho từng chặng.
- Có runbook ops tương ứng.

## Không làm trong idea này
- Không đi vào patch code chi tiết.
- Không chốt cluster orchestration policy liên instance.
