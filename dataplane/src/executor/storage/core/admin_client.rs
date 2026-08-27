use tokio::process::Command;

use crate::observability::logger::Logger;

/// [COMMENT]: MinioAdminClient thực hiện các thao tác quản trị MinIO qua `mc` CLI.
/// Dùng `mc` binary đã bundled trong container thay vì gọi thẳng MinIO Admin HTTP API
/// (vì Admin API dùng format mã hóa nội bộ không có Rust SDK chính thức).
/// `mc` tự xử lý SigV4 signing và body encryption đúng chuẩn MinIO.
pub struct MinioAdminClient {
    alias: String,      // mc alias name, e.g. "minio"
    endpoint: String,   // e.g. "http://minio:9000"
    access_key: String, // Root access key
    secret_key: String, // Root secret key
}

impl MinioAdminClient {
    /// [COMMENT]: Khởi tạo Admin Client từ biến môi trường.
    pub fn from_env() -> Self {
        let host = std::env::var("MINIO_HOST")
            .expect("MINIO_HOST must be validated during Dataplane bootstrap");
        let port = std::env::var("MINIO_PORT")
            .expect("MINIO_PORT must be validated during Dataplane bootstrap");
        let access_key = std::env::var("MINIO_ACCESS_KEY")
            .expect("MINIO_ACCESS_KEY must be validated during Dataplane bootstrap");
        let secret_key = std::env::var("MINIO_SECRET_KEY")
            .expect("MINIO_SECRET_KEY must be validated during Dataplane bootstrap");

        Self {
            alias: "minio".to_string(),
            endpoint: format!("http://{}:{}", host, port),
            access_key,
            secret_key,
        }
    }

