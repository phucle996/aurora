# Notification Service Architecture & Real-Time Integration Plan (Rust-based)

**Status**: Proposal  
**Owner**: Platform & Real-time Delivery Team  
**Scope**: Scalable Real-time Notifications, Centrifugo Edge Connection offloading, gRPC-delegated Trinity Token validation, and Rust HA Design

---

## 1. Tổng Quan Kiến Trúc (Architectural Overview)

Để đạt được hiệu năng tối ưu nhất (High Throughput, Low Latency, Minimal Memory Footprint) và đồng nhất ngăn công nghệ với các dịch vụ nền như `job-proxy` và `dataplane`, **Notification Service** được thiết kế bằng **Rust**.

Hệ thống gác cổng kết nối dài hạn và xác thực thông qua sự phối hợp:

1. **Centrifugo (Connection Gateway)**: Đứng ở rìa (Edge) hệ thống để gánh toàn bộ các kết nối WebSockets/SSE từ trình duyệt Client.
2. **Notification Service (Rust)**: Xây dựng trên nền tảng **Axum + Tokio**. Dịch vụ này hoàn toàn stateless, đảm nhiệm hai nhiệm vụ chính:
   - **Xác thực kết nối (Connect Proxy)**: Nhận HTTP Connect Proxy request từ Centrifugo, chuyển tiếp cookie/token qua cuộc gọi **gRPC** sang **Controlplane (Go)** để xác thực, sau đó trả kết quả về Centrifugo.
   - **Tiêu thụ Event (Redis Subscriber)**: Lắng nghe kênh Pub/Sub kết quả `job_results:*` từ Redis và chuyển tiếp tin nhắn sang HTTP API của Centrifugo để push về phía Client.

### Sơ đồ định tuyến tin nhắn và kết nối

```text
Trình duyệt Client
      │
      ├── 1. Mở WebSocket (Kèm 3 Cookie Trinity) ──> Centrifugo Gateway (Port 8000)
      │                                                     │
      │                                           2. Connect Proxy Hook (HTTP)
      │                                                     ▼
      └── 5. Nhận tin <── 6. Push WS <── 200 OK ── Notification Service (Rust, Port 8083)
                                                            │
                                                  3. Verify Token (gRPC)
                                                            ▼
                                                     Controlplane (Go)
                                                            │
                                                   4. Xác thực L2 Cache
```

---

## 2. Cấu Trúc Thư Mục Đề Xuất (`notification-service` - Rust)

Dịch vụ này được tạo song song với `controlplane` và `dataplane` trong thư mục gốc của dự án:

```text
notification-service/
├── src/
│   ├── main.rs                 # Điểm khởi chạy chính (Bootstrap)
│   ├── config.rs               # Quản lý cấu hình ENV (Port, Redis, Centrifugo, gRPC Endpoints)
│   ├── handler/
│   │   ├── mod.rs
│   │   └── connect.rs          # HTTP Handler xử lý Connect Proxy (gọi gRPC Auth)
│   ├── service/
│   │   ├── mod.rs
│   │   └── notifier.rs         # Event listener từ Redis -> Centrifugo API
│   ├── infra/
│   │   ├── mod.rs
│   │   ├── centrifugo.rs       # HTTP Client gọi API của Centrifugo
│   │   ├── redis.rs            # Subscriber lắng nghe Redis Pub/Sub
│   │   └── grpc.rs             # Client gRPC gọi sang Controlplane
│   └── pb/                     # Mã nguồn tự động sinh từ protobuf (cho gRPC client)
├── build.rs                    # Cấu hình biên dịch Protobuf/gRPC
├── Cargo.toml
├── Dockerfile                  # Cấu hình Cargo Watch cho môi trường Dev
├── Dockerfile.prod             # Multi-stage Dockerfile tĩnh (Alpine/Distroless) cho Prod
```

---

