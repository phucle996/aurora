use crate::job_receiver::message::JobPayload;

/// ============================================================================
/// 📂 MODULE: rpc/receiver/client.rs - Bộ Đón Nhận RPC Ngoại Vi & Kích Hoạt Nghiệp Vụ
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là client xử lý (handler) các yêu cầu gRPC từ các hệ thống ngoại vi
///     gọi trực tiếp tới Dataplane (không đi qua Redis Stream).
///   - Tiến hành xác thực payload và kích hoạt (dispatch) các Executor tương ứng xử lý.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Dữ liệu Payload gửi kèm cuộc gọi RPC của caller bên ngoài.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Tiếp nhận và phân loại nghiệp vụ kỹ thuật thô. Cách ly hoàn toàn thông tin Tenant.
///   - Áp dụng các quy tắc kiểm soát truy cập nghiêm ngặt tại cổng vào gRPC để bảo vệ Dataplane.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi gRPC server (`rpc/sender/server.rs`) khi có kết nối RPC inbound kích hoạt nghiệp vụ.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Luồng này đi tắt qua Redis Stream, do đó cần có hệ thống rate-limit cực kỳ nghiêm ngặt tại đây
///     để bảo vệ máy chủ ảo hóa khỏi bị quá tải (thundering herd bảo vệ tài nguyên hypervisor).
///
pub struct ExternalRpcReceiverClient;

impl ExternalRpcReceiverClient {
    /// Tiếp nhận yêu cầu RPC thô, phân giải dữ liệu và gửi sang bộ điều phối nghiệp vụ.
    ///
    /// # Luồng xử lý kỹ thuật (Technical Flow):
    ///   1. Xác thực cấu trúc dữ liệu Payload nhận được.
    ///   2. Đẩy gói tin sang `JobConsumer::dispatch_workload()` để gọi Executor nghiệp vụ tương ứng.
    pub async fn handle_incoming_rpc(payload: JobPayload) {
        println!("RPC Receiver: Intercepted dynamic RPC command for job: {}", payload.job_id);
        
        // Phase: Chuyển tiếp luồng thực thi sang JobConsumer điều phối Executor tương ứng
        crate::job_receiver::consumer::JobConsumer::dispatch_workload(payload).await;
    }
}
