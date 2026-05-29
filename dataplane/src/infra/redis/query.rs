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
pub async fn fetch_next_stream_message(client: &redis::Client, stream_key: &str) -> Result<String, String> {
    let mut conn = client.get_multiplexed_async_connection().await.map_err(|e| e.to_string())?;

    // 1. Đảm bảo Consumer Group đã tồn tại (XGROUP CREATE stream_key dataplane-group $ MKSTREAM)
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(stream_key)
        .arg("dataplane-group")
        .arg("$")
        .arg("MKSTREAM")
        .query_async(&mut conn)
        .await;

    // 2. Thực thi đọc XREADGROUP block 1000ms
    let consumer_id = format!("consumer-{}", std::process::id());
    let reply: redis::Value = redis::cmd("XREADGROUP")
        .arg("GROUP")
        .arg("dataplane-group")
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

    // 3. Phân tích kết quả từ Value nguyên thủy của Redis
    if let Some((msg_id, raw_json)) = parse_stream_message(&reply) {
        // Nhúng động redis_msg_id vào chuỗi JSON trả về
        if let Ok(mut json_val) = serde_json::from_str::<serde_json::Value>(&raw_json) {
            if let Some(obj) = json_val.as_object_mut() {
                obj.insert("redis_msg_id".to_string(), serde_json::Value::String(msg_id.clone()));
            }
            if let Ok(modified_json) = serde_json::to_string(&json_val) {
                return Ok(modified_json);
            }
        }
        return Ok(raw_json);
    }

    Ok("{}".to_string())
}

/// Trích xuất ID và payload JSON từ phản hồi Bulk của Redis Stream
fn parse_stream_message(val: &redis::Value) -> Option<(String, String)> {
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
                                    let msg_id_val = entry_data.get(0)?;
                                    let msg_id = match msg_id_val {
                                        redis::Value::Data(d) => String::from_utf8_lossy(d).into_owned(),
                                        _ => return None,
                                    };
                                    let fields_val = entry_data.get(1)?;
                                    match fields_val {
                                        redis::Value::Bulk(fields) => {
                                            // Tìm trường có khóa là "payload"
                                            for chunk in fields.chunks(2) {
                                                if chunk.len() == 2 {
                                                    let k = match &chunk[0] {
                                                        redis::Value::Data(d) => String::from_utf8_lossy(d).into_owned(),
                                                        _ => continue,
                                                    };
                                                    if k == "payload" {
                                                        if let redis::Value::Data(d) = &chunk[1] {
                                                            return Some((msg_id, String::from_utf8_lossy(d).into_owned()));
                                                        }
                                                    }
                                                }
                                            }
                                            // Fallback trả về giá trị đầu tiên
                                            if fields.len() >= 2 {
                                                if let redis::Value::Data(d) = &fields[1] {
                                                    return Some((msg_id, String::from_utf8_lossy(d).into_owned()));
                                                }
                                            }
                                        }
                                        _ => {}
                                    }
                                }
                                _ => {}
                            }
                        }
                        _ => {}
                    }
                }
                _ => {}
            }
        }
        _ => {}
    }
    None
}

/// Xác nhận hoàn tất xử lý gói tin (Acknowledge Message) để gỡ khỏi hàng đợi kẹt.
pub async fn acknowledge_message(client: &redis::Client, stream_key: &str, group: &str, msg_id: &str) -> Result<(), String> {
    let mut conn = client.get_multiplexed_async_connection().await.map_err(|e| e.to_string())?;
    let _: u64 = redis::cmd("XACK")
        .arg(stream_key)
        .arg(group)
        .arg(msg_id)
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;
    crate::observability::logger::Logger::sys_debug(
        "infra.redis",
        &format!("Infra Redis: Stream message {} successfully XACKed in group {}", msg_id, group),
    );
    Ok(())
}

/// Thiết lập Distributed Lease Lock với thời hạn (TTL) tính bằng giây.
pub async fn acquire_lease_lock(client: &redis::Client, lock_key: &str, lease_time_secs: u32) -> Result<bool, String> {
    let mut conn = client.get_multiplexed_async_connection().await.map_err(|e| e.to_string())?;
    let reply: redis::Value = redis::cmd("SET")
        .arg(lock_key)
        .arg("locked")
        .arg("NX")
        .arg("EX")
        .arg(lease_time_secs)
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
    let mut conn = client.get_multiplexed_async_connection().await.map_err(|e| e.to_string())?;
    let _: u64 = redis::cmd("DEL")
        .arg(lock_key)
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;
    crate::observability::logger::Logger::sys_debug(
        "infra.redis",
        &format!("Infra Redis: Distributed lease lock '{}' successfully released", lock_key),
    );
    Ok(())
}

/// Đo đạc độ ứ đọng (Queue Lag) hiện tại của Stream.
pub async fn query_stream_lag(client: &redis::Client, stream_key: &str) -> Result<u64, String> {
    let mut conn = client.get_multiplexed_async_connection().await.map_err(|e| e.to_string())?;
    let len: u64 = redis::cmd("XLEN")
        .arg(stream_key)
        .query_async(&mut conn)
        .await
        .unwrap_or(0);
    Ok(len)
}

/// Đo đạc độ trễ xử lý hàng đợi (Queue Latency) tính bằng mili giây.
pub async fn query_stream_latency_ms(client: &redis::Client, stream_key: &str) -> Result<f64, String> {
    let mut conn = client.get_multiplexed_async_connection().await.map_err(|e| e.to_string())?;
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
