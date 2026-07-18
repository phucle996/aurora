/// ============================================================================
/// 📂 MODULE: infra/redis/query.rs - Các Thao Tác Nghiệp Vụ & Giám Sát Redis
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Triển khai toàn bộ các thao tác nghiệp vụ động và giám sát trạng thái trên Redis.
///   - Đây là nơi duy nhất thực thi các truy vấn động (Stream blocking reads, Lease Locking,
///     Acknowledge, Lag/Latency queries).
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Hệ thống lưu trữ khóa-giá trị động và kênh truyền tin (Redis DB).
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Thực hiện thao tác nghiệp vụ thô thông qua kết nối truyền tin bảo mật đã được xác thực.
///

/// Đọc gói tin tiếp theo từ Stream sử dụng cơ chế Consumer Group chặn (blocking read).
use crate::job_lifecycle::message::JobPayload;

/// Đọc gói tin tiếp theo từ Stream sử dụng cơ chế Consumer Group chặn (blocking read).
/// Đã được tối ưu hóa để parse trực tiếp các trường Key-Value nhị phân từ Redis Stream,
/// tránh sử dụng JSON Wrapper gây tốn CPU và phình to mảng bytes.
// [COMMENT]: Nhận thêm group_name động và gắn vào JobPayload trả về
pub async fn fetch_next_stream_message(
    client: &redis::Client,
    stream_key: &str,
    group_name: &str,
) -> Result<Option<JobPayload>, String> {
    let mut conn = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| e.to_string())?;

    // 1. Đảm bảo Consumer Group đã tồn tại (XGROUP CREATE stream_key group_name 0 MKSTREAM)
    // Sửa mốc khởi đầu từ "$" sang "0" để khi scale-up từ 0 node, Worker không bị bỏ lỡ các job đang chờ sẵn trong stream.
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(stream_key)
        .arg(group_name)
        .arg("0")
        .arg("MKSTREAM")
        .query_async(&mut conn)
        .await;

    // 2. Thực thi đọc XREADGROUP block 1000ms
    let consumer_id = format!("consumer-{}", std::process::id());
    let reply: redis::Value = redis::cmd("XREADGROUP")
        .arg("GROUP")
        .arg(group_name)
        .arg(&consumer_id)
        .arg("BLOCK")
        .arg(1000)
        .arg("COUNT")
        .arg(1)
        .arg("STREAMS")
        .arg(stream_key)
        .arg(">")
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;

    // 3. Phân tích kết quả từ Value nguyên thủy của Redis trực tiếp thành JobPayload và gắn group_name
    if let Some(mut payload) = parse_job_payload(&reply) {
        payload.redis_group_name = Some(group_name.to_string());
        Ok(Some(payload))
    } else {
        Ok(None)
    }
}

