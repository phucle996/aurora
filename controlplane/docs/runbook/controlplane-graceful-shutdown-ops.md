# Runbook: Controlplane Graceful Full Shutdown (Ops)

## Mục tiêu runbook
Hướng dẫn vận hành shutdown theo target flow trước và sau khi implementation hoàn tất.

## Trạng thái pre-implementation
- Dùng runbook này như checklist validation behavior hiện tại.
- Nếu có phase không đạt target, mở issue theo implementation plan.

## Target sequence cần đạt
1. Mark not-ready.
2. HTTP drain.
3. gRPC graceful stop (+ fallback).
4. Module stop.
5. Telemetry shutdown.
6. Infra close.

## Kiểm tra sau mỗi lần shutdown drill
- Có request drop bất thường không.
- Có timeout phase nào vượt baseline không.
- Có panic/race/blocked goroutine không.

## Escalation
- Nếu phase timeout thường xuyên: escalate SRE + backend owner.
- Nếu shutdown không idempotent: block release cho tới khi fix.
