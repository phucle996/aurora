// [COMMENT]: Implementation of SessionManager for SRE Admin Sessions
impl SessionManager {
    // [COMMENT]: Lấy thông tin Session Admin của SRE từ Redis L2
    pub async fn get_admin_session(
        &self,
        access_key: &str,
    ) -> Result<Option<AdminAccessSession>, AcrError> {
        // [COMMENT]: 1. Tạo kết nối tới Redis
        let mut conn = self.get_connection().await?;
        // [COMMENT]: 2. Xác định Redis key của Admin session (Bỏ zone_id khỏi Namespace theo kiến trúc HA mới)
        let redis_key = format!("iam:admin_access_session:{}", access_key);

        // [COMMENT]: 3. Thực hiện GET từ Redis
        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("GET admin session failed: {}", e)))?;

        // [COMMENT]: 4. Giải mã Protobuf nếu tồn tại dữ liệu
        match data {
            Some(bytes) => {
                let session = AdminAccessSession::decode(bytes.as_slice())
                    .map_err(|e| AcrError::Internal(format!("Protobuf decode failed: {}", e)))?;
                Ok(Some(session))
            }
            None => Ok(None),
        }
    }

    // [COMMENT]: Đăng ký Session Admin mới cho SRE vào Redis L2, đính kèm device_public_key để verify signature tại biên
    pub async fn register_admin_session(
        &self,
        access_key: &str,
        access_secret_hash: &str,
        device_public_key: &str,
    ) -> Result<(), AcrError> {
        // [COMMENT]: 1. Tạo kết nối tới Redis
        let mut conn = self.get_connection().await?;

        // [COMMENT]: 2. Thiết lập Redis key cho admin session (Bỏ zone_id khỏi Namespace theo kiến trúc HA mới)
        let redis_key = format!("iam:admin_access_session:{}", access_key);

        // [COMMENT]: 3. Khởi tạo đối tượng session với băm mật của access_secret và khóa công khai thiết bị
        let session = AdminAccessSession {
            access_secret_hash: access_secret_hash.to_string(),
            device_public_key: device_public_key.to_string(),
        };

        // [COMMENT]: 4. Serialize struct sang Protobuf binary
        let mut buf = Vec::new();
        session.encode(&mut buf).map_err(|e| {
            AcrError::Internal(format!("Protobuf encode AdminAccessSession failed: {}", e))
        })?;

        // [COMMENT]: 5. Thực thi ghi dữ liệu vào Redis và thiết lập thời gian hết hạn TTL
        redis::pipe()
            .atomic()
            .cmd("SET")
            .arg(&redis_key)
            .arg(&buf)
            .cmd("EXPIRE")
            .arg(&redis_key)
            .arg(self.config.session_ttl_secs)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("Register admin session in Redis failed: {}", e))
            })?;

        Ok(())
    }

    // [COMMENT]: Xoay vòng Session Admin (SRE Trinity Refresh) có bảo vệ chống Race Condition bằng Distributed Lock
    pub async fn try_rotate_admin_session(
        &self,
        old_access_key: &str,
        new_access_key: &str,
        new_ash: &str,
    ) -> Result<bool, AcrError> {
        let mut conn = self.get_connection().await?;
        let lock_key = format!("iam:lock:admin_refresh:{}", old_access_key);

        // [COMMENT]: 1. Cố gắng giành quyền Lock bằng SETNX (TTL 5 giây để tự giải phóng nếu crash)
        let acquired: bool = redis::cmd("SET")
            .arg(&lock_key)
            .arg(1)
            .arg("EX")
            .arg(5)
            .arg("NX")
            .query_async(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Admin lock acquisition error: {}", e)))?;

        if !acquired {
            // Không lấy được lock -> luồng song song khác đang refresh -> cho phép đi tiếp bằng session cũ
            return Ok(false);
        }

        // [COMMENT]: 2. Đọc session cũ để lấy thông tin device_public_key nhằm giữ lại sau khi xoay vòng
        let device_public_key = match self.get_admin_session(old_access_key).await? {
            Some(old_sess) => old_sess.device_public_key,
            None => "".to_string(),
        };

        // [COMMENT]: 3. Thiết lập key Redis cho session cũ và mới (Bỏ zone_id khỏi Namespace theo kiến trúc HA mới)
        let old_redis_key = format!("iam:admin_access_session:{}", old_access_key);
        let new_redis_key = format!("iam:admin_access_session:{}", new_access_key);

        let new_session = AdminAccessSession {
            access_secret_hash: new_ash.to_string(),
            device_public_key,
        };

        let mut buf = Vec::new();
        new_session
            .encode(&mut buf)
            .map_err(|e| AcrError::Internal(e.to_string()))?;

        // [COMMENT]: 4. Thực thi Pipeline nguyên tử:
        // - Tạo session mới với đầy đủ TTL
        // - Chuyển session cũ sang thời gian sống ngắn hạn (Grace Period 5 giây) để tránh lỗi 401 cho các request song song đang xử lý.
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
            .arg(5) // grace period
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| AcrError::RedisError(format!("Admin rotation pipeline failed: {}", e)))?;

        // [COMMENT]: 4. Giải phóng lock sớm
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(());

        Ok(true)
    }

    // [COMMENT]: Thực hiện giảm TTL của admin session xuống còn 5 giây thay vì xoá ngay lập tức (DEL)
    // để tránh gây lỗi 401 bất ngờ cho các request song song khác đang bay lơ lửng.
    pub async fn delete_admin_session(&self, access_key: &str) -> Result<(), AcrError> {
        let mut conn = self.get_connection().await?;
        let redis_key = format!("iam:admin_access_session:{}", access_key);

        redis::cmd("EXPIRE")
            .arg(&redis_key)
            .arg(5)
            .query_async::<_, ()>(&mut conn)
            .await
            .map_err(|e| {
                AcrError::RedisError(format!("Expire admin session in Redis failed: {}", e))
            })?;

        Ok(())
    }
}
