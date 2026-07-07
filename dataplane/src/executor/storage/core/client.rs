use aws_config::BehaviorVersion;
use aws_credential_types::Credentials;
use aws_sdk_s3::config::{Builder, Region};
use aws_sdk_s3::Client as S3Client;

/// [COMMENT]: MinioClient bọc AWS S3 SDK Client phục vụ tương tác an toàn với cụm MinIO L2.
#[derive(Clone)]
pub struct MinioClient {
    s3_client: S3Client,
}

impl MinioClient {
    /// Khởi tạo MinIO Client bằng cách đọc cấu hình Root credentials từ môi trường
    pub async fn from_env() -> Self {
        let host = std::env::var("MINIO_HOST").unwrap_or_else(|_| "localhost".to_string());
        let port_str = std::env::var("MINIO_PORT").unwrap_or_else(|_| "9000".to_string());

        // Root credentials của cụm MinIO để thực hiện các thao tác quản trị (tạo bucket)
        let access_key =
            std::env::var("MINIO_ACCESS_KEY").unwrap_or_else(|_| "minioadmin".to_string());
        let secret_key =
            std::env::var("MINIO_SECRET_KEY").unwrap_or_else(|_| "minioadmin".to_string());

        let endpoint_url = format!("http://{}:{}", host, port_str);

        // Tạo AWS Credentials thủ công cho MinIO
        let credentials = Credentials::new(access_key, secret_key, None, None, "minio-admin");

        // Cấu hình AWS SDK trỏ tới MinIO endpoint local
        let sdk_config = aws_config::defaults(BehaviorVersion::latest())
            .credentials_provider(credentials)
            .endpoint_url(endpoint_url)
            .region(Region::new("us-east-1")) // MinIO mặc định hoạt động với region bất kỳ
            .load()
            .await;

        // Ép cấu hình sử dụng path style access cho MinIO (bắt buộc đối với local IP/domain không có subdomain routing)
        let s3_config = Builder::from(&sdk_config).force_path_style(true).build();

        let s3_client = S3Client::from_conf(s3_config);

        Self { s3_client }
    }

    /// Khởi tạo bucket vật lý trên MinIO sử dụng SDK (Tự động ký Signature V4)
    pub async fn create_bucket(&self, bucket_name: &str) -> Result<(), aws_sdk_s3::Error> {
        self.s3_client
            .create_bucket()
            .bucket(bucket_name)
            .send()
            .await?;
        Ok(())
    }
}
