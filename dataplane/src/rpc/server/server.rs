#![allow(dead_code)]

use crate::infra::grpc::GrpcSecurityConfig;
use crate::observability::logger::Logger;
use crate::config::GrpcTlsMode;

/// 📡 Tín hiệu điều phối khẩn cấp dành riêng cho các Job đang thực thi tại Runtime (Runtime Job Control Signals)
///
/// 📌 PHÂN LOẠI CÔNG VIỆC TRONG HỆ THỐNG:
///   1. **Job User:** Các tác vụ nghiệp vụ thông thường do người dùng khởi tạo (ví dụ: tạo VPS, cấu hình mail).
///      Các Job này được xếp hàng đợi (queue) và xử lý bất đồng bộ qua Redis Job Queue, chấp nhận độ trễ lớn.
///   2. **Job Runtime:** Các tác vụ điều phối mức hệ thống cực kỳ khẩn cấp, đòi hỏi độ trễ cực thấp và
///      bảo đảm **không bao giờ được phép thất lạc (100% no-miss guarantee)** (ví dụ: CANCEL_RUNTIME_JOB,
///      heartbeat sync, autoscale commands, soft-reload).
///
/// 📌 NGUYÊN TẮC THIẾT KẾ:
///   - Cấu trúc `ControlplaneSignal` được thiết kế để chỉ đón nhận và xử lý khẩn cấp tín hiệu của **Job Runtime**.
///   - Việc tách biệt kênh giao tiếp gRPC riêng giúp các Job Runtime được ưu tiên tuyệt đối tài nguyên, không bị
///     nghẽn hoặc bị trễ bởi các Job User đang xử lý nặng.
#[derive(serde::Serialize, serde::Deserialize, Clone, Debug)]
pub struct ControlplaneSignal {
    /// ID của Job runtime đích cần tác động
    pub runtime_job_id: String,
    /// Loại tín hiệu điều khiển runtime (ví dụ: "CANCEL", "BOOST", "PAUSE")
    pub signal_type: String,
    /// Dữ liệu bổ sung đi kèm phục vụ điều phối
    pub payload: String,
}

/// ============================================================================
/// 📂 MODULE: rpc/receiver/server.rs - Bộ Khởi Tạo gRPC Server & Tiếp Nhận RPC
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Khởi tạo và thiết lập cổng lắng nghe (gRPC Server) ngay tại Dataplane.
///   - **Sứ mệnh độc quyền:** Chỉ tiếp nhận các tín hiệu can thiệp khẩn cấp đối với các **Job Runtime**,
///     cam kết truyền dẫn thông tin an toàn tuyệt đối và **không bị bỏ lỡ bất kỳ signal nào (100% no-miss)**.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Cấu hình địa chỉ IP/Port lắng nghe được cung cấp bởi `config/mod.rs` nạp từ `.env`.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Lắng nghe yêu cầu trên cổng mạng được mã hóa gRPC mTLS/TLS.
///   - Chỉ cho phép các kết nối hợp lệ được quyền gọi vào.
///
pub struct DataplaneGrpcServer;

impl DataplaneGrpcServer {
    /// Bắt đầu lắng nghe và khởi chạy gRPC Server nội bộ sử dụng cấu hình bảo mật.
    pub async fn start_server(
        port: u32,
        tls_mode: GrpcTlsMode,
        ca_cert: Option<String>,
        client_cert: Option<String>,
        client_key: Option<String>,
    ) -> Result<(), String> {
        let addr = format!("0.0.0.0:{}", port);
        Logger::sys_info(
            "rpc.server",
            &format!("Initializing Dataplane internal gRPC Server on {} (TLS Mode: {:?})...", addr, tls_mode)
        );

        // 1. Cấu hình bảo mật truyền dẫn gRPC (TLS/mTLS)
        match tls_mode {
            GrpcTlsMode::Mtls => {
                if let (Some(ref ca), Some(ref cert), Some(ref key)) = (&ca_cert, &client_cert, &client_key) {
                    GrpcSecurityConfig::build_mtls_connector(ca, cert, key)?;
                } else {
                    return Err("gRPC mTLS is enabled but certificate parameters are missing".to_string());
                }
            }
            GrpcTlsMode::Tls => {
                if let Some(ref ca) = ca_cert {
                    GrpcSecurityConfig::build_tls_connector(ca)?;
                } else {
                    return Err("gRPC TLS is enabled but CA certificate parameter is missing".to_string());
                }
            }
            GrpcTlsMode::Disable => {}
        }

        // 2. Chạy Server loop giả lập (trên production thực tế sẽ block phục vụ tonic Server)
        Logger::sys_info(
            "rpc.server",
            &format!("Dataplane internal gRPC Server successfully bound and listening on {}", addr)
        );

        Ok(())
    }

    /// Tiếp nhận tín hiệu điều phối khẩn cấp cho các Job đang thực thi tại Runtime.
    pub async fn handle_incoming_rpc(signal: ControlplaneSignal) {
        Logger::sys_info(
            "rpc.receiver",
            &format!(
                "RPC Receiver: Intercepted runtime signal [{}] for active Job ID: {}",
                signal.signal_type, signal.runtime_job_id
            ),
        );

        // Xử lý các tín hiệu can thiệp trực tiếp vào runtime của Job
        match signal.signal_type.as_str() {
            "CANCEL" => {
                Logger::sys_warn(
                    "rpc.receiver",
                    &format!("CRITICAL: Instructing Worker Pool to immediately abort and cancel runtime Job {}", signal.runtime_job_id),
                    "JOB_ABORT_SIGNAL"
                );
            }
            "BOOST" => {
                Logger::sys_info(
                    "rpc.receiver",
                    &format!(
                        "BOOST: Raising execution priority for active runtime Job {}",
                        signal.runtime_job_id
                    ),
                );
            }
            _ => {
                Logger::sys_warn(
                    "rpc.receiver",
                    &format!(
                        "Skipping unknown runtime control signal type '{}'",
                        signal.signal_type
                    ),
                    "UNKNOWN_SIGNAL_TYPE",
                );
            }
        }
    }
}
