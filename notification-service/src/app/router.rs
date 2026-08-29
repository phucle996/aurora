use crate::app::state::AppState;
use crate::middleware::{http_telemetry_middleware, identity_middleware};
use crate::transport::http_handler::{
    handle_connect, list_activities, list_notifications, mark_all_notifications_read,
    mark_notification_read,
};
use axum::{
    extract::DefaultBodyLimit,
    middleware,
    routing::{get, post, put},
    Router,
};
use std::sync::Arc;
use tower_http::trace::TraceLayer;

pub fn build_router(state: Arc<AppState>) -> Router {
    // [COMMENT]: Nhóm các route cá nhân (/api/v1/me/...) yêu cầu định danh người dùng qua identity_middleware
    let me_routes = Router::new()
        .route("/activity/list", get(list_activities))
        .route("/notification/list", get(list_notifications))
        .route("/notification/read-all", put(mark_all_notifications_read))
        .route("/notification/:id/read", put(mark_notification_read))
        .layer(middleware::from_fn(identity_middleware));

    Router::new()
        .route("/api/v1/realtime/connect", post(handle_connect))
        .nest("/api/v1/me", me_routes)
        // [COMMENT]: Middleware tự động đo lường thời gian xử lý, ghi metric Prometheus và xuất access log cho toàn bộ routes
        .layer(middleware::from_fn(http_telemetry_middleware))
        // Centrifugo connect payloads are small; this prevents an attacker from
        // allocating an oversized JSON body before schema validation runs.
        .layer(DefaultBodyLimit::max(64 * 1024))
        .layer(TraceLayer::new_for_http())
        .with_state(state)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::repo::notification::NotificationView;
    use crate::repo::timeline::ActivityView;
    use crate::repo::{
        ActivityCategory, ActivityEvent, ActivityPage, NotificationItem, NotificationPage,
        NotificationRepo, TimelineRepo,
    };
    use crate::service::{
        AppError, AuthCredentials, AuthError, AuthVerifier, AuthenticatedPrincipal,
    };
    use axum::body::{to_bytes, Body};
    use axum::http::{Request, StatusCode};
    use chrono::{DateTime, Utc};
    use futures_util::future::BoxFuture;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::Mutex;
    use tower::ServiceExt;
    use uuid::Uuid;

    type ReadCall = (Uuid, String, DateTime<Utc>, Uuid);

    #[derive(Clone)]
    enum VerifierOutcome {
        Valid(Uuid),
        Invalid,
        Unavailable,
        Protocol,
    }

    struct TestVerifier {
        outcome: VerifierOutcome,
        calls: AtomicUsize,
    }

    impl AuthVerifier for TestVerifier {
        fn verify<'a>(
            &'a self,
            _credentials: AuthCredentials,
        ) -> BoxFuture<'a, Result<AuthenticatedPrincipal, AuthError>> {
            self.calls.fetch_add(1, Ordering::Relaxed);
            Box::pin(std::future::ready(match self.outcome {
                VerifierOutcome::Valid(id) => Ok(AuthenticatedPrincipal { id: id.to_string() }),
                VerifierOutcome::Invalid => Err(AuthError::Invalid),
                VerifierOutcome::Unavailable => Err(AuthError::Unavailable("private".to_string())),
                VerifierOutcome::Protocol => Err(AuthError::Protocol("private".to_string())),
            }))
        }
    }

    #[derive(Default)]
    struct TestStore {
        activity_list: Mutex<Vec<(Uuid, usize, Option<String>)>>,
        notification_list: Mutex<Vec<(Uuid, usize)>>,
        reads: Mutex<Vec<ReadCall>>,
        read_all: Mutex<Vec<Uuid>>,
        fail_reads: bool,
    }

    impl TimelineRepo for TestStore {
        fn persist_activity<'a>(
            &'a self,
            _event: ActivityEvent,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async { Ok(()) })
        }

        fn list_activity<'a>(
            &'a self,
            user_id: Uuid,
            _cursor: Option<&'a str>,
            category: Option<ActivityCategory>,
            limit: usize,
            _max_month_scan: usize,
        ) -> BoxFuture<'a, Result<ActivityPage, AppError>> {
            Box::pin(async move {
                if self.fail_reads {
                    return Err(test_error("scylla unavailable"));
                }
                self.activity_list.lock().expect("activity calls").push((
                    user_id,
                    limit,
                    category.map(|value| value.as_str().to_string()),
                ));
                Ok(ActivityPage {
                    items: Vec::<ActivityView>::new(),
                    next_cursor: None,
                })
            })
        }
    }

    impl NotificationRepo for TestStore {
        fn persist_notification<'a>(
            &'a self,
            _item: NotificationItem,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async { Ok(()) })
        }

        fn list_notifications<'a>(
            &'a self,
            user_id: Uuid,
            _cursor: Option<&'a str>,
            limit: usize,
            _max_month_scan: usize,
        ) -> BoxFuture<'a, Result<NotificationPage, AppError>> {
            Box::pin(async move {
                if self.fail_reads {
                    return Err(test_error("scylla unavailable"));
                }
                self.notification_list
                    .lock()
                    .expect("notification calls")
                    .push((user_id, limit));
                Ok(NotificationPage {
                    items: Vec::<NotificationView>::new(),
                    next_cursor: None,
                })
            })
        }

        fn mark_notification_read<'a>(
            &'a self,
            user_id: Uuid,
            month_bucket: &'a str,
            created_at: DateTime<Utc>,
            notification_id: Uuid,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async move {
                if self.fail_reads {
                    return Err(test_error("scylla unavailable"));
                }
                self.reads.lock().expect("read calls").push((
                    user_id,
                    month_bucket.to_string(),
                    created_at,
                    notification_id,
                ));
                Ok(())
            })
        }

        fn mark_all_notifications_read<'a>(
            &'a self,
            user_id: Uuid,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async move {
                if self.fail_reads {
                    return Err(test_error("scylla unavailable"));
                }
                self.read_all.lock().expect("read-all calls").push(user_id);
                Ok(())
            })
        }
    }

    fn test_error(message: &str) -> AppError {
        std::io::Error::other(message.to_string()).into()
    }

    fn app(
        outcome: VerifierOutcome,
        fail_reads: bool,
    ) -> (Router, Arc<TestStore>, Arc<TestVerifier>) {
        let store = Arc::new(TestStore {
            fail_reads,
            ..TestStore::default()
        });
        let verifier = Arc::new(TestVerifier {
            outcome,
            calls: AtomicUsize::new(0),
        });
        let state = Arc::new(AppState {
            authorizer: Arc::new(crate::service::ConnectAuthorizer::new(verifier.clone())),
            activities: Arc::new(crate::service::ActivityService::new(store.clone(), 50, 12)),
            inbox: Arc::new(crate::service::NotificationService::new(
                store.clone(),
                50,
                12,
            )),
        });
        (build_router(state), store, verifier)
    }

    #[tokio::test]
    async fn self_routes_require_verified_edge_identity() {
        let (app, _, _) = app(VerifierOutcome::Invalid, false);
        let response = app
            .oneshot(
                Request::builder()
                    .uri("/api/v1/me/activity/list")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    }

    #[tokio::test]
    async fn self_activity_uses_only_edge_subject_and_clamps_page_size() {
        let verified_user = Uuid::new_v4();
        let attacker_user = Uuid::new_v4();
        let (app, store, _) = app(VerifierOutcome::Invalid, false);
        let response = app
            .oneshot(
                Request::builder()
                    .uri(format!(
                        "/api/v1/me/activity/list?limit=999&category=security&user_id={attacker_user}"
                    ))
                    .header("x-user-id", verified_user.to_string())
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            store
                .activity_list
                .lock()
                .expect("activity calls")
                .as_slice(),
            &[(verified_user, 50, Some("security".to_string()))]
        );
    }

    #[tokio::test]
    async fn invalid_timeline_query_is_400_and_dependency_failure_is_503() {
        let user_id = Uuid::new_v4();
        let (validating_app, store, _) = app(VerifierOutcome::Invalid, false);
        let invalid = validating_app
            .oneshot(
                Request::builder()
                    .uri("/api/v1/me/activity/list?category=unknown")
                    .header("x-user-id", user_id.to_string())
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(invalid.status(), StatusCode::BAD_REQUEST);
        assert!(store
            .activity_list
            .lock()
            .expect("activity calls")
            .is_empty());

        let (failing_app, _, _) = app(VerifierOutcome::Invalid, true);
        let unavailable = failing_app
            .oneshot(
                Request::builder()
                    .uri("/api/v1/me/notification/list")
                    .header("x-user-id", user_id.to_string())
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(unavailable.status(), StatusCode::SERVICE_UNAVAILABLE);
    }

    #[tokio::test]
    async fn mark_read_derives_partition_from_timestamp_and_verified_subject() {
        let user_id = Uuid::new_v4();
        let notification_id = Uuid::new_v4();
        let created_at = "2026-01-31T23:59:59Z";
        let (app, store, _) = app(VerifierOutcome::Invalid, false);
        let response = app
            .oneshot(
                Request::builder()
                    .method("PUT")
                    .uri(format!("/api/v1/me/notification/{notification_id}/read"))
                    .header("content-type", "application/json")
                    .header("x-user-id", user_id.to_string())
                    .body(Body::from(format!(r#"{{"created_at":"{created_at}"}}"#)))
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::OK);
        let reads = store.reads.lock().expect("read calls");
        assert_eq!(reads.len(), 1);
        assert_eq!(reads[0].0, user_id);
        assert_eq!(reads[0].1, "2026-01");
        assert_eq!(reads[0].3, notification_id);
    }

    #[tokio::test]
    async fn realtime_connect_grants_only_durable_notification_channel() {
        let user_id = Uuid::new_v4();
        let (app, _, verifier) = app(VerifierOutcome::Valid(user_id), false);
        let response = app
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/v1/realtime/connect")
                    .header("content-type", "application/json")
                    .body(Body::from(
                        r#"{"client":"browser-1","request":{"headers":{"Cookie":"access_token=token; access_key=key; access_secret=secret"}}}"#,
                    ))
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(verifier.calls.load(Ordering::Relaxed), 1);
        let body = to_bytes(response.into_body(), 4096).await.expect("body");
        let payload: serde_json::Value = serde_json::from_slice(&body).expect("JSON response");
        assert_eq!(payload["result"]["user"], user_id.to_string());
        assert_eq!(
            payload["result"]["channels"],
            serde_json::json!([format!("notifications:{user_id}")])
        );
    }

    #[tokio::test]
    async fn realtime_connect_maps_auth_failures_without_leaking_dependency_details() {
        for (outcome, expected) in [
            (VerifierOutcome::Invalid, StatusCode::UNAUTHORIZED),
            (
                VerifierOutcome::Unavailable,
                StatusCode::SERVICE_UNAVAILABLE,
            ),
            (VerifierOutcome::Protocol, StatusCode::INTERNAL_SERVER_ERROR),
        ] {
            let (app, _, _) = app(outcome, false);
            let response = app
                .oneshot(
                    Request::builder()
                        .method("POST")
                        .uri("/api/v1/realtime/connect")
                        .header("content-type", "application/json")
                        .header(
                            "cookie",
                            "access_token=token; access_key=key; access_secret=secret",
                        )
                        .body(Body::from(r#"{"client":"browser-1"}"#))
                        .expect("request"),
                )
                .await
                .expect("response");
            assert_eq!(response.status(), expected);
            let body = to_bytes(response.into_body(), 4096).await.expect("body");
            assert!(!String::from_utf8_lossy(&body).contains("private"));
        }
    }

    #[tokio::test]
    async fn realtime_connect_rejects_invalid_client_before_auth() {
        let (app, _, verifier) = app(VerifierOutcome::Valid(Uuid::new_v4()), false);
        let response = app
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/v1/realtime/connect")
                    .header("content-type", "application/json")
                    .body(Body::from(r#"{"client":""}"#))
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::UNPROCESSABLE_ENTITY);
        assert_eq!(verifier.calls.load(Ordering::Relaxed), 0);
    }
}
