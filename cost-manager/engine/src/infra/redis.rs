use std::error::Error;
use redis::aio::MultiplexedConnection;
use crate::config::Config;

// [COMMENT]: Khởi tạo kết nối Redis Multiplexed Connection.
// TLS termination được xử lý ở tầng hạ tầng (service mesh / mTLS proxy) nên
// application chỉ cần kết nối plain TCP tới Redis local endpoint.
pub async fn init_redis_conn(config: &Config) -> Result<MultiplexedConnection, Box<dyn Error>> {
    let redis_client = redis::Client::open(config.redis_url.as_str())?;

    // [COMMENT]: Khởi tạo một multiplexed connection để các task có thể dùng chung connection Redis một cách an toàn
    let redis_conn = redis_client.get_multiplexed_tokio_connection().await?;
    Ok(redis_conn)
}
