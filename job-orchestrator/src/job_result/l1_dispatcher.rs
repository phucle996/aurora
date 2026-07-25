use super::notifier::JobNotifier;
use crate::observability::logger::Logger;
use crate::reverse_provider;

pub mod job_proto {
    include!(concat!(env!("OUT_DIR"), "/job_lifecycle.rs"));
}

// [COMMENT]: L1 Dispatcher cập nhật business state rồi enqueue notification
// nội vùng Central qua Shared L2 Redis; NATS Core không nằm trên result path.
pub async fn dispatch_result(
    payload_bytes: &[u8],
    pg_client: &mut tokio_postgres::Client,
    notification_redis: &mut redis::aio::ConnectionManager,
) -> Result<(), Box<dyn std::error::Error>> {
    use prost::Message;

    let result = match job_proto::JobExecutionResultProto::decode(payload_bytes) {
        Ok(res) => res,
        Err(e) => {
            Logger::sys_error(
                "job_result.decode",
                "Không thể giải mã Protobuf payload từ Kafka",
                &e.to_string(),
            );
            return Err(Box::new(e));
        }
    };

    // Convert UUID bytes từ Protobuf thành chuỗi string UUID
    let job_id = uuid::Uuid::from_slice(&result.job_id)
        .map(|u| u.to_string())
        .unwrap_or_default();

    Logger::job_log(
        &job_id,
        &result.job_topic,
        result.attempt,
        "job_result.recv",
        &format!("Nhận kết quả: status={}", result.result_status),
    );

    let status = result.result_status.clone();
    let error_code = result.error_code.as_deref();
    let error_message = if status == "SUCCEEDED" || status == "PROCESSING" {
        None
    } else {
        Some(result.message.as_str())
    };

    let job_uuid = uuid::Uuid::from_slice(&result.job_id).unwrap_or_default();

    let db_context = crate::observability::otel::OtelTracer::start_current_span(
        "UPDATE controlplane job result",
        opentelemetry::trace::SpanKind::Client,
        vec![
            opentelemetry::KeyValue::new("db.system", "postgresql"),
            opentelemetry::KeyValue::new("db.operation.name", "update"),
            opentelemetry::KeyValue::new("aurora.source.domain", result.source_domain.clone()),
        ],
    );
    use opentelemetry::trace::FutureExt;
    // [COMMENT]: L1 chỉ route source domain; Mail/Storage L2 sở hữu transaction
    // update và lifecycle event. Span kết thúc sau đúng transaction boundary.
    let db_result: Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error>> = async {
        if result.source_domain == "MAIL" {
            reverse_provider::mail::l2_dispatcher::dispatch_mail_result(
                pg_client,
                job_uuid,
                &result.job_topic,
                &status,
                result.attempt,
                error_code,
                error_message,
            )
            .await
        } else if result.source_domain == "STORAGE" {
            reverse_provider::storage::l2_dispatcher::dispatch_storage_result(
                pg_client,
                job_uuid,
                &result.job_topic,
                &status,
                error_code,
                error_message,
            )
            .await
            .map_err(|error| Box::<dyn std::error::Error>::from(error.to_string()))
        } else {
            Err(format!("unsupported source_domain '{}'", result.source_domain).into())
        }
    }
    .with_context(db_context.clone())
    .await;
    crate::observability::otel::OtelTracer::finish_span(
        &db_context,
        db_result
            .as_ref()
            .err()
            .map(|_| "POSTGRES_JOB_RESULT_UPDATE_FAILED"),
    );
    let row_opt = db_result?;

    if let Some(row) = row_opt {
        Logger::job_log(
            &job_id,
            &result.job_topic,
            result.attempt,
            "job_result.update",
            &format!("Cập nhật thành công DB -> Trạng thái {}", status),
        );

        // [COMMENT]: actor_user_id chỉ dùng để gửi notification; payer của billing
        // được truyền riêng bằng owner_id/owner_type trong lifecycle event.
        let actor_user_id: Option<String> = row.get(0);
        let job_topic: String = row.get(1);
        let resource_id = row.get::<_, Option<String>>(3).unwrap_or_default();

        let result_job_id = job_id.clone();
        let result_message = result.message.clone();
        let actor_user_id = actor_user_id.unwrap_or_default();
        let user_id_clone = actor_user_id.clone();
        let status_clone = status.clone();
        let job_topic_clone = job_topic.clone();

        if !user_id_clone.is_empty() {
            let notify_context = crate::observability::otel::OtelTracer::start_current_span(
                "send stream:{job_notifications}",
                opentelemetry::trace::SpanKind::Producer,
                vec![
                    opentelemetry::KeyValue::new("messaging.system", "redis"),
                    opentelemetry::KeyValue::new("messaging.operation.type", "send"),
                    opentelemetry::KeyValue::new(
                        "messaging.destination.name",
                        "stream:{job_notifications}",
                    ),
                    opentelemetry::KeyValue::new("aurora.job.id", result_job_id.clone()),
                ],
            );
            let propagation =
                crate::observability::otel::OtelTracer::inject_context(&notify_context);
            let notify_res = JobNotifier::notify_realtime(
                &result_job_id,
                &user_id_clone,
                result.job_version,
                result.attempt,
                &status_clone,
                &job_topic_clone,
                &resource_id,
                &result_message,
                &propagation.traceparent,
                &propagation.tracestate,
                notification_redis,
            )
            .with_context(notify_context.clone())
            .await;
            crate::observability::otel::OtelTracer::finish_span(
                &notify_context,
                notify_res
                    .as_ref()
                    .err()
                    .map(|_| "REDIS_NOTIFICATION_XADD_FAILED"),
            );

            if let Err(e) = notify_res {
                return Err(e);
            }
        }
    } else {
        Logger::job_log(
                    &job_id,
                    &result.job_topic,
                    result.attempt,
                    "job_result.update_skip",
                    "Không thể cập nhật Job hoặc không tìm thấy bản ghi phù hợp (có thể đã hoàn thành trước đó hoặc lệch topic)",
                );
    }

    Ok(())
}
