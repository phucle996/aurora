use crate::config::{ScyllaConfig, ScyllaTlsMode};
use crate::infra::vault::VaultClient;
use crate::service::ports::AppError;
use rustls::pki_types::{CertificateDer, PrivateKeyDer};
use scylla::client::execution_profile::ExecutionProfile;
use scylla::client::session::Session;
use scylla::client::session_builder::SessionBuilder;
use scylla::statement::Consistency;
use std::fs::File;
use std::io::BufReader;
use std::path::Path;
use std::sync::Arc;

// [COMMENT]: Đường dẫn secret trong HashiCorp Vault chứa thông tin kết nối ScyllaDB của notification-service
const CONNECTION_PATH: &str = "secret/data/connections/scylla/central/role-notification-service";

// [COMMENT]: Cấu trúc bản ghi cấu hình kết nối ScyllaDB lấy từ Vault (schema_version 1)
#[derive(serde::Deserialize)]
struct ConnectionRecord {
    schema_version: u32,
    contact_points: Vec<String>,
    local_dc: String,
    keyspace: String,
    username: String,
    password: String,
    tls_mode: String,
    ca_cert_path: Option<String>,
    client_cert_path: Option<String>,
    client_key_path: Option<String>,
}

// [COMMENT]: Đọc và giải mã thông tin kết nối ScyllaDB từ Vault, sau đó validate và ghi đè vào ScyllaConfig
pub async fn resolve_from_vault(
    vault: &VaultClient,
    config: &mut ScyllaConfig,
) -> Result<(), AppError> {
    // [COMMENT]: Truy vấn Vault client để lấy secret payload
    let record: ConnectionRecord = vault
        .read(CONNECTION_PATH)
        .await
        .map_err(|error| Box::new(std::io::Error::other(error)) as AppError)?;

    // [COMMENT]: Kiểm tra schema_version của secret để bảo đảm tính tương thích dữ liệu
    if record.schema_version != 1 {
        return Err(invalid(format!(
            "unsupported Vault Scylla schema_version {}",
            record.schema_version
        )));
    }

    // [COMMENT]: Chuẩn hóa và làm sạch danh sách địa chỉ contact_points
    let contact_points = record
        .contact_points
        .iter()
        .map(|value| value.trim())
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .collect::<Vec<_>>();
    if contact_points.is_empty() {
        return Err(invalid("Vault Scylla contact_points is required"));
    }

    // [COMMENT]: Kiểm tra tính hợp lệ của định danh Datacenter và Keyspace
    if !valid_identifier(&record.local_dc) {
        return Err(invalid("Vault Scylla local_dc is invalid"));
    }
    if !valid_identifier(&record.keyspace) {
        return Err(invalid("Vault Scylla keyspace is invalid"));
    }

    // [COMMENT]: Bắt buộc phải có tài khoản xác thực (username/password)
    if record.username.trim().is_empty() || record.password.trim().is_empty() {
        return Err(invalid("Vault Scylla credentials are required"));
    }

    // [COMMENT]: Xác thực cấu hình TLS dựa trên tls_mode
    let tls_mode = match record.tls_mode.trim().to_ascii_lowercase().as_str() {
        "disabled" => {
            // [COMMENT]: Nếu chế độ disabled, không được phép cấu hình đường dẫn chứng chỉ TLS
            if record.ca_cert_path.is_some()
                || record.client_cert_path.is_some()
                || record.client_key_path.is_some()
            {
                return Err(invalid(
                    "Vault Scylla TLS material is not allowed when tls_mode=disabled",
                ));
            }
            ScyllaTlsMode::Disabled
        }
        "server" => {
            // [COMMENT]: Chế độ Server TLS: chỉ cần CA cert để verify server, không có client cert/key
            if record.client_cert_path.is_some() || record.client_key_path.is_some() {
                return Err(invalid(
                    "Vault Scylla server TLS must not include client certificate material",
                ));
            }
            if record.ca_cert_path.is_none() {
                return Err(invalid("Vault Scylla server TLS requires ca_cert_path"));
            }
            ScyllaTlsMode::Server
        }
        "mutual" => {
            // [COMMENT]: Chế độ Mutual TLS (mTLS): bắt buộc phải có đủ CA, client certificate và private key
            if record.ca_cert_path.is_none()
                || record.client_cert_path.is_none()
                || record.client_key_path.is_none()
            {
                return Err(invalid(
                    "Vault Scylla mutual TLS requires CA, client certificate and key",
                ));
            }
            ScyllaTlsMode::Mutual
        }
        _ => return Err(invalid("Vault Scylla tls_mode is invalid")),
    };

    // [COMMENT]: Cập nhật các thông số đã xác thực từ Vault vào cấu hình ứng dụng
    config.contact_points = contact_points;
    config.local_dc = record.local_dc;
    config.keyspace = record.keyspace;
    config.username = record.username;
    config.password = record.password;
    config.tls = crate::config::ScyllaTlsConfig {
        mode: tls_mode,
        ca_cert: record.ca_cert_path.map(Into::into),
        client_cert: record.client_cert_path.map(Into::into),
        client_key: record.client_key_path.map(Into::into),
    };
    Ok(())
}