## 3. Cấu Hình Tích Hợp `docker-compose.dev.yml`

Bổ sung 2 Service này vào tệp `controlplane/docker-compose.dev.yml` để hoàn thiện hạ tầng:

```yaml
  # ============================================================================
  # 🔔 REAL-TIME MESSAGING & NOTIFICATION SERVICES
  # ============================================================================

  centrifugo:
    image: centrifugo/centrifugo:v5
    container_name: aurora-centrifugo
    ports:
      - "8000:8000"
    volumes:
      - ./dev/centrifugo/config.json:/centrifugo/config.json:ro
    command: centrifugo --config /centrifugo/config.json
    environment:
      - CENTRIFUGO_TOKEN_HMAC_SECRET_KEY=your_shared_jwt_secret_key
      - CENTRIFUGO_API_KEY=your_centrifugo_api_key_secret
    depends_on:
      redis-job:
        condition: service_healthy
    restart: unless-stopped

  notification-service:
    build:
      context: ../notification-service
      dockerfile: Dockerfile
    container_name: aurora-notification-service
    ports:
      - "8083:8083"
    environment:
      - APP_PORT=8083
      - CENTRIFUGO_API_URL=http://centrifugo:8000/api
      - CENTRIFUGO_API_KEY=your_centrifugo_api_key_secret
      - REDIS_URL=redis://controlplane-redis-job:6379/0
      - CONTROLPLANE_GRPC_ENDPOINT=controlplane-dev-1:9090 # Endpoint gRPC của Controlplane phục vụ xác thực
    depends_on:
      centrifugo:
        condition: service_started
      controlplane1:
        condition: service_started
      redis-job:
        condition: service_healthy
    restart: unless-stopped
```

---

## 4. Cơ Chế Xác Thực Ủy Quyền Qua gRPC (Trinity Token Validation)

[ignoring loop detection]
Để giữ cho `notification-service` hoàn toàn stateless và tách biệt khỏi cơ sở dữ liệu/L2 Cache của hệ thống, cơ chế xác thực sẽ ủy quyền (delegate) thông qua cuộc gọi gRPC từ Rust sang Go:

### Cấu hình `config.json` của Centrifugo

```json
{
  "allowed_origins": ["*"],
  "port": 8000,
  "proxy_connect_endpoint": "http://notification-service:8083/api/v1/realtime/connect",
  "proxy_connect_timeout": "2s",
  "proxy_headers": ["Cookie", "Authorization", "X-Real-IP", "User-Agent"]
}
```

### Quy trình giải quyết xác thực

1. Trình duyệt gửi yêu cầu kết nối đến Centrifugo kèm theo các Cookie xác thực Trinity.
2. Centrifugo gửi HTTP POST chứa các Header và Cookie sang endpoint `/api/v1/realtime/connect` của **Notification Service (Rust)**.
3. **Notification Service (Rust)** trích xuất các cookie và đóng gói thành một yêu cầu gRPC `VerifyTrinityTokenRequest` (chứa `token`, `access_key`, `access_secret`) gửi sang **Controlplane (Go)**.
4. **Controlplane (Go)**:
   - Sử dụng các middleware/helper hiện hữu để phân tích JWT, kiểm tra CIDR, so khớp chữ ký và truy vấn cache L2 cho Session.
   - Phản hồi lại qua gRPC trạng thái xác thực kèm theo thông tin định danh (`user_id`, `role`, `zone_id`).
5. **Notification Service (Rust)**:
   - Nhận phản hồi gRPC từ Go.
   - Nếu xác thực thành công: Trả về HTTP `200 OK` cho Centrifugo kèm cấu hình kênh cá nhân:

     ```json
     {
       "result": {
         "user": "user_id_123",
         "channels": ["personal:user_id_123"]
       }
     }
     ```

   - Nếu thất bại: Trả về HTTP `401 Unauthorized` để Centrifugo ngắt kết nối.
