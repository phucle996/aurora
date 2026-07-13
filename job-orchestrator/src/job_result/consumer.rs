use crate::config::Config;
use crate::observability::logger::Logger;
use tokio_postgres::NoTls;
use super::l1_dispatcher;


// [COMMENT]: JobResultConsumer tiêu thụ kết quả công việc từ Redis Stream, giải mã và định tuyến DB update.
pub struct JobResultConsumer {
    config: Config,
    redis_client: redis::Client,
    nats_client: async_nats::Client,
}

impl JobResultConsumer {
    // [COMMENT]: Khởi tạo một JobResultConsumer mới
    pub fn new(
        config: Config,
        redis_client: redis::Client,
        nats_client: async_nats::Client,
    ) -> Self {
        Self {
            config,
            redis_client,
            nats_client,
        }
    }

    // [COMMENT]: Khởi chạy luồng chặn đọc Redis Stream nhận kết quả từ Dataplane và update outbox (HA design)
    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info(
            "job_result.run",
            "JobResultConsumer: Bắt đầu kết nối tới PostgreSQL...",
        );
        let (client, connection) =
            tokio_postgres::connect(&self.config.database_url, NoTls).await?;
        tokio::spawn(async move {
            if let Err(e) = connection.await {
                Logger::sys_error(
                    "job_result.postgres",
                    "JobResultConsumer: Lỗi kết nối PostgreSQL",
                    &e.to_string(),
                );
            }
        });

        Logger::sys_info("job_result.run", "JobResultConsumer: Kết nối tới Redis...");
        let mut redis_conn = self.redis_client.get_multiplexed_tokio_connection().await?;

        let stream_key = &self.config.result_stream_name;
        let group_name = "job-proxy-group";

        let _: redis::RedisResult<()> = redis::cmd("XGROUP")
            .arg("CREATE")
            .arg(stream_key)
            .arg(group_name)
            .arg("$")
            .arg("MKSTREAM")
            .query_async(&mut redis_conn)
            .await;

        let consumer_id = format!("job-proxy-{}", std::process::id());
        Logger::sys_info(
            "job_result.run",
            &format!("JobResultConsumer: Đang lắng nghe kết quả từ Redis Stream: {} (Group: {}, Consumer: {})...", stream_key, group_name, consumer_id)
        );

        loop {
            let reply: redis::Value = match redis::cmd("XREADGROUP")
                .arg("GROUP")
                .arg(group_name)
                .arg(&consumer_id)
                .arg("BLOCK")
                .arg(2000)
                .arg("COUNT")
                .arg(1)
                .arg("STREAMS")
                .arg(stream_key)
                .arg(">")
                .query_async(&mut redis_conn)
                .await
            {
                Ok(val) => val,
                Err(e) => {
                    Logger::sys_error("job_result.read", "Lỗi đọc từ Redis Stream", &e.to_string());
                    tokio::time::sleep(tokio::time::Duration::from_secs(1)).await;
                    continue;
                }
            };

            if let redis::Value::Bulk(streams) = reply {
                if streams.is_empty() {
                    continue;
                }
                if let redis::Value::Bulk(ref stream_data) = streams[0] {
                    if stream_data.len() >= 2 {
                        if let redis::Value::Bulk(ref messages) = stream_data[1] {
                            for message in messages {
                                if let redis::Value::Bulk(ref msg_parts) = message {
                                    if msg_parts.len() >= 2 {
                                        let msg_id = match &msg_parts[0] {
                                            redis::Value::Data(bytes) => {
                                                String::from_utf8_lossy(bytes).into_owned()
                                            }
                                            _ => continue,
                                        };

                                        if let redis::Value::Bulk(ref fields) = msg_parts[1] {
                                            let mut payload_bytes = None;
                                            for i in (0..fields.len()).step_by(2) {
                                                let key = match &fields[i] {
                                                    redis::Value::Data(bytes) => {
                                                        String::from_utf8_lossy(bytes).into_owned()
                                                    }
                                                    _ => continue,
                                                };
                                                if key == "payload" && i + 1 < fields.len() {
                                                    if let redis::Value::Data(bytes) =
                                                        &fields[i + 1]
                                                    {
                                                        payload_bytes = Some(bytes.clone());
                                                    }
                                                }
                                            }

                                            if let Some(payload) = payload_bytes {
                                                match l1_dispatcher::dispatch_result(&payload, &client, &self.nats_client).await {
                                                    Ok(_) => {
                                                        let _: redis::RedisResult<i32> =
                                                            redis::cmd("XACK")
                                                                .arg(stream_key)
                                                                .arg(group_name)
                                                                .arg(&msg_id)
                                                                .query_async(&mut redis_conn)
                                                                .await;
                                                    }
                                                    Err(err) => {
                                                        Logger::sys_error(
                                                            "job_result.process",
                                                            "Lỗi xử lý kết quả",
                                                            &err.to_string(),
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