    /// [COMMENT]: Thiết lập mc alias để trỏ vào MinIO cluster với root credentials.
    /// Gọi một lần trước mỗi chuỗi lệnh mc admin — alias được lưu trong memory tạm thời.
    async fn setup_alias(&self) -> Result<(), String> {
        let output = Command::new("mc")
            .arg("alias")
            .arg("set")
            .arg(&self.alias)
            .arg(&self.endpoint)
            .arg(&self.access_key)
            .arg(&self.secret_key)
            .arg("--api")
            .arg("S3v4")
            .output()
            .await
            .map_err(|e| format!("Failed to run 'mc alias set': {}", e))?;

        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr);
            return Err(format!("mc alias set failed: {}", stderr));
        }
        Ok(())
    }

    /// [COMMENT]: Tạo MinIO User với access_key và secret_key chỉ định.
    /// Dùng: mc admin user add <alias> <access_key> <secret_key>
    /// Idempotent: nếu user đã tồn tại, coi như thành công.
    pub async fn create_user(&self, access_key: &str, secret_key: &str) -> Result<(), String> {
        let op = "storage.admin.create_user";

        // [COMMENT]: Thiết lập alias trước khi chạy lệnh
        self.setup_alias().await?;

        let output = Command::new("mc")
            .arg("admin")
            .arg("user")
            .arg("add")
            .arg(&self.alias)
            .arg(access_key)
            .arg(secret_key)
            .output()
            .await
            .map_err(|e| format!("Failed to run 'mc admin user add': {}", e))?;

        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);

        if output.status.success() {
            Logger::sys_info(
                op,
                &format!(
                    "MinIO user '{}' created via mc: {}",
                    access_key,
                    stdout.trim()
                ),
            );
            return Ok(());
        }

        // [COMMENT]: Idempotency — nếu user đã tồn tại, stderr chứa "already exists"
        if stderr.contains("already exists") || stdout.contains("already exists") {
            Logger::sys_info(
                op,
                &format!(
                    "MinIO user '{}' already exists, idempotent skip.",
                    access_key
                ),
            );
            return Ok(());
        }

        Err(format!(
            "mc admin user add failed: stdout={} stderr={}",
            stdout.trim(),
            stderr.trim()
        ))
    }

    /// [COMMENT]: Tạo canned policy trên MinIO từ policy JSON file.
    /// Dùng: mc admin policy create <alias> <policy_name> /dev/stdin
    pub async fn set_user_bucket_policy(
        &self,
        policy_name: &str,
        policy_json: &str,
    ) -> Result<(), String> {
        let op = "storage.admin.set_policy";

        self.setup_alias().await?;

        // [COMMENT]: Ghi policy JSON ra file tạm trong /tmp để mc đọc được
        let tmp_path = format!("/tmp/minio-policy-{}.json", policy_name);
        tokio::fs::write(&tmp_path, policy_json)
            .await
            .map_err(|e| format!("Failed to write policy file: {}", e))?;

        let output = Command::new("mc")
            .arg("admin")
            .arg("policy")
            .arg("create")
            .arg(&self.alias)
            .arg(policy_name)
            .arg(&tmp_path)
            .output()
            .await
            .map_err(|e| format!("Failed to run 'mc admin policy create': {}", e))?;

        // [COMMENT]: Cleanup temp file
        let _ = tokio::fs::remove_file(&tmp_path).await;

        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);

        if output.status.success() || stdout.contains("Created policy") || stdout.contains("policy")
        {
            Logger::sys_info(op, &format!("MinIO policy '{}' created.", policy_name));
            return Ok(());
        }

        // [COMMENT]: Idempotency — policy đã tồn tại
        if stderr.contains("already exists") || stdout.contains("already exists") {
            Logger::sys_info(
                op,
                &format!("MinIO policy '{}' already exists, skip.", policy_name),
            );
            return Ok(());
        }

        Err(format!(
            "mc admin policy create failed: stdout={} stderr={}",
            stdout.trim(),
            stderr.trim()
        ))
    }

    /// [COMMENT]: Gắn policy vào user cụ thể trên MinIO.
    /// Dùng: mc admin policy attach <alias> <policy_name> --user <access_key>
    pub async fn attach_policy_to_user(
        &self,
        access_key: &str,
        policy_name: &str,
    ) -> Result<(), String> {
        let op = "storage.admin.attach_policy";

        self.setup_alias().await?;

        let output = Command::new("mc")
            .arg("admin")
            .arg("policy")
            .arg("attach")
            .arg(&self.alias)
            .arg(policy_name)
            .arg("--user")
            .arg(access_key)
            .output()
            .await
            .map_err(|e| format!("Failed to run 'mc admin policy attach': {}", e))?;

        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);

        if output.status.success() {
            Logger::sys_info(
                op,
                &format!(
                    "Policy '{}' attached to user '{}'.",
                    policy_name, access_key
                ),
            );
            return Ok(());
        }

        // [COMMENT]: Idempotency — policy đã được attach trước đó
        if stderr.contains("already") || stdout.contains("already") {
            Logger::sys_info(
                op,
                &format!(
                    "Policy '{}' already attached to '{}', skip.",
                    policy_name, access_key
                ),
            );
            return Ok(());
        }

        Err(format!(
            "mc admin policy attach failed: stdout={} stderr={}",
            stdout.trim(),
            stderr.trim()
        ))
    }

    // [COMMENT]: Xóa MinIO User chỉ định phục vụ cơ chế rollback khi tạo lỗi
    // Dùng: mc admin user remove <alias> <access_key>
    pub async fn delete_user(&self, access_key: &str) -> Result<(), String> {
        let op = "storage.admin.delete_user";

        // Cấu hình mc alias trước khi thực hiện
        self.setup_alias().await?;

        let output = Command::new("mc")
            .arg("admin")
            .arg("user")
            .arg("remove")
            .arg(&self.alias)
            .arg(access_key)
            .output()
            .await
            .map_err(|e| format!("Failed to run 'mc admin user remove': {}", e))?;

        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);

        if output.status.success() {
            Logger::sys_info(
                op,
                &format!(
                    "MinIO user '{}' removed via mc: {}",
                    access_key,
                    stdout.trim()
                ),
            );
            return Ok(());
        }

        // Idempotency: nếu user không tồn tại hoặc đã bị xóa trước đó
        if stderr.contains("does not exist") || stdout.contains("does not exist") {
            Logger::sys_info(
                op,
                &format!(
                    "MinIO user '{}' does not exist, idempotent skip.",
                    access_key
                ),
            );
            return Ok(());
        }

        Err(format!(
            "mc admin user remove failed: stdout={} stderr={}",
            stdout.trim(),
            stderr.trim()
        ))
    }

    // [COMMENT]: Xóa MinIO policy chỉ định phục vụ cơ chế rollback khi tạo lỗi
    // Dùng: mc admin policy remove <alias> <policy_name>
    pub async fn delete_policy(&self, policy_name: &str) -> Result<(), String> {
        let op = "storage.admin.delete_policy";

        // Cấu hình mc alias trước khi thực hiện
        self.setup_alias().await?;

        let output = Command::new("mc")
            .arg("admin")
            .arg("policy")
            .arg("remove")
            .arg(&self.alias)
            .arg(policy_name)
            .output()
            .await
            .map_err(|e| format!("Failed to run 'mc admin policy remove': {}", e))?;

        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);

        if output.status.success() {
            Logger::sys_info(
                op,
                &format!(
                    "MinIO policy '{}' removed via mc: {}",
                    policy_name,
                    stdout.trim()
                ),
            );
            return Ok(());
        }

        // Idempotency: nếu policy không tồn tại hoặc đã bị xóa trước đó
        if stderr.contains("does not exist") || stdout.contains("does not exist") {
            Logger::sys_info(
                op,
                &format!(
                    "MinIO policy '{}' does not exist, idempotent skip.",
                    policy_name
                ),
            );
            return Ok(());
        }

        Err(format!(
            "mc admin policy remove failed: stdout={} stderr={}",
            stdout.trim(),
            stderr.trim()
        ))
    }

    /// [COMMENT]: Cấu hình hạn mức dung lượng (quota) cho bucket.
    /// Dùng: mc admin bucket quota <alias> <bucket_name> --hard <quota_bytes>
    pub async fn set_bucket_quota(
        &self,
        bucket_name: &str,
        quota_bytes: i64,
    ) -> Result<(), String> {
        let op = "storage.admin.set_quota";

        self.setup_alias().await?;

        let quota_str = format!("{}", quota_bytes);
        let output = Command::new("mc")
            .arg("admin")
            .arg("bucket")
            .arg("quota")
            .arg(&self.alias)
            .arg(bucket_name)
            .arg("--hard")
            .arg(&quota_str)
            .output()
            .await
            .map_err(|e| format!("Failed to run 'mc admin bucket quota': {}", e))?;

        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);

        if output.status.success() {
            Logger::sys_info(
                op,
                &format!(
                    "Bucket '{}' quota set to {} bytes via mc.",
                    bucket_name, quota_bytes
                ),
            );
            return Ok(());
        }

        Err(format!(
            "mc admin bucket quota failed: stdout={} stderr={}",
            stdout.trim(),
            stderr.trim()
        ))
    }

    /// [COMMENT]: Bật hoặc tạm dừng Object Versioning cho bucket trên MinIO.
    /// Dùng: mc version enable <alias>/<bucket_name> hoặc mc version suspend <alias>/<bucket_name>
    pub async fn set_bucket_versioning(
        &self,
        bucket_name: &str,
        enabled: bool,
    ) -> Result<(), String> {
        let op = "storage.admin.set_versioning";

        self.setup_alias().await?;

        let action = if enabled { "enable" } else { "suspend" };
        let target = format!("{}/{}", self.alias, bucket_name);
        let output = Command::new("mc")
            .arg("version")
            .arg(action)
            .arg(&target)
            .output()
            .await
            .map_err(|e| format!("Failed to run 'mc version {}': {}", action, e))?;

        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);

        if output.status.success() {
            Logger::sys_info(
                op,
                &format!(
                    "Bucket '{}' versioning set to '{}' via mc.",
                    bucket_name, action
                ),
            );
            return Ok(());
        }

        Err(format!(
            "mc version {} failed: stdout={} stderr={}",
            action,
            stdout.trim(),
            stderr.trim()
        ))
    }
}
