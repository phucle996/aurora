use redis::{Client, Value};
use crate::observability::logger::Logger;
use crate::infra::centrifugo::CentrifugoClient;
use std::time::Duration;
use tokio::time::sleep;
use prost::Message;
use chrono::{Utc, TimeZone};
use crate::observability::metrics::MetricsManager;
use crate::observability::otel::TraceContext;

// Sinh mã Rust từ file proto/job_event.proto
pub mod job {
    tonic::include_proto!("job");
}
use job::JobNotificationEvent;

// Bộ lắng nghe và điều phối thông điệp từ Redis Stream
pub struct RedisSubscriber {
    client: Client,                       // Redis Client để tạo kết nối mới
    centrifugo_client: CentrifugoClient,  // Client để chuyển tiếp tin nhắn đến Gateway Centrifugo
    consumer_name: String,                // Định danh duy nhất cho Consumer Replica (Pod name)
}

impl RedisSubscriber {
    // Khởi tạo một RedisSubscriber mới
    pub fn new(redis_url: &str, centrifugo_client: CentrifugoClient) -> Self {
        // [ignoring loop detection]
        let client = Client::open(redis_url).expect("Failed to open connection to Redis");
        
        // Đọc hostname từ môi trường để làm danh tính của Pod trong K8s Consumer Group
        let consumer_name = std::env::var("HOSTNAME")
            .unwrap_or_else(|_| "notification_service_local".to_string());
            
        Self {
            client,
            centrifugo_client,
            consumer_name,
        }
    }

    // Khởi chạy vòng lặp lắng nghe với cơ chế tự động kết nối lại khi mất mạng (reconnect loop)
    pub async fn start_listening(&self) {
        Logger::sys_info("redis.subscriber", "Starting Redis Stream connection loop listener...");
        
        let mut retry_delay = Duration::from_secs(1);
        let max_delay = Duration::from_secs(30);

        loop {
            // Thực thi vòng lặp chính, nếu có lỗi kết nối sẽ bắt lấy và chờ retry
            if let Err(e) = self.listen_loop().await {
                Logger::sys_error(
                    "redis.subscriber",
                    "Error in Redis subscription stream loop. Attempting connection recovery...",
                    &format!("{:?}", e),
                );
                
                // Exponential backoff: nhân đôi thời gian chờ tới khi đạt giới hạn 30s
                sleep(retry_delay).await;
                retry_delay = std::cmp::min(retry_delay * 2, max_delay);
            } else {
                // Nếu thoát ra thành công không lỗi, reset thời gian chờ về mặc định 1s
                retry_delay = Duration::from_secs(1);
            }
        }
    }

