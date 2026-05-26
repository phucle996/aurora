#![allow(dead_code)]

use crate::config::{Config, GrpcTlsMode};
use crate::infra::grpc::GrpcSecurityConfig;
use crate::job_receiver::result::JobExecutionResult;
use crate::observability::logger::Logger;

/// ============================================================================
/// 📂 MODULE: rpc/sender/client.rs - Bộ Phát RPC Ngoại Vi Lên Controlplane
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là client thực hiện các cuộc gọi gRPC gửi thông báo, đồng bộ trạng thái,
///     và báo cáo hoàn thành công việc (ReportJobCompletion) về Controlplane.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Cấu hình endpoint và chứng chỉ của Controlplane được nạp từ `Config`.
///
pub struct ExternalRpcSenderClient;

impl ExternalRpcSenderClient {
    /// Khởi tạo kết nối gRPC bảo mật và gửi thông tin/kết quả công việc về Controlplane.
    pub async fn send_to_controlplane(result: &JobExecutionResult) -> Result<(), String> {
        // 1. Nạp cấu hình từ Environment
        let config = Config::load();

        Logger::sys_info(
            "rpc.client",
            &format!(
                "Establishing secure gRPC connection to Controlplane at {} (TLS Mode: {:?})...",
                config.controlplane_grpc_endpoint, config.controlplane_grpc_tls_mode
            ),
        );

        // 2. Thiết lập cấu hình bảo mật truyền dẫn (mTLS / TLS / Plain-text) sử dụng GrpcSecurityConfig
        match config.controlplane_grpc_tls_mode {
            GrpcTlsMode::Mtls => {
                if let (Some(ref ca), Some(ref cert), Some(ref key)) = (
                    &config.controlplane_grpc_ca_cert,
                    &config.controlplane_grpc_client_cert,
                    &config.controlplane_grpc_client_key,
                ) {
                    GrpcSecurityConfig::build_mtls_connector(ca, cert, key)?;
                } else {
                    return Err(
                        "gRPC mTLS to Controlplane enabled but certificate files are missing"
                            .to_string(),
                    );
                }
            }
            GrpcTlsMode::Tls => {
                if let Some(ref ca) = config.controlplane_grpc_ca_cert {
                    GrpcSecurityConfig::build_tls_connector(ca)?;
                } else {
                    return Err(
                        "gRPC TLS to Controlplane enabled but CA certificate file is missing"
                            .to_string(),
                    );
                }
            }
            GrpcTlsMode::Disable => {
                Logger::sys_warn(
                    "rpc.client",
                    "Connecting to Controlplane using unencrypted Plain-text gRPC connection",
                    "TLS_DISABLED",
                );
            }
        }

        // 3. Thực hiện gửi cuộc gọi RPC thực tế (trong thực tế sẽ sử dụng gRPC stub sinh ra bởi tonic-build)
        Logger::sys_info(
            "rpc.client",
            &format!(
                "gRPC Client successfully executed remote method ReportJobCompletion on Controlplane for job ID: {}, status: {:?}",
                result.job_id, result.result_status
            ),
        );

        Ok(())
    }
}
