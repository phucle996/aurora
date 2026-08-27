use super::*;
use std::time::Duration;
use tokio::io::{AsyncReadExt, AsyncWriteExt};

#[tokio::test]
async fn s3_errors_keep_service_codes_and_only_absent_bucket_delete_succeeds() {
    for (operation, status, code, succeeds) in [
        ("create", 409, "BucketAlreadyOwnedByYou", false),
        ("create", 409, "BucketAlreadyExists", false),
        ("delete", 404, "NoSuchBucket", true),
        ("delete", 403, "AccessDenied", false),
        ("versioning", 403, "AccessDenied", false),
        ("delete_lifecycle", 403, "AccessDenied", false),
        ("put_lifecycle", 403, "AccessDenied", false),
    ] {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let endpoint = format!("http://{}", listener.local_addr().unwrap());
        let server = tokio::spawn(async move {
            let (mut socket, _) = tokio::time::timeout(Duration::from_secs(10), listener.accept())
                .await
                .unwrap()
                .unwrap();
            let mut request = Vec::new();
            while !request.windows(4).any(|bytes| bytes == b"\r\n\r\n") {
                let mut buffer = [0; 4096];
                let read = tokio::time::timeout(Duration::from_secs(10), socket.read(&mut buffer))
                    .await
                    .unwrap()
                    .unwrap();
                assert!(read > 0, "SDK closed before sending headers");
                request.extend_from_slice(&buffer[..read]);
            }
            let body =
                format!("<Error><Code>{code}</Code><Message>test service error</Message></Error>");
            let response = format!(
                "HTTP/1.1 {status} Error\r\nContent-Type: application/xml\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                body.len()
            );
            socket.write_all(response.as_bytes()).await.unwrap();
        });
        let config = Builder::new()
            .behavior_version(BehaviorVersion::latest())
            .credentials_provider(Credentials::new(
                "test-key",
                "test-secret",
                None,
                None,
                "test",
            ))
            .region(Region::new("us-east-1"))
            .endpoint_url(endpoint)
            .force_path_style(true)
            .retry_config(aws_sdk_s3::config::retry::RetryConfig::standard().with_max_attempts(1))
            .build();
        let client = MinioClient {
            s3_client: S3Client::from_conf(config),
        };
        let result: Result<(), String> = tokio::time::timeout(Duration::from_secs(10), async {
            match operation {
                "create" => client.create_bucket("bucket").await,
                "delete" => client.delete_bucket("bucket").await,
                "versioning" => client.put_bucket_versioning("bucket", true).await,
                "delete_lifecycle" => {
                    client
                        .put_bucket_lifecycle_configuration("bucket", vec![])
                        .await
                }
                "put_lifecycle" => {
                    let rule = aws_sdk_s3::types::LifecycleRule::builder()
                        .id("test")
                        .status(aws_sdk_s3::types::ExpirationStatus::Enabled)
                        .filter(
                            aws_sdk_s3::types::LifecycleRuleFilter::builder()
                                .prefix("")
                                .build(),
                        )
                        .expiration(
                            aws_sdk_s3::types::LifecycleExpiration::builder()
                                .days(1)
                                .build(),
                        )
                        .build()
                        .unwrap();
                    client
                        .put_bucket_lifecycle_configuration("bucket", vec![rule])
                        .await
                }
                _ => unreachable!(),
            }
        })
        .await
        .expect("S3 test request timed out");
        if succeeds {
            assert_eq!(result, Ok(()), "{operation}: {code}");
        } else {
            let error = result.expect_err("S3 error must not become success");
            assert!(error.contains(code), "{operation}: lost {code} in {error}");
        }
        server.await.unwrap();
    }
}
