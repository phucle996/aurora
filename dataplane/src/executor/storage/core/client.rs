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
    // [COMMENT]: Helper nội bộ: khởi tạo S3 Client từ endpoint_url bất kỳ với credentials từ môi trường.
    // Tất cả config chung (path style, region, credential provider) được tập trung tại đây.
    async fn build_from_endpoint(endpoint_url: String, provider_name: &'static str) -> Self {
        let access_key = std::env::var("MINIO_ACCESS_KEY")
            .expect("MINIO_ACCESS_KEY must be validated during Dataplane bootstrap");
        let secret_key = std::env::var("MINIO_SECRET_KEY")
            .expect("MINIO_SECRET_KEY must be validated during Dataplane bootstrap");

        let credentials = Credentials::new(access_key, secret_key, None, None, provider_name);

        let sdk_config = aws_config::defaults(BehaviorVersion::latest())
            .credentials_provider(credentials)
            .endpoint_url(endpoint_url)
            .region(Region::new("us-east-1")) // MinIO mặc định hoạt động với region bất kỳ
            .load()
            .await;

        // Ép cấu hình sử dụng path style access (bắt buộc đối với local IP/domain không có subdomain routing)
        let s3_config = Builder::from(&sdk_config).force_path_style(true).build();
        let s3_client = S3Client::from_conf(s3_config);

        Self { s3_client }
    }

    /// [COMMENT]: Khởi tạo MinIO Client kết nối qua Private Endpoint (internal network: MINIO_HOST:MINIO_PORT).
    /// Sử dụng cho các tác vụ quản trị nội bộ: tạo/xóa bucket, list objects, monitor.
    pub async fn from_env_private() -> Self {
        let host = std::env::var("MINIO_HOST")
            .expect("MINIO_HOST must be validated during Dataplane bootstrap");
        let port = std::env::var("MINIO_PORT")
            .expect("MINIO_PORT must be validated during Dataplane bootstrap");
        let endpoint_url = format!("http://{}:{}", host, port);
        Self::build_from_endpoint(endpoint_url, "minio-private").await
    }

    /// Khởi tạo bucket vật lý trên MinIO sử dụng SDK (Tự động ký Signature V4)
    pub async fn create_bucket(&self, bucket_name: &str) -> Result<(), aws_sdk_s3::Error> {
        crate::observability::otel::OtelTracer::trace_result(
            "S3 CreateBucket",
            opentelemetry::trace::SpanKind::Client,
            vec![
                opentelemetry::KeyValue::new("rpc.system", "aws-api"),
                opentelemetry::KeyValue::new("rpc.service", "S3"),
                opentelemetry::KeyValue::new("rpc.method", "CreateBucket"),
            ],
            self.s3_client.create_bucket().bucket(bucket_name).send(),
        )
        .await?;
        Ok(())
    }

    // [COMMENT]: Xóa bucket vật lý khỏi MinIO phục vụ cơ chế rollback khi tạo lỗi
    pub async fn delete_bucket(&self, bucket_name: &str) -> Result<(), aws_sdk_s3::Error> {
        crate::observability::otel::OtelTracer::trace_result(
            "S3 DeleteBucket",
            opentelemetry::trace::SpanKind::Client,
            vec![
                opentelemetry::KeyValue::new("rpc.system", "aws-api"),
                opentelemetry::KeyValue::new("rpc.service", "S3"),
                opentelemetry::KeyValue::new("rpc.method", "DeleteBucket"),
            ],
            self.s3_client.delete_bucket().bucket(bucket_name).send(),
        )
        .await?;
        Ok(())
    }
}
