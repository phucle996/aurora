use crate::config::Config;
use crate::observability::logger::Logger;
use futures_util::StreamExt;

/// Lắng nghe các yêu cầu tài nguyên từ Dataplane qua Redis PubSub và trả về kết quả kèm distributed trace context.
#[allow(deprecated)]
pub async fn run_listener(
    config: &Config,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    // Nhận kết nối async chuyên biệt cho PubSub
    let conn_pubsub = redis_client.get_async_connection().await?;
    let mut pubsub = conn_pubsub.into_pubsub();

    // Subscribe kênh yêu cầu mail template
    pubsub.subscribe("mail.template.request").await?;

    let mut stream = pubsub.on_message();
    Logger::sys_info(
        "reverse_provider.listener",
        "ReverseProvider: Đang lắng nghe kênh 'mail.template.request'...",
    );

    while let Some(msg) = stream.next().await {
        let payload_str: String = match msg.get_payload() {
            Ok(p) => p,
            Err(e) => {
                Logger::sys_error(
                    "reverse_provider.listener",
                    "Lỗi nhận payload từ Redis",
                    &e.to_string(),
                );
                continue;
            }
        };

        let req: serde_json::Value = match serde_json::from_str(&payload_str) {
            Ok(v) => v,
            Err(_) => continue,
        };

        let request_id = match req.get("request_id").and_then(|v| v.as_str()) {
            Some(id) => id.to_string(),
            None => continue,
        };

        let template_id = match req.get("template_id").and_then(|v| v.as_str()) {
            Some(id) => id.to_string(),
            None => continue,
        };

        let reply_to = match req.get("reply_to").and_then(|v| v.as_str()) {
            Some(ch) => ch.to_string(),
            None => continue,
        };

        // Trích xuất trace_id được inject từ Dataplane
        let trace_id = req
            .get("trace_id")
            .and_then(|v| v.as_str())
            .unwrap_or("")
            .to_string();

        Logger::sys_info(
            "reverse_provider.listener",
            &format!(
                "Nhận yêu cầu template_id '{}' (Request ID: {}, Trace ID: {})",
                template_id, request_id, trace_id
            ),
        );

        let config_clone = config.clone();
        let template_id_clone = template_id.clone();
        let trace_id_clone = trace_id.clone();

        // Chạy dispatcher xử lý trong Scope của Trace ID truyền từ Dataplane
        let dispatch_result = crate::observability::otel::CURRENT_TRACE_ID
            .scope(trace_id_clone.clone(), async move {
                use opentelemetry::trace::{Span, TraceContextExt, Tracer};

                // 1. Phân tích trace context cha truyền từ Dataplane
                let cx = if let Some(parent_ctx) =
                    crate::observability::otel::OtelTracer::parse_traceparent(&trace_id_clone)
                {
                    opentelemetry::Context::current().with_remote_span_context(parent_ctx)
                } else {
                    opentelemetry::Context::current()
                };

                // 2. Khởi tạo một span nghiệp vụ mới để gửi lên OpenTelemetry collector (Tempo)
                let tracer = opentelemetry::global::tracer("job-proxy");
                let mut span = tracer.start_with_context(
                    format!("reverse_provider.template.{}", template_id_clone),
                    &cx,
                );

                span.set_attribute(opentelemetry::KeyValue::new(
                    "template_id",
                    template_id_clone.clone(),
                ));
                span.set_attribute(opentelemetry::KeyValue::new(
                    "request_id",
                    request_id.clone(),
                ));

                // 3. Thực hiện điều phối và xử lý truy vấn
                let dispatch_res =
                    super::dispatcher::dispatch_request(&config_clone, &template_id_clone).await;

                match dispatch_res {
                    Ok(mut res_data) => {
                        // Inject lại trace_id vào response JSON payload
                        if let Some(obj) = res_data.as_object_mut() {
                            obj.insert(
                                "trace_id".to_string(),
                                serde_json::Value::String(trace_id_clone),
                            );
                        }
                        Ok(res_data)
                    }
                    Err(e) => {
                        span.record_error(e.as_ref());
                        Err(e)
                    }
                }
            })
            .await;

        match dispatch_result {
            Ok(res_data) => {
                let mut conn = redis_client.get_multiplexed_tokio_connection().await?;
                let _: () = redis::cmd("PUBLISH")
                    .arg(&reply_to)
                    .arg(res_data.to_string())
                    .query_async(&mut conn)
                    .await?;

                Logger::sys_info(
                    "reverse_provider.listener",
                    &format!(
                        "Đã trả kết quả template '{}' qua kênh {}",
                        template_id, reply_to
                    ),
                );
            }
            Err(e) => {
                Logger::sys_error(
                    "reverse_provider.listener",
                    &format!("Thất bại khi xử lý template_id '{}'", template_id),
                    &e.to_string(),
                );
            }
        }
    }

    Ok(())
}