// [COMMENT]: Khởi tạo phiên kết nối (Session) tới cụm ScyllaDB
pub async fn connect(config: &ScyllaConfig) -> Result<Arc<Session>, AppError> {
    // [COMMENT]: Xây dựng ExecutionProfile mặc định với mức nhất quán LocalQuorum và cấu hình timeout
    let profile = ExecutionProfile::builder()
        .consistency(Consistency::LocalQuorum)
        .request_timeout(Some(config.request_timeout))
        .build();

    // [COMMENT]: Thiết lập các thông số khởi tạo Session (contact points, user, dc ưu tiên, connection timeout)
    let mut builder = config
        .contact_points
        .iter()
        .fold(SessionBuilder::new(), |builder, node| {
            builder.known_node(node)
        })
        .user(&config.username, &config.password)
        .prefer_datacenter(config.local_dc.clone())
        .connection_timeout(config.connect_timeout)
        .default_execution_profile_handle(profile.into_handle());

    // [COMMENT]: Nếu TLS được kích hoạt, gắn rustls context vào SessionBuilder
    if config.tls.mode != ScyllaTlsMode::Disabled {
        builder = builder.tls_context(Some(Arc::new(build_tls(config)?)));
    }

    // [COMMENT]: Thực thi kết nối để tạo Session ScyllaDB
    let session = Arc::new(builder.build().await?);

    // [COMMENT]: Nếu auto_schema bật, tự động tạo keyspace và các bảng nếu chưa tồn tại
    if config.auto_schema {
        super::schema::ensure(&session, config).await?;
    }

    // [COMMENT]: Chuyển session làm việc vào keyspace mục tiêu
    session.use_keyspace(&config.keyspace, false).await?;

    // [COMMENT]: Kiểm tra và xác nhận cấu trúc schema (bảng, cột) đã sẵn sàng phục vụ
    super::schema::verify(&session).await?;
    Ok(session)
}

// [COMMENT]: Xây dựng cấu hình TLS (rustls ClientConfig) cho ScyllaDB client
fn build_tls(config: &ScyllaConfig) -> Result<rustls::ClientConfig, AppError> {
    // [COMMENT]: Đọc chứng chỉ Root CA từ đường dẫn cấu hình
    let ca_path = config
        .tls
        .ca_cert
        .as_deref()
        .ok_or_else(|| invalid("Scylla TLS CA path is missing"))?;
    let mut roots = rustls::RootCertStore::empty();
    for certificate in read_certificates(ca_path)? {
        roots.add(certificate)?;
    }
    if roots.is_empty() {
        return Err(invalid("Scylla TLS root store is empty"));
    }

    let builder = rustls::ClientConfig::builder().with_root_certificates(roots);
    match config.tls.mode {
        ScyllaTlsMode::Disabled => Err(invalid("Scylla TLS builder called in disabled mode")),
        ScyllaTlsMode::Server => Ok(builder.with_no_client_auth()),
        ScyllaTlsMode::Mutual => {
            // [COMMENT]: Đọc client certificate và private key cho xác thực mTLS hai chiều
            let cert_path = config
                .tls
                .client_cert
                .as_deref()
                .ok_or_else(|| invalid("Scylla mTLS client certificate path is missing"))?;
            let key_path = config
                .tls
                .client_key
                .as_deref()
                .ok_or_else(|| invalid("Scylla mTLS client key path is missing"))?;
            let certificates = read_certificates(cert_path)?;
            let private_key = read_private_key(key_path)?;
            Ok(builder.with_client_auth_cert(certificates, private_key)?)
        }
    }
}

// [COMMENT]: Đọc danh sách chứng chỉ X.509 định dạng PEM từ file
fn read_certificates(path: &Path) -> Result<Vec<CertificateDer<'static>>, AppError> {
    let mut reader = BufReader::new(File::open(path)?);
    let certificates = rustls_pemfile::certs(&mut reader).collect::<Result<Vec<_>, _>>()?;
    if certificates.is_empty() {
        return Err(invalid("certificate file contains no certificates"));
    }
    Ok(certificates)
}

// [COMMENT]: Đọc private key từ file PEM
fn read_private_key(path: &Path) -> Result<PrivateKeyDer<'static>, AppError> {
    let mut reader = BufReader::new(File::open(path)?);
    rustls_pemfile::private_key(&mut reader)?
        .ok_or_else(|| invalid("private key file contains no supported key"))
}

// [COMMENT]: Helper khởi tạo lỗi InvalidInput
fn invalid(message: impl Into<String>) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidInput, message.into()).into()
}

// [COMMENT]: Kiểm tra định danh Cassandra/ScyllaDB hợp lệ (chữ thường, số, dấu gạch dưới, dài tối đa 48 ký tự)
fn valid_identifier(value: &str) -> bool {
    value.len() <= 48
        && value.as_bytes().first().is_some_and(u8::is_ascii_lowercase)
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_')
}
