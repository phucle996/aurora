use std::error::Error;
use redis::IntoConnectionInfo;
use redis::aio::MultiplexedConnection;
use crate::config::Config;

// [COMMENT]: Khởi tạo kết nối Redis Multiplexed Connection có hỗ trợ TLS/mTLS qua giao thức rediss://
pub async fn init_redis_conn(config: &Config) -> Result<MultiplexedConnection, Box<dyn Error>> {
    let redis_conn_info = config.redis_url.clone().into_connection_info()?;
    
    // [COMMENT]: Kiểm tra xem URL kết nối có sử dụng SSL/TLS không
    let redis_client = if redis_conn_info.addr.is_connection_secure() {
        let mut tls_certs = redis::TlsCertificates::default();
        
        // [COMMENT]: Nạp root CA để xác thực chứng chỉ Redis Server
        if let Some(ref ca_path) = config.redis_ssl_root_cert {
            let ca_pem = std::fs::read(ca_path)?;
            tls_certs = tls_certs.verify_client_with_ca(ca_pem);
        }
        
        // [COMMENT]: Nạp Client Cert & Key phục vụ mTLS Redis
        if let (Some(ref cert_path), Some(ref key_path)) = (&config.redis_ssl_client_cert, &config.redis_ssl_client_key) {
            let cert_pem = std::fs::read(cert_path)?;
            let key_pem = std::fs::read(key_path)?;
            tls_certs = tls_certs.client_auth(cert_pem, key_pem);
        }
        
        redis::Client::open_with_tls(redis_conn_info, tls_certs)?
    } else {
        redis::Client::open(redis_conn_info)?
    };

    // [COMMENT]: Khởi tạo một multiplexed connection để các task có thể dùng chung connection Redis một cách an toàn
    let redis_conn = redis_client.get_multiplexed_tokio_connection().await?;
    Ok(redis_conn)
}
