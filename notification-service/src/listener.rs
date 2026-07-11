use crate::infra::centrifugo::CentrifugoClient;
use crate::infra::nats::NatsClient;
use crate::observability::logger::Logger;
use futures_util::StreamExt;
use std::time::Duration;

// [COMMENT]: Bộ lắng nghe sự kiện từ NATS Core và điều phối tới các Service tương ứng xử lý push Centrifugo
pub struct NatsListener {
    nats_client: NatsClient,
    centrifugo_client: CentrifugoClient,
}

impl NatsListener {
    // Khởi tạo mới NatsListener
    pub fn new(nats_client: NatsClient, centrifugo_client: CentrifugoClient) -> Self {
        // [ignoring loop detection]
        Self {
            nats_client,
            centrifugo_client,
        }
    }

    // Khởi chạy ngầm hai luồng lắng nghe sự kiện song song
    pub async fn start_listening(&self) {
        let self_sizes = Self {
            nats_client: self.nats_client.clone(),
            centrifugo_client: self.centrifugo_client.clone(),
        };
        let self_jobs = Self {
            nats_client: self.nats_client.clone(),
            centrifugo_client: self.centrifugo_client.clone(),
        };

        // 1. Spawn luồng xử lý đồng bộ dung lượng bucket
        tokio::spawn(async move {
            self_sizes.listen_loop_bucket_sizes().await;
        });

        // 2. Spawn luồng xử lý thông báo kết quả công việc
        tokio::spawn(async move {
            self_jobs.listen_loop_job_notifications().await;
        });
    }

    // Vòng lặp tự động kết nối lại cho luồng dung lượng bucket
    async fn listen_loop_bucket_sizes(&self) {
        // [ignoring loop detection]
        Logger::sys_info(
            "nats_listener.sizes_start",
            "NATS Listener: Khởi chạy luồng lắng nghe storage.bucket.sizes.sync.* ...",
        );

        let mut retry_delay = Duration::from_secs(1);
        let max_delay = Duration::from_secs(30);

        loop {
            match self.subscribe_and_dispatch("storage.bucket.sizes.sync.*", "storage").await {
                Ok(_) => {
                    retry_delay = Duration::from_secs(1);
                }
                Err(e) => {
                    Logger::sys_error(
                        "nats_listener.sizes_error",
                        "Lỗi trong vòng lặp lắng nghe storage.bucket.sizes.sync.*, kết nối lại sau...",
                        &e.to_string(),
                    );
                    tokio::time::sleep(retry_delay).await;
                    retry_delay = std::cmp::min(retry_delay * 2, max_delay);
                }
            }
        }
    }

    // Vòng lặp tự động kết nối lại cho luồng thông báo kết quả công việc
    async fn listen_loop_job_notifications(&self) {
        // [ignoring loop detection]
        Logger::sys_info(
            "nats_listener.jobs_start",
            "NATS Listener: Khởi chạy luồng lắng nghe jobs.notifications.* ...",
        );

        let mut retry_delay = Duration::from_secs(1);
        let max_delay = Duration::from_secs(30);

        loop {
            match self.subscribe_and_dispatch("jobs.notifications.*", "job").await {
                Ok(_) => {
                    retry_delay = Duration::from_secs(1);
                }
                Err(e) => {
                    Logger::sys_error(
                        "nats_listener.jobs_error",
                        "Lỗi trong vòng lặp lắng nghe jobs.notifications.*, kết nối lại sau...",
                        &e.to_string(),
                    );
                    tokio::time::sleep(retry_delay).await;
                    retry_delay = std::cmp::min(retry_delay * 2, max_delay);
                }
            }
        }
    }

    // Đăng ký lắng nghe và phân phối (dispatch) tin nhắn tới Service phù hợp
    async fn subscribe_and_dispatch(&self, subject: &str, target_service: &str) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        // [ignoring loop detection]
        let mut subscriber = self.nats_client.client().subscribe(subject.to_string()).await?;
        Logger::sys_info(
            "nats_listener.subscribed",
            &format!("Đã đăng ký thành công NATS subject: {}", subject),
        );

        while let Some(message) = subscriber.next().await {
            let subject_str = message.subject.as_str();

            // [COMMENT]: Trích xuất user_id từ hậu tố của NATS subject
            let user_id = match subject_str.split('.').last() {
                Some(id) if !id.is_empty() => id.to_string(),
                _ => {
                    Logger::sys_warn(
                        "nats_listener.invalid_subject",
                        &format!("Bỏ qua tin nhắn, subject không hợp lệ: {}", subject_str),
                        "",
                    );
                    continue;
                }
            };

            // [COMMENT]: Giải mã payload JSON nhận được
            let payload: serde_json::Value = match serde_json::from_slice(&message.payload) {
                Ok(json) => {
                    crate::observability::metrics::MetricsManager::record_nats_event(subject_str, "ok");
                    json
                }
                Err(e) => {
                    crate::observability::metrics::MetricsManager::record_nats_event(subject_str, "decode_failed");
                    Logger::sys_error(
                        "nats_listener.payload_parse_error",
                        &format!("Lỗi giải mã JSON từ subject: {}", subject_str),
                        &e.to_string(),
                    );
                    continue;
                }
            };

            let centrifugo_client = self.centrifugo_client.clone();
            let target_service_str = target_service.to_string();

            // [COMMENT]: Gọi service xử lý tương ứng
            tokio::spawn(async move {
                let res = match target_service_str.as_str() {
                    "storage" => {
                        crate::service::storage::bucket::handle_bucket_size_sync(
                            &centrifugo_client,
                            &user_id,
                            payload,
                        )
                        .await
                    }
                    "job" => {
                        crate::service::job::notification::handle_job_notification(
                            &centrifugo_client,
                            &user_id,
                            payload,
                        )
                        .await
                    }
                    _ => Ok(()),
                };

                if let Err(e) = res {
                    Logger::sys_error(
                        "nats_listener.dispatch_fail",
                        &format!("Lỗi khi service xử lý sự kiện cho user: {}", user_id),
                        &e.to_string(),
                    );
                }
            });
        }

        Ok(())
    }
}
