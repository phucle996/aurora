// [COMMENT]: Implementation of SessionManager for User Sessions
impl SessionManager {
    // [COMMENT]: Lấy thông tin Session từ Redis L2 sử dụng khoá phân cấp mới (zone_id:tenant_id:user_id:access_key)
    pub async fn get_session(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        access_key: &str,
    ) -> Result<Option<UserAccessSession>, AcrError> {
        let mut conn = self.get_connection().await?;
        // [COMMENT]: Dựng key session theo định dạng phân cấp mới tối ưu cho HA & Scan
        let redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, access_key
        );

        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("GET session failed: {}", e)))?;

        match data {
            Some(bytes) => {
                let session = UserAccessSession::decode(bytes.as_slice())
                    .map_err(|e| AcrError::Internal(format!("Protobuf decode failed: {}", e)))?;
                Ok(Some(session))
            }
            None => Ok(None),
        }
    }

    // [COMMENT]: Đăng ký Session mới dùng khoá phân cấp và ghi nhận full key vào index set
    pub async fn register_session(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        access_key: &str,
        ash: &str,
        device_id: &str,
    ) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        // [COMMENT]: Xây dựng Redis key phân cấp: zone_id -> tenant_id -> user_id -> access_key
        let redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, access_key
        );

        let now = chrono::Utc::now().timestamp();
        let session = UserAccessSession {
            ash: ash.to_string(),
            tdid: device_id.to_string(),
            lsa: now,
        };

        // Serialize sang dạng Protobuf binary trước khi ghi xuống Redis
        let mut buf = Vec::new();
        session
            .encode(&mut buf)
            .map_err(|e| AcrError::Internal(format!("Protobuf encode failed: {}", e)))?;

        // [COMMENT]: Lưu full redis key thay vì chỉ access_key thô để đảm bảo Control Plane (Go) có thể scan/delete
        let index_key = format!("iam:user_access_index:{}", user_id);
        let dev_index_key = format!("iam:device_access_index:{}", device_id);
        redis::pipe()
            .atomic()
            .cmd("SET")
            .arg(&redis_key)
            .arg(&buf)
            .cmd("EXPIRE")
            .arg(&redis_key)
            .arg(self.config.session_ttl_secs)
            .cmd("SADD")
            .arg(&index_key)
            .arg(&redis_key) // [COMMENT]: Thêm full key phân cấp vào User Index
            .cmd("EXPIRE")
            .arg(&index_key)
            .arg(self.config.session_ttl_secs * 3)
            .cmd("SADD")
            .arg(&dev_index_key)
            .arg(&redis_key) // [COMMENT]: Thêm full key phân cấp vào Device Index
            .cmd("EXPIRE")
            .arg(&dev_index_key)
            .arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("Register session in Redis failed: {}", e))
            })?;

        Ok(())
    }

    // [COMMENT]: Cập nhật Last Seen At cho khoá phân cấp (giảm tải bằng cách throttle 30s)
    pub async fn update_last_seen(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        access_key: &str,
        last_lsa: i64,
    ) -> Result<(), AcrError> {
        let now = chrono::Utc::now().timestamp();

        // Chỉ ghi đè Redis nếu thời gian cũ lệch quá 30 giây so với hiện tại
        if now - last_lsa < 30 {
            return Ok(());
        }

        let mut conn = self.get_connection().await?;
        let redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, access_key
        );

        // Lấy session hiện tại
        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(e.to_string()))?;

        if let Some(bytes) = data {
            let mut session = UserAccessSession::decode(bytes.as_slice())
                .map_err(|e| AcrError::Internal(e.to_string()))?;

            // Cập nhật timestamp mới
            session.lsa = now;
            let mut buf = Vec::new();
            session
                .encode(&mut buf)
                .map_err(|e| AcrError::Internal(e.to_string()))?;

            // Ghi đè vào Redis, giữ nguyên TTL
            let ttl: u64 = redis::cmd("TTL")
                .arg(&redis_key)
                .query_async(&mut conn)
                .await
                .unwrap_or(self.config.session_ttl_secs);

            redis::pipe()
                .atomic()
                .cmd("SET")
                .arg(&redis_key)
                .arg(&buf)
                .cmd("EXPIRE")
                .arg(&redis_key)
                .arg(ttl)
                .query_async::<_, ()>(&mut conn)
                .await
                .map_err(|e| AcrError::RedisError(format!("Update LSA failed: {}", e)))?;
        }

        Ok(())
    }

    // [COMMENT]: Xoay vòng session phân cấp nguyên tử có lock distributed
    pub async fn try_rotate_session(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        old_access_key: &str,
        new_access_key: &str,
        new_ash: &str,
        device_id: &str,
    ) -> Result<bool, AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:refresh:{}", old_access_key);

        // 1. Cố gắng giành quyền Lock bằng SETNX (TTL 5 giây để tự giải phóng nếu crash)
        let acquired: bool = redis::cmd("SET")
            .arg(&lock_key)
            .arg(1)
            .arg("EX")
            .arg(5)
            .arg("NX")
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Lock acquisition error: {}", e)))?;

        if !acquired {
            // Không lấy được lock -> luồng song song khác đang refresh -> cho phép đi tiếp bằng session cũ
            return Ok(false);
        }

        // 2. Tiến hành xoay vòng session
        let old_redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, old_access_key
        );
        let new_redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, new_access_key
        );
        let index_key = format!("iam:user_access_index:{}", user_id);

        let now = chrono::Utc::now().timestamp();
        let new_session = UserAccessSession {
            ash: new_ash.to_string(),
            tdid: device_id.to_string(),
            lsa: now,
        };

        let mut buf = Vec::new();
        new_session
            .encode(&mut buf)
            .map_err(|e| AcrError::Internal(e.to_string()))?;

        // 3. Thực thi Pipeline nguyên tử:
        // - Tạo session mới với đầy đủ TTL
        // - Chuyển session cũ sang thời gian sống ngắn hạn (Grace Period - hardcode 5 giây) để tránh lỗi 401 cho các request đang bay lơ lửng.
        // - Thêm full key mới vào index và xoá full key cũ để đảm bảo thu hồi chính xác.
        // - Cập nhật song song Secondary Index của thiết bị.
        let dev_index_key = format!("iam:device_access_index:{}", device_id);
        redis::pipe()
            .atomic()
            .cmd("SET")
            .arg(&new_redis_key)
            .arg(&buf)
            .cmd("EXPIRE")
            .arg(&new_redis_key)
            .arg(self.config.session_ttl_secs)
            .cmd("EXPIRE")
            .arg(&old_redis_key)
            .arg(5) // grace period 5 giây
            .cmd("SADD")
            .arg(&index_key)
            .arg(&new_redis_key)
            .cmd("SREM")
            .arg(&index_key)
            .arg(&old_redis_key)
            .cmd("EXPIRE")
            .arg(&index_key)
            .arg(self.config.session_ttl_secs * 3)
            .cmd("SADD")
            .arg(&dev_index_key)
            .arg(&new_redis_key)
            .cmd("SREM")
            .arg(&dev_index_key)
            .arg(&old_redis_key)
            .cmd("EXPIRE")
            .arg(&dev_index_key)
            .arg(self.config.session_ttl_secs * 3)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Rotation pipeline failed: {}", e)))?;

        // Giải phóng lock sớm (không cần đợi hết 5s)
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(());

        Ok(true)
    }

    // [COMMENT]: Thực hiện giảm TTL của session xuống còn 5 giây thay vì xoá ngay lập tức (DEL)
    // để tránh gây lỗi 401 bất ngờ cho các request song song khác đang bay lơ lửng,
    // đồng thời loại bỏ access key khỏi user index để ngắt liên kết.
    pub async fn delete_session(
        &self,
        zone_id: &str,
        tenant_id: &str,
        user_id: &str,
        access_key: &str,
    ) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!(
            "iam:user_session:{}:{}:{}:{}",
            zone_id, tenant_id, user_id, access_key
        );
        let index_key = format!("iam:user_access_index:{}", user_id);

        // [COMMENT]: Lấy dữ liệu session thô để decode tdid của thiết bị
        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(None);

        let device_id_opt = if let Some(bytes) = data {
            if let Ok(sess) = UserAccessSession::decode(bytes.as_slice()) {
                Some(sess.tdid)
            } else {
                None
            }
        } else {
            None
        };

        // Sử dụng pipeline để đảm bảo tính nguyên tử khi cập nhật thông tin phiên
        let mut pipe = redis::pipe();
        pipe.atomic()
            .cmd("EXPIRE")
            .arg(&redis_key)
            .arg(5)
            .cmd("SREM")
            .arg(&index_key)
            .arg(&redis_key); // [COMMENT]: Xoá full key khỏi user index

        if let Some(ref device_id) = device_id_opt {
            let dev_index_key = format!("iam:device_access_index:{}", device_id);
            pipe.cmd("SREM").arg(&dev_index_key).arg(&redis_key); // [COMMENT]: Xoá full key khỏi device index
        }

        pipe.query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Expire session in Redis failed: {}", e)))?;

        Ok(())
    }

    // [COMMENT]: Lấy dữ liệu phục hồi session đã được cache từ Redis L2
    pub async fn get_recovery_cache(
        &self,
        token_hash: &str,
    ) -> Result<Option<RecoverySessionCache>, AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:recovery_cache:{}", token_hash);

        let data: Option<String> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("GET recovery cache failed: {}", e)))?;

        match data {
            Some(json_str) => {
                let cache = serde_json::from_str(&json_str).map_err(|e| {
                    AcrError::Internal(format!("JSON deserialize recovery cache failed: {}", e))
                })?;
                Ok(Some(cache))
            }
            None => Ok(None),
        }
    }

    // [COMMENT]: Cố gắng giành quyền Lock phục hồi session bằng SETNX (TTL 5 giây)
    pub async fn try_lock_recovery(&self, token_hash: &str) -> Result<bool, AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:recovery:{}", token_hash);

        let acquired: bool = redis::cmd("SET")
            .arg(&lock_key)
            .arg(1)
            .arg("EX")
            .arg(5)
            .arg("NX")
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Recovery lock acquisition error: {}", e)))?;

        Ok(acquired)
    }

    // [COMMENT]: Lưu kết quả phục hồi session vào Redis L2 và giải phóng lock
    pub async fn set_recovery_cache(
        &self,
        token_hash: &str,
        cache: &RecoverySessionCache,
    ) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:recovery_cache:{}", token_hash);
        let lock_key = format!("iam:lock:recovery:{}", token_hash);

        let json_str = serde_json::to_string(cache).map_err(|e| {
            AcrError::Internal(format!("JSON serialize recovery cache failed: {}", e))
        })?;

        // Lưu kết quả cache với TTL ngắn (5 giây) để phục vụ các request đồng thời khác
        redis::pipe()
            .atomic()
            .cmd("SET")
            .arg(&redis_key)
            .arg(&json_str)
            .cmd("EXPIRE")
            .arg(&redis_key)
            .arg(5)
            .cmd("DEL")
            .arg(&lock_key)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("Set recovery cache in Redis failed: {}", e))
            })?;

        Ok(())
    }

    // [COMMENT]: Kiểm tra xem Lock phục hồi còn tồn tại không
    pub async fn is_recovery_locked(&self, token_hash: &str) -> Result<bool, AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:recovery:{}", token_hash);

        let exists: isize = redis::cmd("EXISTS")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("EXISTS lock check failed: {}", e)))?;

        Ok(exists > 0)
    }

    // [COMMENT]: Giải phóng lock recovery session trong Redis L2
    pub async fn release_recovery_lock(&self, token_hash: &str) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:recovery:{}", token_hash);
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("DEL recovery lock failed: {}", e)))?;
        Ok(())
    }

    // [COMMENT]: Lấy tất cả các session đang hoạt động của user từ index
    pub async fn get_active_sessions(
        &self,
        user_id: &str,
    ) -> Result<Vec<(String, i64)>, AcrError> {
        let mut conn = self.get_connection().await?;
        let index_key = format!("iam:user_access_index:{}", user_id);

        // 1. Lấy tất cả session keys từ user index
        let session_keys: Vec<String> = redis::cmd("SMEMBERS")
            .arg(&index_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("SMEMBERS failed for index {}: {}", index_key, e)))?;

        if session_keys.is_empty() {
            return Ok(vec![]);
        }

        // 2. Fetch dữ liệu của tất cả session keys qua multi GET (pipeline)
        let mut pipe = redis::pipe();
        for key in &session_keys {
            pipe.cmd("GET").arg(key);
        }
        
        let datas: Vec<Option<Vec<u8>>> = pipe
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("MGET sessions failed: {}", e)))?;

        let mut active_sessions = Vec::new();
        for data in datas.into_iter().flatten() {
            if let Ok(session) = UserAccessSession::decode(data.as_slice()) {
                active_sessions.push((session.tdid, session.lsa));
            }
        }

        Ok(active_sessions)
    }
}
