// [COMMENT]: Implementation of SessionManager for SRE Admin Sessions
impl SessionManager {
    // [COMMENT]: Lấy thông tin Session Admin của SRE từ Redis L2
    pub async fn get_admin_session(
        &self,
        access_key: &str,
    ) -> Result<Option<AdminAccessSession>, AclError> {
        // [COMMENT]: 1. Tạo kết nối tới Redis
        let mut conn = self.get_connection().await?;
        // [COMMENT]: 2. Xác định Redis key của Admin session (Bỏ zone_id khỏi Namespace theo kiến trúc HA mới)
        let redis_key = format!("iam:admin_access_session:{}", access_key);

        // [COMMENT]: 3. Thực hiện GET từ Redis
        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| AclError::RedisError(format!("GET admin session failed: {}", e)))?;

        // [COMMENT]: 4. Giải mã Protobuf nếu tồn tại dữ liệu
        match data {
            Some(bytes) => {
                let session = AdminAccessSession::decode(bytes.as_slice())
                    .map_err(|e| AclError::Internal(format!("Protobuf decode failed: {}", e)))?;
                Ok(Some(session))
            }
            None => Ok(None),
        }
    }

    // [COMMENT]: Đăng ký Session Admin mới cho SRE vào Redis L2
    pub async fn register_admin_session(
        &self,
        access_key: &str,
        access_secret_hash: &str,
    ) -> Result<(), AclError> {
        // [COMMENT]: 1. Tạo kết nối tới Redis
        let mut conn = self.get_connection().await?;

        // [COMMENT]: 2. Thiết lập Redis key cho admin session (Bỏ zone_id khỏi Namespace theo kiến trúc HA mới)
        let redis_key = format!("iam:admin_access_session:{}", access_key);

        // [COMMENT]: 3. Khởi tạo đối tượng session với băm mật của access_secret
        let session = AdminAccessSession {
            access_secret_hash: access_secret_hash.to_string(),
        };

        // [COMMENT]: 4. Serialize struct sang Protobuf binary
        let mut buf = Vec::new();
        session.encode(&mut buf).map_err(|e| {
            AclError::Internal(format!("Protobuf encode AdminAccessSession failed: {}", e))
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
                AclError::RedisError(format!("Register admin session in Redis failed: {}", e))
            })?;

        Ok(())
    }

    // [COMMENT]: Xoay vòng Session Admin (SRE Trinity Refresh) có bảo vệ chống Race Condition bằng Distributed Lock
    pub async fn try_rotate_admin_session(
        &self,
        old_access_key: &str,
        new_access_key: &str,
        new_ash: &str,
    ) -> Result<bool, AclError> {
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
            .map_err(|e| AclError::RedisError(format!("Admin lock acquisition error: {}", e)))?;

        if !acquired {
            // Không lấy được lock -> luồng song song khác đang refresh -> cho phép đi tiếp bằng session cũ
            return Ok(false);
        }

        // [COMMENT]: 2. Thiết lập key Redis cho session cũ và mới (Bỏ zone_id khỏi Namespace theo kiến trúc HA mới)
        let old_redis_key = format!("iam:admin_access_session:{}", old_access_key);
        let new_redis_key = format!("iam:admin_access_session:{}", new_access_key);

        let new_session = AdminAccessSession {
            access_secret_hash: new_ash.to_string(),
        };

        let mut buf = Vec::new();
        new_session
            .encode(&mut buf)
            .map_err(|e| AclError::Internal(e.to_string()))?;

        // [COMMENT]: 3. Thực thi Pipeline nguyên tử:
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
            .map_err(|e| AclError::RedisError(format!("Admin rotation pipeline failed: {}", e)))?;

        // [COMMENT]: 4. Giải phóng lock sớm
        let _: () = redis::cmd("DEL")
            .arg(&lock_key)
            .query_async(&mut conn)
            .await
            .unwrap_or(());

        Ok(true)
    }
}