    // Vòng lặp chính đọc dữ liệu từ Redis Stream
    async fn listen_loop(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        // [ignoring loop detection]
        // 1. Tạo kết nối bất đồng bộ thực tế tới Redis
        let mut conn = self.client.get_async_connection().await?;
        Logger::sys_info("redis.subscriber", "Successfully established async connection to Redis Cluster.");

        // 2. Tạo Consumer Group nếu chưa tồn tại (lệnh XGROUP CREATE)
        // Lỗi BUSYGROUP sẽ tự động bị bỏ qua nếu Group đã tồn tại trước đó.
        let group_create_result: redis::RedisResult<()> = redis::cmd("XGROUP")
            .arg("CREATE")
            .arg("stream:job_notifications")       // Tên Redis Stream lắng nghe
            .arg("notification_consumers")         // Tên Consumer Group của chúng ta
            .arg("$")                              // Chỉ tiêu thụ các tin nhắn mới phát sinh từ đây trở đi
            .arg("MKSTREAM")                       // Tự động tạo stream trống nếu chưa có
            .query_async(&mut conn)
            .await;

        if let Err(e) = group_create_result {
            // Chỉ ghi log debug vì đây thường là lỗi BUSYGROUP do group đã tồn tại
            Logger::sys_info("redis.subscriber", &format!("XGROUP CREATE group check: {:?}", e.detail()));
        }

        // 3. Vòng lặp vô hạn đọc tin nhắn chưa được xử lý (XREADGROUP)
        loop {
            // Đọc tối đa 10 tin nhắn mỗi chu kỳ, block tối đa 2000ms nếu không có tin mới
            let result: Value = redis::cmd("XREADGROUP")
                .arg("GROUP")
                .arg("notification_consumers")
                .arg(&self.consumer_name)
                .arg("COUNT")
                .arg("10")
                .arg("BLOCK")
                .arg("2000")
                .arg("STREAMS")
                .arg("stream:job_notifications")
                .arg(">")                           // Chỉ đọc tin nhắn chưa giao cho ai
                .query_async(&mut conn)
                .await?;

            // 4. Giải mã cấu trúc dữ liệu trả về từ Redis Stream
            // Định dạng trả về: [[stream_name, [[message_id, [key1, value1, key2, value2, ...]], ...]], ...]
            if let Value::Bulk(streams) = result {
                for stream in streams {
                    if let Value::Bulk(stream_data) = stream {
                        if stream_data.len() >= 2 {
                            if let Value::Bulk(messages) = &stream_data[1] {
                                for message in messages {
                                    if let Value::Bulk(msg_data) = message {
                                        if msg_data.len() >= 2 {
                                            // Lấy Message ID phục vụ cho việc gửi lệnh XACK sau này
                                            let message_id = match &msg_data[0] {
                                                Value::Data(bytes) => String::from_utf8_lossy(bytes).into_owned(),
                                                _ => continue,
                                            };

                                            if let Value::Bulk(fields) = &msg_data[1] {
                                                // Tìm trường "data" chứa mảng nhị phân bytes Protobuf
                                                let mut binary_data = None;
                                                for chunk in fields.chunks(2) {
                                                    if chunk.len() == 2 {
                                                        if let Value::Data(key_bytes) = &chunk[0] {
                                                            let key = String::from_utf8_lossy(key_bytes);
                                                            if key == "data" {
                                                                if let Value::Data(val_bytes) = &chunk[1] {
                                                                    binary_data = Some(val_bytes.clone());
                                                                }
                                                            }
                                                        }
                                                    }
                                                }

                                                // 5. Xử lý giải mã bản tin và đẩy qua Centrifugo
                                                if let Some(raw_bytes) = binary_data {
                                                    MetricsManager::record_redis_event("consumed");
                                                    if let Ok(event) = JobNotificationEvent::decode(&*raw_bytes) {
                                                        // Trích xuất hoặc khởi tạo Trace Context từ sự kiện nhận được
                                                        let trace_ctx = TraceContext::parse(&event.trace_parent)
                                                            .unwrap_or_else(TraceContext::new_random);
                                                        
                                                        let centrifugo_client = self.centrifugo_client.clone();
                                                        let message_id_clone = message_id.clone();
                                                        let event_job_id = event.job_id.clone();
                                                        let event_user_id = event.user_id.clone();
                                                        let event_status = event.status.clone();
                                                        let event_title = event.title.clone();
                                                        let event_message = event.message.clone();
                                                        let event_created_at = event.created_at;
                                                        let event_type = event.event_type.clone();
                                                        let client = self.client.clone();

                                                        // Thực thi xử lý nghiệp vụ đẩy tin trong phạm vi Trace Context
                                                        crate::observability::otel::CURRENT_TRACE.scope(trace_ctx, async move {
                                                            use opentelemetry::trace::{Span, Tracer};

                                                            // Lấy Trace Context hiện tại từ task-local
                                                            let trace_ctx_opt = crate::observability::otel::OtelTracer::get_current_trace();
                                                            let tracer = opentelemetry::global::tracer("notification-service");

                                                            let cx = if let Some(ref tc) = trace_ctx_opt {
                                                                tc.get_otel_context()
                                                            } else {
                                                                opentelemetry::Context::current()
                                                            };

                                                            // Tạo OTel Span để đồng bộ distributed tracing
                                                            let mut span = tracer.start_with_context(
                                                                format!("notification.publish.{}", event_type),
                                                                &cx,
                                                            );
                                                            span.set_attribute(opentelemetry::KeyValue::new("job_id", event_job_id.clone()));
                                                            span.set_attribute(opentelemetry::KeyValue::new("user_id", event_user_id.clone()));

                                                            Logger::sys_info(
                                                                "redis.subscriber",
                                                                &format!("Successfully decoded job event {} for user: {}", event_job_id, event_user_id),
                                                            );

                                                            // Chuyển đổi Unix timestamp sang chuỗi ISO 8601 UTC
                                                            let datetime = Utc.timestamp_opt(event_created_at, 0).unwrap();
                                                            
                                                            // Đóng gói JSON tinh giản hoàn toàn (lược bỏ job_id và event_type)
                                                            let client_payload = serde_json::json!({
                                                                "status": event_status,
                                                                "title": event_title,
                                                                "message": event_message,
                                                                "created_at": datetime.to_rfc3339()
                                                            });

                                                            // Phát sự kiện tới Centrifugo API
                                                            let channel_name = format!("personal:{}", event_user_id);
                                                            match centrifugo_client.publish(&channel_name, client_payload).await {
                                                                Ok(_) => {
                                                                    MetricsManager::record_centrifugo_publish("success");
                                                                    
                                                                    // Đo lường độ trễ từ khi phát sinh CDC ở DB đến lúc xuất bản Websocket
                                                                    let current_time = Utc::now().timestamp();
                                                                    let lag = (current_time - event_created_at).max(0) as f64;
                                                                    MetricsManager::record_delivered_lag("success", Duration::from_secs_f64(lag));

                                                                    // Tạo kết nối Redis riêng để thực hiện XACK tránh nghẽn thread đọc
                                                                    if let Ok(mut ack_conn) = client.get_async_connection().await {
                                                                        let ack_res: redis::RedisResult<()> = redis::cmd("XACK")
                                                                            .arg("stream:job_notifications")
                                                                            .arg("notification_consumers")
                                                                            .arg(&message_id_clone)
                                                                            .query_async(&mut ack_conn)
                                                                            .await;
                                                                        
                                                                        if let Err(ack_err) = ack_res {
                                                                            MetricsManager::record_redis_event("ack_failed");
                                                                            Logger::sys_error(
                                                                                "redis.subscriber",
                                                                                "Failed to ACK message",
                                                                                &format!("{:?}", ack_err),
                                                                            );
                                                                        } else {
                                                                            MetricsManager::record_redis_event("ack_success");
                                                                        }
                                                                    } else {
                                                                        MetricsManager::record_redis_event("ack_conn_failed");
                                                                        Logger::sys_error(
                                                                            "redis.subscriber",
                                                                            "Failed to obtain Redis connection for XACK",
                                                                            "connection_error",
                                                                        );
                                                                    }
                                                                }
                                                                Err(pub_err) => {
                                                                    span.record_error(&pub_err);
                                                                    MetricsManager::record_centrifugo_publish("failed");
                                                                    Logger::sys_error(
                                                                        "redis.subscriber",
                                                                        "Failed to publish to Centrifugo. Message held in PEL.",
                                                                        &format!("{:?}", pub_err),
                                                                    );
                                                                }
                                                            }
                                                        }).await;
                                                    } else {
                                                        MetricsManager::record_redis_event("decode_failed");
                                                        Logger::sys_error(
                                                            "redis.subscriber",
                                                            "Protobuf decode failed for incoming stream message.",
                                                            &message_id,
                                                        );
                                                    }
                                                }
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}
