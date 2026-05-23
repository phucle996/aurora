# Dataplane Runnable Skeleton

## Mục tiêu

Tài liệu này mô tả skeleton tối thiểu để `dataplane` có thể chạy được ngay:
- có entrypoint
- có config loader
- có app lifecycle
- có graceful shutdown khi nhận `SIGINT/SIGTERM`

---

## Cấu trúc hiện tại

```txt
dataplane/
├─ cmd/
│  └─ server/
│     └─ main.go
├─ internal/
│  ├─ app/
│  │  └─ app.go
│  └─ config/
│     └─ config.go
└─ go.mod
```

---

## Trách nhiệm từng file

- `cmd/server/main.go`
  - process entrypoint
  - load config
  - init app
  - start app
  - wait signal và graceful shutdown

- `internal/config/config.go`
  - định nghĩa `Config`
  - load env vars
  - parse `APP_SHUTDOWN_TIMEOUT`

- `internal/app/app.go`
  - `New(cfg)` fail-fast khi thiếu config
  - `Start(ctx)`
  - `Stop(ctx)` với timeout context

---

## Env vars

- `APP_NAME` (default: `aurora-dataplane`)
- `APP_LOG_LEVEL` (default: `info`)
- `APP_SHUTDOWN_TIMEOUT`
  - hỗ trợ dạng duration (`15s`, `30s`)
  - hoặc số nguyên giây (`15`)
  - default: `15s`

---

## Cách chạy local

Từ thư mục `dataplane`:

```bash
go run ./cmd/server
```

hoặc build rồi chạy:

```bash
go build -o dataplane-server ./cmd/server
./dataplane-server
```

---

## Graceful shutdown

Flow hiện tại:
1. process nhận `SIGINT` hoặc `SIGTERM`
2. tạo `shutdownCtx` với timeout từ `APP_SHUTDOWN_TIMEOUT`
3. gọi `app.Stop(shutdownCtx)`
4. app dừng các thành phần nội bộ (placeholder cho phase sau)

Skeleton này đã đủ để làm nền cho các phase tiếp theo:
- thêm bootstrap infra
- thêm module mail consumer
- thêm gRPC/worker runtime
