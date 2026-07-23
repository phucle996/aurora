# Lộ Trình Phát Triển Hạ Tầng CDC (Future Roadmap)

Tài liệu này phác thảo tầm nhìn kiến trúc dài hạn cho hệ thống CDC Outbox của Aurora và cách thiết kế hiện tại của `job-proxy` hỗ trợ chuyển dịch linh hoạt trong tương lai.

---

## 1. Bản Đồ Kiến Trúc Qua Các Phiên Bản

### Phiên Bản V1 (Hiện Tại)

Mô hình tinh gọn, tối ưu tài nguyên cho môi trường thử nghiệm và giai đoạn đầu:

```text
Control Plane (Go) 
      ↓ [Save outbox record]
Postgres DB (mail_outbox_records)
      ↓ (WAL logical replication - pgoutput push)
CDC Worker (job-proxy - Rust)
      ↓ (Push job via XADD)
Central Kafka (`aurora.jobs.commands.zone.<zone_id>.v1`)
      ↓ (XREADGROUP)
Dataplane (Rust Nodes)
```

* **Đặc trưng**: Không cần cài đặt hạ tầng phức tạp (như Kafka, Kafka Connect). Hoạt động rất nhẹ và có độ trễ cực thấp.

---

### Phiên Bản V2 (Mở Rộng Kênh Truyền Tin)

Khi hệ thống có thêm nhiều module (Billing, IAM, Audit) và cần phân phối tin nhắn đến nhiều consumer khác nhau (không chỉ riêng Dataplane):

```text
Control Plane (Go)
      ↓
Postgres DB
      ↓
CDC Worker (job-proxy)
      ↓ (Push event)
Kafka (Topic: aurora-outbox-events)
      ├── Dataplane (Consumes jobs)
      └── Audit Service (Consumes audit logs)
```

* **Cách chuyển dịch**:
  * Chỉ cần sửa đổi hàm `process_insert` trong `job-proxy/src/cdc/mod.rs` để thay thế lệnh ghi Redis (`XADD`) bằng việc gọi Kafka Producer (ví dụ sử dụng crate `rdkafka`).
  * Phía Dataplane đổi `JobConsumer` từ đọc Redis sang đọc Kafka Topic.
  * Nhờ cấu trúc payload `JobPayload` đã được chuẩn hóa độc lập ở tầng giữa, toàn bộ tầng nghiệp vụ chạy job của Dataplane không cần thay đổi.

---

### Phiên Bản V3 (Tiêu Chuẩn Enterprise & Data Platform)

Khi số lượng bảng outbox lên tới hàng chục và cần đẩy dữ liệu về Data Lake/Analytics:

```text
Control Plane (Go)
      ↓
Postgres DB
      ↓ (WAL logical replication)
Debezium Connector (Kafka Connect)
      ↓
Kafka (Topic per table)
      ├── Dataplane
      ├── Audit Service
      ├── Analytics / Data Warehouse
      └── Data Lake (S3 / GCS)
```

* **Cách chuyển dịch**:
  * Tắt hoàn toàn dịch vụ tự viết `job-proxy` ở luồng đi. Thay thế bằng cấu hình **Debezium Postgres Connector** chuẩn công nghiệp.
  * Do ta đã tách việc tạo Replication Slot và Publication ra khỏi migration SQL từ bản V1, Debezium chỉ việc kế thừa lại slot (`outbox_slot`) và publication (`outbox_pub`) hiện hữu trên Postgres DB mà không làm ảnh hưởng đến ứng dụng Control Plane.
  * Payload gửi lên Kafka bởi Debezium có dạng JSON chuẩn (chứa block `after`). Các Consumer phía sau chỉ cần đọc trường `after` này để lấy dữ liệu bản ghi tương tự như cách `job-proxy` decode nhị phân ngày trước.
  * `event_id` luôn được dùng làm khóa chống trùng lặp (Idempotency Key) để bảo vệ tính toàn vẹn của Dataplane khi chuyển đổi dịch vụ trung gian.

---

## 2. Các Điểm Thiết Kế Quan Trọng Cần Lưu Ý Khi Scale-Up

1. **Quản lý Dynamic Publication**:
   * Hiện tại publication chỉ được tạo cứng cho bảng `mail_outbox_records`. Khi xuất hiện các bảng outbox khác, cần sửa đổi để thực hiện:

     ```sql
     ALTER PUBLICATION outbox_pub ADD TABLE new_module_outbox_records;
     ```

     Hoặc cấu hình publication dạng `FOR ALL TABLES` (lưu ý quyền superuser trong RDS).

2. **Cơ chế Idempotent (Chống Trùng Lặp)**:
   * Do giao thức Logical Replication hoạt động trên cơ chế **At-Least-Once**, trùng lặp tin nhắn khi có sự cố mạng/khởi động lại worker là chắc chắn xảy ra.
   * Luôn duy trì và sử dụng `event_id` (được sinh ngẫu nhiên dạng UUID từ Control Plane) làm khóa chống trùng trên Redis/Database ở phía Dataplane để đảm bảo tính Exactly-Once hiệu dụng.
