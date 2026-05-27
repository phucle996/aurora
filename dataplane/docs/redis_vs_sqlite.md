# 🏛️ ĐỐI CHIẾU KIẾN TRÚC: REDIS (EPHEMERAL LOCK) & SQLITE (DURABLE STORE)

Tài liệu này phân tích chi tiết vai trò độc bản, ranh giới thiết kế và lý do tại sao hệ thống sử dụng kết hợp song song cả **Redis** và **SQLite** trong Aurora Dataplane để đạt được tính kiên cố tuyệt đối (Fault-Tolerance) và chống trùng lặp xử lý (Strict Idempotency).

---

## 1. ⚔️ Tóm Tắt Vai Trò (The Architectural Division)

Sự kết hợp này tuân thủ triết lý **"Defense in Depth" (Phòng thủ nhiều lớp)**. Cụ thể:

```mermaid
+---------------------------------------------------------------------------------+
|                                 REDIS STREAM & LOCK                             |
|    - RAM (In-Memory)  |  Tốc độ siêu tốc (Microseconds)  |  Tính chất tạm thời  |
|    - VAI TRÒ: Điều phối phân tán tức thời, khóa tranh chấp giữa các Node.       |
+---------------------------------------------------------------------------------+
                                         |
                                         v
+---------------------------------------------------------------------------------+
|                                 LOCAL SQLITE DB                                 |
|    - Ổ cứng (Durable SSD)  |  Bền vững vĩnh viễn  |  Tính chất cục bộ tại Node  |
|    - VAI TRÒ: Sổ cái ghi chép lịch sử, lá chắn chống trùng lặp, tự trị offline.  |
+---------------------------------------------------------------------------------+
```

| Tiêu chí | Redis (Distributed Ephemeral Lock) | SQLite (Local Durable Registry) |
| :--- | :--- | :--- |
| **Vị trí lưu trữ** | Tập trung (RAM Cluster) | Cục bộ tại ổ cứng Node vật lý |
| **Vòng đời trạng thái** | Tạm thời (Chỉ tồn tại khi Job đang chạy) | Bền vững dài hạn (Lịch sử giao dịch) |
| **Mục tiêu tối thượng** | **Bảo đảm tính duy nhất tức thời** (Distributed Concurrency) | **Bảo đảm tính bền vững & Chống trùng lặp** (Strict Idempotency) |
| **Tác động mạng** | Rất nhạy cảm với đứt gãy kết nối mạng | Hoàn toàn độc lập, hoạt động hoàn hảo khi offline |

---

## 2. ⚡ Chi Tiết Vai Trò Từng Thành Phần

### 2.1 Redis: Ephemeral Distributed Coordinator

Hệ thống sử dụng Redis như một **"Người điều phối giao thông phân tán"** thời gian thực nhờ tốc độ xử lý trên RAM cực nhanh:

* **Khóa tranh chấp tức thời (Distributed Lease Lock):** Sử dụng `SET locks:job:ID locked NX EX TTL` đảm bảo tại một thời điểm chỉ có duy nhất `1` Worker trên toàn bộ cụm Dataplane (gồm nhiều instance chạy ở nhiều Server vật lý khác nhau) chiếm quyền xử lý Job đó.
* **Tự động phục hồi khi Crash (Fail-safe TTL):** Nếu Node đang chạy bị sập nguồn đột ngột, khóa trên Redis tự động biến mất sau khi hết hạn (TTL). Job đó sẽ tự động được thu hồi trên Redis Stream để instance khác xử lý lại mà không bị treo vĩnh viễn.

### 2.2 SQLite: Local Durable Registry & Idempotency Guard

SQLite hoạt động như một **"Sổ cái ghi chép chứng cứ vật lý"** được lưu cứng xuống đĩa cứng của chính server chạy Dataplane đó:

* **Chống trùng lặp tuyệt đối (Strict Idempotency Guard):**
  Nếu Redis gặp sự cố đứt gãy mạng tạm thời dẫn đến việc giải phóng khóa sớm và phân phối lại Job cũ đã chạy xong. Khi Job bị gửi lại lần 2, Worker sẽ truy vấn SQLite. SQLite báo: *"Job ID này tôi đã xử lý thành công xuống đĩa cứng rồi!"* ➔ Worker lập tức hủy bỏ tiến trình chạy, tránh thảm họa chạy lại tác vụ có tác dụng phụ (e.g., tạo trùng 2 máy chủ VPS cho khách hàng).
* **Khả năng tự trị khi mất mạng (Offline Resilience):**
  Nếu kết nối đến Controlplane hoặc mạng chính bị gián đoạn, Dataplane vẫn tiếp tục thực thi Job, ghi vết thành công vào SQLite. Ngay khi mạng khôi phục, cơ chế đồng bộ nền sẽ đẩy kết quả (Sync state) từ SQLite lên Controlplane mà không sợ mất mát thông tin.
* **Lịch sử kiểm toán dài hạn (Audit Trail & Post-mortem):**
  Giúp các kỹ sư vận hành có thể SSH vào chính Node đó để điều tra lịch sử xử lý (Thời gian nhận Job, số lần retry, thông báo lỗi cụ thể) thông qua các câu lệnh SQL truyền thống một cách dễ dàng mà không làm ảnh hưởng đến hiệu năng của Redis.

---

## 3. 🛡️ Kịch Bản Ứng Phó Sự Cố (Failure Scenario Walkthroughs)

### Kịch bản A: Instance Dataplane bị đột tử (Crash/Sập nguồn)

* **Nếu chỉ có SQLite:** Không có cơ chế nào để các instance ở Server khác biết Node này đã chết để nhảy vào cứu hộ Job.
* **Thực tế phối hợp:** Khóa Lease Lock trên Redis tự động hết hạn (TTL). Instance khác chiếm lại khóa, SQLite của instance mới chưa có bản ghi này ➔ Instance mới xử lý lại Job một cách an toàn.

### Kịch bản B: Redis gặp sự cố Split-brain hoặc Quá tải

* **Nguy cơ:** Redis bị mất trạng thái khóa, giải phóng sớm khiến Job cũ bị phân phối lại (Duplicate Delivery).
* **Thực tế phối hợp:** Khi Node nhận lại Job trùng lặp, nó truy vấn SQLite cục bộ. Thấy trạng thái của Job ID đã là `COMPLETED` ➔ Từ chối chạy tiếp, lập tức bỏ qua. Hệ thống được bảo vệ 100%.

### Kịch bản C: Mất kết nối diện rộng (Sập mạng Controlplane & Redis)

* **Thực tế phối hợp:** Dataplane vẫn hoạt động cục bộ, ghi nhận trạng thái Job vào SQLite. Khi có mạng trở lại, nó thực hiện gửi ACK và gRPC report bù, không để mất bất kỳ giao dịch nào của người dùng.

---

## 4. 💡 Kết Luận Thiết Kế

Sự kết hợp giữa **Redis** và **SQLite** là minh chứng của một hệ thống có **tính thực thi nhanh nhạy thời gian thực (Redis)** kết hợp cùng **sự kiên cố, chắc chắn về lưu trữ (SQLite)**.

Tuyệt đối không được bỏ bất kỳ thành phần nào trong hai: Bỏ Redis sẽ mất khả năng điều phối phân tán nhanh; bỏ SQLite sẽ mất đi lá chắn bảo vệ cuối cùng chống lại sự cố trùng lặp dữ liệu và mất mạng cục bộ.