/// Trích xuất và parse trực tiếp các thuộc tính của Job từ phản hồi Bulk của Redis Stream
fn parse_job_payload(val: &redis::Value) -> Option<JobPayload> {
    match val {
        redis::Value::Bulk(streams) => {
            let stream = streams.first()?;
            match stream {
                redis::Value::Bulk(stream_data) => {
                    let entries = stream_data.get(1)?;
                    match entries {
                        redis::Value::Bulk(entry_list) => {
                            let entry = entry_list.first()?;
                            match entry {
                                redis::Value::Bulk(entry_data) => {
                                    // Phần tử đầu tiên là ID của tin nhắn Redis (e.g. "171873918-0")
                                    let msg_id_val = entry_data.get(0)?;
                                    let msg_id = match msg_id_val {
                                        redis::Value::Data(d) => {
                                            String::from_utf8_lossy(d).into_owned()
                                        }
                                        _ => return None,
                                    };

                                    // Phần tử thứ hai chứa danh sách cặp Key-Value dẹt
                                    let fields_val = entry_data.get(1)?;
                                    match fields_val {
                                        redis::Value::Bulk(fields) => {
                                            let mut job_id = String::new();
                                            let mut job_version = 1;
                                            let mut attempt = 0;
                                            let mut job_topic = String::new();
                                            let mut resource_id = String::new();
                                            let mut payload_schema_version = 1;
                                            let mut payload = Vec::new();
                                            let mut trace_id = String::new();
                                            let mut idle = None;

                                            // Lặp qua từng cặp Key-Value trong bulk array
                                            for chunk in fields.chunks(2) {
                                                if chunk.len() == 2 {
                                                    let k = match &chunk[0] {
                                                        redis::Value::Data(d) => {
                                                            String::from_utf8_lossy(d)
                                                        }
                                                        _ => continue,
                                                    };
                                                    match k.as_ref() {
                                                        "job_id" => {
                                                            if let redis::Value::Data(d) = &chunk[1]
                                                            {
                                                                job_id = String::from_utf8_lossy(d)
                                                                    .into_owned();
                                                            }
                                                        }
                                                        "job_version" => {
                                                            if let redis::Value::Data(d) = &chunk[1]
                                                            {
                                                                let s = String::from_utf8_lossy(d);
                                                                job_version =
                                                                    s.parse().unwrap_or(1);
                                                            }
                                                        }
                                                        "attempt" => {
                                                            if let redis::Value::Data(d) = &chunk[1]
                                                            {
                                                                let s = String::from_utf8_lossy(d);
                                                                attempt = s.parse().unwrap_or(0);
                                                            }
                                                        }
                                                        "job_topic" => {
                                                            if let redis::Value::Data(d) = &chunk[1]
                                                            {
                                                                job_topic =
                                                                    String::from_utf8_lossy(d)
                                                                        .into_owned();
                                                            }
                                                        }
                                                        "resource_id" => {
                                                            if let redis::Value::Data(d) = &chunk[1]
                                                            {
                                                                resource_id =
                                                                    String::from_utf8_lossy(d)
                                                                        .into_owned();
                                                            }
                                                        }
                                                        "payload_schema_version" => {
                                                            if let redis::Value::Data(d) = &chunk[1]
                                                            {
                                                                let s = String::from_utf8_lossy(d);
                                                                payload_schema_version =
                                                                    s.parse().unwrap_or(1);
                                                            }
                                                        }
                                                        "payload" => {
                                                            // Đọc trực tiếp bytes nhị phân thô không bị phình kích thước
                                                            if let redis::Value::Data(d) = &chunk[1]
                                                            {
                                                                payload = d.clone();
                                                            }
                                                        }
                                                        "trace_id" => {
                                                            if let redis::Value::Data(d) = &chunk[1]
                                                            {
                                                                // Convert 16-byte raw binary to 32-character hex string for task-local propagation
                                                                trace_id = d
                                                                    .iter()
                                                                    .map(|b| format!("{:02x}", b))
                                                                    .collect::<String>();
                                                            }
                                                        }
                                                        "idle" => {
                                                            if let redis::Value::Data(d) = &chunk[1]
                                                            {
                                                                let s = String::from_utf8_lossy(d);
                                                                idle = s.parse().ok();
                                                            }
                                                        }
                                                        _ => {}
                                                    }
                                                }
                                            }

                                            if job_id.is_empty() {
                                                return None;
                                            }

                                            Some(JobPayload {
                                                job_id,
                                                job_version,
                                                attempt,
                                                job_topic,
                                                resource_id,
                                                payload_schema_version,
                                                payload,
                                                trace_id,
                                                idle,
                                                redis_group_name: None,
                                                redis_msg_id: Some(msg_id),
                                            })
                                        }
                                        _ => None,
                                    }
                                }
                                _ => None,
                            }
                        }
                        _ => None,
                    }
                }
                _ => None,
            }
        }
        _ => None,
    }
}

/// Xác nhận hoàn tất xử lý gói tin (Acknowledge Message) để gỡ khỏi hàng đợi kẹt.
pub async fn acknowledge_message(
    client: &redis::Client,
    stream_key: &str,
    group: &str,
    msg_id: &str,
) -> Result<(), String> {
    let mut conn = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| e.to_string())?;
    let _: u64 = redis::cmd("XACK")
        .arg(stream_key)
        .arg(group)
        .arg(msg_id)
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;
    crate::observability::logger::Logger::sys_debug(
        "infra.redis",
        &format!(
            "Infra Redis: Stream message {} successfully XACKed in group {}",
            msg_id, group
        ),
    );
    Ok(())
}

