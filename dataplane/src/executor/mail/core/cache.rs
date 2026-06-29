use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
use futures_util::StreamExt;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::{Mutex, OnceLock};

/// Struct chứa dữ liệu hoàn chỉnh của một email template
#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct MailTemplate {
    pub subject: String,
    pub body: String,
}

/// ============================================================================
/// 📂 L1 CACHE (IN-MEMORY) CHO MAIL TEMPLATES
/// ============================================================================
fn get_l1_cache() -> &'static Mutex<HashMap<String, MailTemplate>> {
    static CACHE: OnceLock<Mutex<HashMap<String, MailTemplate>>> = OnceLock::new();
    CACHE.get_or_init(|| Mutex::new(HashMap::new()))
}

/// Đọc template từ L1 cache
fn read_l1_cache(template_id: &str) -> Option<MailTemplate> {
    let cache = get_l1_cache().lock().ok()?;
    cache.get(template_id).cloned()
}

/// Ghi template vào L1 cache
fn write_l1_cache(template_id: &str, template: MailTemplate) {
    if let Ok(mut cache) = get_l1_cache().lock() {
        cache.insert(template_id.to_string(), template);
    }
}

/// Truy xuất template thông qua cơ chế L1 -> L2 -> PubSub Hỏi Job Proxy (kèm Distributed Tracing)
#[allow(deprecated)]
pub async fn get_template(
    redis_mgr: &RedisClientManager,
    template_id: &str,
) -> Result<MailTemplate, String> {
    // 1. Kiểm tra L1 Cache (Memory)
    if let Some(template) = read_l1_cache(template_id) {
        Logger::sys_info(
            "core.mail.cache",
            &format!("L1 Cache Hit cho template_id: {}", template_id),
        );
        return Ok(template);
    }

    let client = redis_mgr.client();

    // 2. Kiểm tra L2 Cache (Redis Zone) - Lưu dạng JSON String
    let redis_key = format!("cache:mail_template:v2:{}", template_id);
    let mut conn = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| format!("Không thể lấy kết nối Redis L2: {}", e))?;

    if let Ok(Some(cached_json)) = redis::cmd("GET")
        .arg(&redis_key)
        .query_async::<_, Option<String>>(&mut conn)
        .await
    {
        if let Ok(template) = serde_json::from_str::<MailTemplate>(&cached_json) {
            Logger::sys_info(
                "core.mail.cache",
                &format!("L2 Cache Hit cho template_id: {}", template_id),
            );
            write_l1_cache(template_id, template.clone());
            return Ok(template);
        }
    }

    // 3. L2 Cache Miss -> Gửi yêu cầu PubSub đến Job Proxy ở tầng Platform
    Logger::sys_info(
        "core.mail.cache",
        &format!(
            "L2 Cache Miss. Đang gửi PubSub hỏi Job Proxy cho template_id: {}",
            template_id
        ),
    );

    // Lấy trace_id hiện hành từ OpenTelemetry Context để liên kết vết (Context Propagation)
    let current_trace_id = crate::observability::otel::OtelTracer::get_current_trace_id()
        .unwrap_or_else(|| "".to_string());

    let request_id = uuid::Uuid::new_v4().to_string();
    let request_channel = "mail.template.request";
    let response_channel = format!("mail.template.response:{}", request_id);

    // Thiết lập subscriber lắng nghe trên response channel
    let conn_pubsub = client
        .get_async_connection()
        .await
        .map_err(|e| format!("Không thể lấy kết nối async Redis cho PubSub: {}", e))?;
    let mut pubsub = conn_pubsub.into_pubsub();
    pubsub
        .subscribe(&response_channel)
        .await
        .map_err(|e| format!("Không thể subscribe kênh phản hồi: {}", e))?;

    // Publish yêu cầu lấy template (inject thêm trace_id để Job Proxy theo vết)
    let req_payload = serde_json::json!({
        "request_id": request_id,
        "template_id": template_id,
        "reply_to": response_channel,
        "trace_id": current_trace_id
    });

    let _: () = redis::cmd("PUBLISH")
        .arg(request_channel)
        .arg(req_payload.to_string())
        .query_async(&mut conn)
        .await
        .map_err(|e| format!("Không thể publish template request: {}", e))?;

    // Lắng nghe phản hồi từ Job Proxy kèm cơ chế Timeout 5 giây
    let mut stream = pubsub.on_message();
    let template_id_str = template_id.to_string();

    let template = tokio::time::timeout(tokio::time::Duration::from_secs(5), async move {
        if let Some(msg) = stream.next().await {
            let payload: String = msg.get_payload().map_err(|e| e.to_string())?;
            if let Ok(res) = serde_json::from_str::<serde_json::Value>(&payload) {
                // Trích xuất trace_id phản hồi từ Job Proxy
                let response_trace_id = res
                    .get("trace_id")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string();

                // Đóng gói và chạy tiến trình phân tích template trong Trace ID Scope
                let parse_result = crate::observability::otel::CURRENT_TRACE_ID
                    .scope(response_trace_id.clone(), async move {
                        use opentelemetry::trace::{Span, TraceContextExt, Tracer};

                        // Khôi phục Span Context cha từ trace_id nhận được
                        let cx = if let Some(parent_ctx) =
                            crate::observability::otel::OtelTracer::parse_traceparent(
                                &response_trace_id,
                            ) {
                            opentelemetry::Context::current().with_remote_span_context(parent_ctx)
                        } else {
                            opentelemetry::Context::current()
                        };

                        let tracer = opentelemetry::global::tracer("dataplane");
                        let mut span = tracer.start_with_context(
                            format!("core.mail.cache.receive.{}", template_id_str),
                            &cx,
                        );

                        let subject = res
                            .get("subject")
                            .and_then(|s| s.as_str())
                            .unwrap_or("No Subject")
                            .to_string();
                        let body = res
                            .get("content")
                            .or_else(|| res.get("body"))
                            .and_then(|c| c.as_str())
                            .unwrap_or("")
                            .to_string();

                        span.set_attribute(opentelemetry::KeyValue::new(
                            "template_id",
                            template_id_str.clone(),
                        ));

                        Ok(MailTemplate { subject, body })
                    })
                    .await;

                return parse_result;
            }
            Err("Dữ liệu phản hồi template từ Job Proxy không hợp lệ".to_string())
        } else {
            Err("Không nhận được tin nhắn phản hồi từ kênh PubSub".to_string())
        }
    })
    .await
    .map_err(|_| "Timeout (5s) chờ đợi Job Proxy phản hồi template content".to_string())??;

    // Lưu lại kết quả vào L2 (TTL 1 giờ) và L1 cache dưới dạng JSON
    if let Ok(serialized) = serde_json::to_string(&template) {
        let _: redis::RedisResult<()> = redis::cmd("SETEX")
            .arg(&redis_key)
            .arg(3600)
            .arg(&serialized)
            .query_async(&mut conn)
            .await;
    }

    write_l1_cache(template_id, template.clone());
    Ok(template)
}
