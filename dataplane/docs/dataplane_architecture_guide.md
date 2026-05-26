# 📖 Cẩm nang Kiến trúc Hệ thống Dataplane (aurora-dataplane)

Tài liệu này mô tả chi tiết cấu trúc thư mục, trách nhiệm của từng thành phần, luồng xử lý và cách vận hành dự án **Dataplane (viết bằng Rust)** để đảm bảo tính sẵn sàng cao, bảo mật nghiêm ngặt và khả năng mở rộng quy mô nghiệp vụ không giới hạn.

---

## 📂 Tổng quan Cấu trúc Thư mục Dự án

```text
dataplane/
├── .env                            # Chứa biến môi trường cấu hình cục bộ (ZONE_ID, REDIS_JOB_URL...)
├── Cargo.toml                      # Quản lý dependency của Cargo build system
├── docs/                           # Thư mục lưu trữ tài liệu đặc tả & kiến trúc (specs)
└── src/
    ├── main.rs                     # Entry point khởi chạy ứng dụng & quản lý Graceful Shutdown
    ├── config/
    │   └── mod.rs                  # Bộ nạp cấu hình kiểu config.go trong Controlplane
    ├── policyengine/               # Phân hệ quản lý chính sách hot-reload (không downtime)
    │   ├── mod.rs                  # Khai báo các module con
    │   ├── engine.rs               # Quản lý active snapshot bằng ArcSwap / RwLock an toàn, lock-free
    │   ├── notifier.rs             # Tích hợp Redis Pub/Sub đồng bộ hóa tức thì liên instance
    │   ├── adapter.rs              # Watcher (notify crate) + Polling để kiểm tra file YAML local
    │   └── types.rs                # Cấu trúc schema YAML, kiểm định semantic & SHA-256 checksum
    ├── workerpool/
    │   ├── mod.rs                  # Khai báo các module con
    │   ├── lifecycle.rs            # Quản lý vòng đời worker (provisioning, restart, graceful close)
    │   ├── metrics.rs              # Thu thập số liệu đo đạc (lag, latency) linh hoạt theo MetricsType
    │   └── auto_scale.rs           # Logic co giãn số lượng worker (hỗ trợ scale về 0 & cap max bounds)
    ├── job_receiver/
    │   ├── mod.rs                  # Khai báo các module con
    │   ├── message.rs              # Định nghĩa struct payload Job và parse dữ liệu đầu vào
    │   ├── consumer.rs             # Ingest engine: Gọi workerpool & dispatch công việc sang workload
    │   └── result.rs               # Báo cáo kết quả công việc qua Redis Stream hoặc gRPC Client
    ├── rpc/                        # Phân hệ truyền thông RPC chính
    │   ├── mod.rs                  # Khai báo các module con
    │   ├── heal/                   # Gửi lazy signal state (heartbeat) về Controlplane
    │   ├── sender/                 # Khởi tạo gRPC server nội bộ của Dataplane
    │   └── receiver/               # Client đón nhận RPC ngoài và kích hoạt các Executor tương ứng
    ├── executor/
    │   ├── mod.rs                  # Định nghĩa traits Executor chung
    │   ├── hypervisor/             # Domain logic chuyên biệt cho hypervisor workload (vps.rs)
    │   └── mail/                   # Domain logic chuyên biệt cho mail workload (send.rs)
    ├── observability/
    │   ├── mod.rs                  # Khai báo các module con
    │   ├── prometheus.rs           # Dynamic Prometheus metrics registry (RPC in/out counter)
    │   ├── otel.rs                 # OpenTelemetry tracer để inject trace_id tại job-receiver
    │   └── logger.rs               # JSON logging phong cách Controlplane (Access, System, Job logs)
    └── infra/
        ├── mod.rs                  # Khai báo các module con
        ├── sqlite/                 # Local DB SQLite kết nối pool phục vụ Idempotency check
        ├── grpc/                   # Cấu hình TLS/mTLS client & server credentials
        └── redis/                  # Quản lý Connection Pool & các wrapper tương tác với Redis
```

---

## 🛠️ Chi tiết Chức năng từng Module

### 1. Tập tin Cấu hình & Khởi chạy (`src/config/` & `src/main.rs`)

* **`.env`**: Đặt tại gốc của thư mục `dataplane/`, chứa các cấu hình quan trọng như `ZONE_ID`, `REDIS_JOB_URL`, `REDIS_POLICY_URL` và host của Controlplane.
* **`config/mod.rs`**: Nạp biến môi trường và cung cấp cấu trúc `Config` bất biến (`immutable struct`) xuyên suốt vòng đời ứng dụng (hoạt động hoàn toàn stateless).
* **`main.rs`**: Điểm khởi đầu của Dataplane. Thực hiện đọc config, khởi tạo luồng Logger, nạp cấu hình cơ bản, khởi chạy các background loop (như RPC Heartbeat, Hot-reload engine, Stream Consumer) và lắng nghe tín hiệu ngắt OS (`Ctrl+C` hoặc `SIGTERM`) để thực hiện **graceful shutdown** cho tất cả các luồng công việc đang xử lý dở dang.

### 2. Bộ máy Hot Reload Chính sách (`src/policyengine/`)