/// Thiết lập Distributed Lease Lock với thời hạn (TTL) mặc định 30 giây.
pub async fn acquire_lease_lock(client: &redis::Client, lock_key: &str) -> Result<bool, String> {
    let mut conn = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| e.to_string())?;
    let mut cmd = redis::cmd("SET");
    cmd.arg(lock_key).arg("locked").arg("NX").arg("EX").arg(30);

    let reply: redis::Value = cmd
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;

    match reply {
        redis::Value::Okay => Ok(true),
        redis::Value::Nil => Ok(false),
        _ => Ok(true),
    }
}

/// Giải phóng Distributed Lease Lock.
pub async fn release_lease_lock(client: &redis::Client, lock_key: &str) -> Result<(), String> {
    let mut conn = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| e.to_string())?;
    let _: u64 = redis::cmd("DEL")
        .arg(lock_key)
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;
    crate::observability::logger::Logger::sys_debug(
        "infra.redis",
        &format!(
            "Infra Redis: Distributed lease lock '{}' successfully released",
            lock_key
        ),
    );
    Ok(())
}

/// Gia hạn hàng loạt các distributed lease lock bằng Redis Pipeline để tiết kiệm băng thông và I/O mạng.
pub async fn bulk_expire_locks(
    client: &redis::Client,
    keys: &[String],
    ttl_secs: u64,
) -> Result<(), String> {
    let mut conn = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| e.to_string())?;

    // Sử dụng redis::pipe() để gộp các lệnh EXPIRE gửi đi trong 1 TCP packet duy nhất.
    let mut pipe = redis::pipe();
    for key in keys {
        pipe.cmd("EXPIRE").arg(key).arg(ttl_secs);
    }

    let _: () = pipe
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;
    Ok(())
}

/// Đo đạc độ ứ đọng (Queue Lag) hiện tại của Stream.
pub async fn query_stream_lag(client: &redis::Client, stream_key: &str) -> Result<u64, String> {
    let mut conn = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| e.to_string())?;

    let res: redis::Value = redis::cmd("XINFO")
        .arg("GROUPS")
        .arg(stream_key)
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;

    if let redis::Value::Bulk(groups) = res {
        for group in groups {
            if let redis::Value::Bulk(fields) = group {
                let mut name_matches = false;
                let mut lag_val = None;

                for chunk in fields.chunks(2) {
                    if chunk.len() == 2 {
                        if let (redis::Value::Data(k), v) = (&chunk[0], &chunk[1]) {
                            let key = std::str::from_utf8(k).unwrap_or("");
                            if key == "name" {
                                if let redis::Value::Data(name_bytes) = v {
                                    let name = std::str::from_utf8(name_bytes).unwrap_or("");
                                    if name == "dataplane-group" {
                                        name_matches = true;
                                    }
                                }
                            } else if key == "lag" {
                                if let redis::Value::Int(l) = v {
                                    lag_val = Some(*l as u64);
                                }
                            }
                        }
                    }
                }

                if name_matches {
                    if let Some(lag) = lag_val {
                        return Ok(lag);
                    }
                }
            }
        }
    }

    Ok(0)
}

/// Đo đạc độ trễ xử lý hàng đợi (Queue Latency) tính bằng mili giây.
pub async fn query_stream_latency_ms(
    client: &redis::Client,
    stream_key: &str,
) -> Result<f64, String> {
    let mut conn = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| e.to_string())?;
    let res: redis::Value = redis::cmd("XPENDING")
        .arg(stream_key)
        .arg("dataplane-group")
        .arg("-")
        .arg("+")
        .arg(1)
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;

    if let redis::Value::Bulk(entries) = res {
        if let Some(redis::Value::Bulk(entry)) = entries.first() {
            if let Some(redis::Value::Int(idle_ms)) = entry.get(2) {
                return Ok(*idle_ms as f64);
            }
        }
    }
    Ok(0.0)
}
