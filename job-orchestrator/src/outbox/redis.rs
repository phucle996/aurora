use crate::config::{OwnershipWorkflowConfig, SharedRedisConfig};
use redis::aio::ConnectionManager;
use std::sync::Arc;
use tokio::sync::Mutex;

/// [COMMENT]: Tên Redis Stream chuyên biệt phát sự kiện sở hữu tài nguyên sang Cost Manager / Cost Engine.
pub const OWNERSHIP_STREAM: &str = "stream:{billing}:resource_ownership";

/// [COMMENT]: Tên Redis Stream phát sự kiện phân bổ dung lượng hạ tầng máy ảo sang Billing.
pub const HYPERVISOR_ALLOCATION_STREAM: &str = "stream:{billing}:hypervisor_allocation";

/// [COMMENT]: Publisher chuyên dụng quản lý kết nối và xuất bản sự kiện sang Shared Redis.
///
/// Tính bất biến kỹ thuật (Durability Invariant):
/// - Sử dụng `Mutex<ConnectionManager>` để tuần tự hóa lệnh `XADD` và lệnh hàng rào bền vững `WAITAOF`
///   trên đúng 1 kết nối duy nhất (vì `WAITAOF` chỉ fence các lệnh ghi được thực hiện trên chính kết nối đó).
/// - Cấm phân tách `XADD` và `WAITAOF` qua connection pool checkout hoặc reconnecting clone.
pub struct SharedStreamPublisher {
    connection: Mutex<ConnectionManager>,
    stream_capacity: usize,
    replica_acks: i64,
    wait_timeout_ms: u64,
}

impl SharedStreamPublisher {
    /// [COMMENT]: Khởi tạo kết nối Redis ConnectionManager kèm cấu hình dung lượng hàng đợi và AOF durability fence.
    pub async fn connect(
        client: &redis::Client,
        redis_config: &SharedRedisConfig,
        ownership_config: &OwnershipWorkflowConfig,
    ) -> Result<Arc<Self>, redis::RedisError> {
        let connection = crate::infra::redis::manager(client, redis_config).await?;
        Ok(Arc::new(Self {
            connection: Mutex::new(connection),
            stream_capacity: ownership_config.stream_capacity,
            replica_acks: redis_config.aof_replica_acks,
            wait_timeout_ms: redis_config.aof_timeout_ms,
        }))
    }

    /// [COMMENT]: Đẩy sự kiện sở hữu tài nguyên (`RESOURCE_CREATED` / `RESOURCE_DELETED`) sang Cost Manager.
    ///
    /// Cơ chế xử lý 3 bước:
    /// 1. **Kiểm tra dung lượng nguyên tử qua Lua Script (Backpressure Protection)**:
    ///    - Nếu độ dài `XLEN` >= `stream_capacity` (khi Cost Service bị nghẽn hoặc sập), script trả về lỗi
    ///      `OWNERSHIP_STREAM_CAPACITY_REACHED` để bảo vệ RAM của Redis không bị tràn bộ nhớ.
    ///    - JO sẽ giữ bản ghi lại trong PostgreSQL outbox để gửi bù sau.
    /// 2. **Phát lệnh XADD**: Thêm bản ghi với các trường `event_id`, `event_type`, `payload` vào stream.
    /// 3. **Hàng rào bền vững WAITAOF (Durability Fence)**:
    ///    - Chờ Redis ghi AOF xuống đĩa cục bộ (`local_aof >= 1`) và xác nhận đồng bộ sang số replica tối thiểu (`replica_aof >= replica_acks`).
    ///    - Đảm bảo dữ liệu không bị mất mát trước khi JO cập nhật `ownership_published_at` trong PostgreSQL.
    pub async fn publish_ownership(
        &self,
        event_id: &str,
        event_type: &str,
        payload: &[u8],
    ) -> Result<String, Box<dyn std::error::Error + Send + Sync>> {
        let mut connection = self.connection.lock().await;

        // [COMMENT]: Thực thi Lua script nguyên tử kiểm tra dung lượng và XADD trên Redis Stream
        let stream_id: String = redis::Script::new(
            r#"
            if redis.call('XLEN', KEYS[1]) >= tonumber(ARGV[1]) then
                return redis.error_reply('OWNERSHIP_STREAM_CAPACITY_REACHED')
            end
            return redis.call(
                'XADD', KEYS[1], '*',
                'event_id', ARGV[2],
                'event_type', ARGV[3],
                'payload', ARGV[4]
            )
            "#,
        )
        .key(OWNERSHIP_STREAM)
        .arg(self.stream_capacity)
        .arg(event_id)
        .arg(event_type)
        .arg(payload)
        .invoke_async(&mut *connection)
        .await?;

        // [COMMENT]: Kiểm tra xác nhận bền vững AOF trên đĩa cứng và Redis replicas
        let (local_aof, replica_aof): (i64, i64) = redis::cmd("WAITAOF")
            .arg(1)
            .arg(self.replica_acks)
            .arg(self.wait_timeout_ms)
            .query_async(&mut *connection)
            .await?;
        if local_aof < 1 || replica_aof < self.replica_acks {
            // [COMMENT]: Nếu không đủ xác nhận AOF, coi như chưa an toàn. Dòng outbox ở PostgreSQL vẫn giữ pending.
            // Phía Cost Manager có Inbox dedupe theo `event_id` nên việc retry gửi lại là an toàn (idempotent).
            return Err(format!(
                "Shared Redis durability fence not met: local={local_aof}, replicas={replica_aof}, required={}",
                self.replica_acks
            )
            .into());
        }

        Ok(stream_id)
    }

    /// [COMMENT]: Đẩy sự kiện phân bổ tài nguyên máy ảo (Hypervisor Allocation) sang Billing.
    pub async fn publish_hypervisor_allocation(
        &self,
        event_id: &str,
        event_type: &str,
        payload: &[u8],
    ) -> Result<String, Box<dyn std::error::Error + Send + Sync>> {
        let mut connection = self.connection.lock().await;
        let stream_id: String = redis::Script::new(
            r#"
            if redis.call('XLEN', KEYS[1]) >= tonumber(ARGV[1]) then
                return redis.error_reply('HYPERVISOR_ALLOCATION_STREAM_CAPACITY_REACHED')
            end
            return redis.call(
                'XADD', KEYS[1], '*',
                'event_id', ARGV[2],
                'event_type', ARGV[3],
                'payload', ARGV[4]
            )
            "#,
        )
        .key(HYPERVISOR_ALLOCATION_STREAM)
        .arg(self.stream_capacity)
        .arg(event_id)
        .arg(event_type)
        .arg(payload)
        .invoke_async(&mut *connection)
        .await?;

        let (local_aof, replica_aof): (i64, i64) = redis::cmd("WAITAOF")
            .arg(1)
            .arg(self.replica_acks)
            .arg(self.wait_timeout_ms)
            .query_async(&mut *connection)
            .await?;
        if local_aof < 1 || replica_aof < self.replica_acks {
            return Err(format!(
                "Shared Redis Hypervisor allocation durability fence not met: local={local_aof}, replicas={replica_aof}, required={}",
                self.replica_acks
            )
            .into());
        }
        Ok(stream_id)
    }
}