* **`engine.rs`**: Quản lý snapshot in-memory của policy. Luồng công việc đọc dữ liệu lock-free thông qua con trỏ bất biến `active_snapshot`. Khi có bản cập nhật được validate thành công, luồng ghi tiến hành atomic swap sang phiên bản mới. Đồng thời, giữ lại `last_known_good` để tự động phục hồi nếu có lỗi trong tương lai.
* **`notifier.rs`**: Đăng ký lắng nghe kênh Redis Pub/Sub (`policyengine.policy.changed.v1`). Giúp đồng bộ tức thì cấu hình mới giữa các pod Dataplane trong cụm HA (High Availability) mà không cần quét file liên tục.
* **`adapter.rs`**: Trình theo dõi file YAML cục bộ. Kết hợp giữa `fsnotify` (sự kiện sửa file từ OS) và cơ chế tự động quét theo chu kỳ (`polling fallback`) để đảm bảo không bỏ sót bất kỳ thay đổi nào.
* **`types.rs`**: Định nghĩa cấu trúc YAML schema, thực hiện các hàm tính băm SHA-256 làm checksum, và chạy các bộ kiểm định (Semantic Check) trước khi hoán đổi cấu hình.

### 3. Điều phối Hạ tầng Worker (`src/workerpool/`)

* **`lifecycle.rs`**: Module duy nhất quản lý hạ tầng sống/chết của các worker task (`tokio::task::JoinHandle`). Nó không liên quan đến logic nghiệp vụ mà chỉ làm nhiệm vụ cấp phát, tái khởi động khi worker bị panics, và truyền tín hiệu kết thúc an toàn.
* **`metrics.rs`**: Cung cấp kiểu enum `MetricsType` động để đo đạc lag, độ trễ và số lượng kết nối đang hoạt động tùy theo nhu cầu của caller.
* **`auto_scale.rs`**: Công cụ đánh giá số lượng worker động. Hỗ trợ **Warm Up Mode (Scale về 0)** khi rảnh rỗi và tự động scale-up dựa trên độ trễ hoặc độ dài hàng đợi, chặn ngưỡng trần của `policyengine`.

### 4. Nhận & Điều phối Job (`src/job_receiver/`)

* **`message.rs`**: Chứa kiểu cấu trúc dữ liệu `JobPayload` và bộ phân tách (JSON parser) để biến đổi message thô từ Redis Stream thành dữ liệu sạch.
* **`consumer.rs`**: Điểm điều phối trung tâm. Nhận các job từ các worker trong pool và thực hiện định tuyến động sang các workload executor tương ứng.
* **`result.rs`**: Gói kết quả xử lý nghiệp vụ của job và trả về thông qua cơ chế Redis kết quả hoặc bắn gRPC RPC trực tiếp lên Controlplane.

### 5. Phân hệ Truyền thông RPC (`src/rpc/`)

* **`rpc/heal/`**: Gửi heartbeat đều đặn dạng "lazy state" lên Controlplane thông báo về độ sẵn sàng của Node.
* **`rpc/sender/`**: Khởi chạy một gRPC Server (sử dụng `tonic`) ngay trên Dataplane để đón nhận các lệnh khẩn cấp hoặc tín hiệu kiểm tra cấu hình.
* **`rpc/receiver/`**: Phục vụ như một client tiếp nhận các RPC gửi đến từ caller bên ngoài Dataplane, tiến hành giải mã và kích hoạt workload tương ứng trong `/src/executor/`.

### 6. Nghiệp vụ Mở rộng (`src/executor/`)

* Code nghiệp vụ được phân tách rõ ràng vào các thư mục con cụ thể:
  * `hypervisor/vps.rs`: Xử lý tạo, xóa, chỉnh sửa tài nguyên ảo hóa.
  * `mail/send.rs`: Xử lý gửi thư điện tử.
* Thiết kế dạng module con giúp việc phát triển thêm các module nghiệp vụ mới (như DNS, Database, Backup...) trở nên vô cùng dễ dàng và an toàn, đảm bảo tính bền vững (`scalable`).

### 7. Đo đạc & Giám sát (`src/observability/`)

* **`prometheus.rs`**: Quản lý Prometheus Registry. Đếm lượng RPC in/out. Không hardcode nhãn (labels) tĩnh để duy trì tính cơ động cao nhất khi lập báo cáo số liệu.
* **`otel.rs`**: Quản lý trace context. Cho phép inject trace_id truyền từ header RPC/Redis payload trực tiếp vào task context của Tokio tại `job_receiver`.
* **`logger.rs`**: JSON structured logger chất lượng cao. Cung cấp ba phân hệ log chuyên biệt:
  * `AccessLog`: Nhật ký các cuộc gọi mạng đến và đi.
  * `SystemLog`: Nhật ký các tiến trình hệ thống, lỗi nạp cấu hình.
  * `JobLog`: Nhật ký vòng đời thực thi của từng công việc.

### 8. Lớp Hạ tầng Cơ sở (`src/infra/`)

* **`sqlite/`**: Quản lý DB SQLite cục bộ dùng làm bảng ghi nhận tính duy nhất (Idempotency table), ngăn cản trùng lặp task.
* **`grpc/`**: Cung cấp credentials thiết lập TLS/mTLS bảo mật cho các gRPC client/server.
* **`redis/`**: Quản lý pool kết nối tập trung đến Redis, đóng gói các tương tác đọc/ghi Stream và Pub/Sub.

---

## 🔁 Luồng Dữ liệu Điển hình (Typical Data Flow)

1. **Ingestion**: `ZoneConsumerWorker` trong `/src/job_receiver/consumer.rs` nhận Job từ Redis Stream.
2. **Telemetry**: `logger.rs` ghi nhận log dạng `job_log` và `otel.rs` thiết lập context tracking thông qua `trace_id`.
3. **Validation**: Job được đưa qua SQLite ở `/src/infra/sqlite/` để xác nhận tính duy nhất (Idempotency).
4. **Routing**: `JobConsumer` kiểm tra topic và gọi Executor tương ứng trong `/src/executor/` để xử lý nghiệp vụ.
5. **Reporting**: Kết quả được xử lý xong sẽ được `/src/job_receiver/result.rs` gửi gRPC/Redis báo cáo về Controlplane.
