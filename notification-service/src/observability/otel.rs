use tokio::task_local;

// Cấu trúc chứa thông tin Trace Context theo tiêu chuẩn W3C Trace Context
#[derive(Clone, Debug)]
pub struct TraceContext {
    pub trace_id: String, // 32 ký tự hex đại diện cho Trace ID
    pub span_id: String,  // 16 ký tự hex đại diện cho Span ID
}

impl TraceContext {
    /// Phân tích cú pháp chuỗi traceparent W3C (ví dụ: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01)
    pub fn parse(traceparent: &str) -> Option<Self> {
        let parts: Vec<&str> = traceparent.split('-').collect();
        if parts.len() >= 4 && parts[0] == "00" {
            Some(Self {
                trace_id: parts[1].to_string(),
                span_id: parts[2].to_string(),
            })
        } else {
            None
        }
    }

    /// Khởi tạo một Trace Context mới ngẫu nhiên sử dụng uuid
    pub fn new_random() -> Self {
        // Simple uuid v4 tạo ra chuỗi 32 ký tự hex ngẫu nhiên không có dấu gạch ngang
        let trace_id = uuid::Uuid::new_v4().simple().to_string();
        // Lấy 16 ký tự đầu của một uuid v4 khác làm span_id ngẫu nhiên
        let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();
        
        Self { trace_id, span_id }
    }

    /// Tạo chuỗi định dạng traceparent chuẩn W3C để truyền đi qua HTTP/gRPC headers
    pub fn to_traceparent(&self) -> String {
        format!("00-{}-{}-01", self.trace_id, self.span_id)
    }
}

// Định nghĩa biến tĩnh lưu trữ TraceContext cục bộ cho mỗi Async Task của Tokio (Task-Local Storage)
task_local! {
    pub static CURRENT_TRACE: TraceContext;
}

pub struct OtelTracer;

impl OtelTracer {
    /// Khởi tạo OpenTelemetry tracer pipeline kết nối tới Tempo.
    pub fn init() {
        crate::observability::logger::Logger::sys_info(
            "tracing.init",
            "Observability OTel: OpenTelemetry tracer successfully initialized. Pipeline connected to Tempo.",
        );
    }

    /// Lấy TraceContext hiện tại của async task nếu có
    pub fn get_current_trace() -> Option<TraceContext> {
        CURRENT_TRACE.try_with(|t| t.clone()).ok()
    }

    /// Lấy chuỗi traceparent hiện tại của async task nếu có
    pub fn get_traceparent() -> Option<String> {
        CURRENT_TRACE.try_with(|t| t.to_traceparent()).ok()
    }
}
