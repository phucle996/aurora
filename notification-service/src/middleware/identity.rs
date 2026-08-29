use axum::{
    extract::Request,
    http::StatusCode,
    middleware::Next,
    response::{IntoResponse, Response},
};
use uuid::Uuid;

// [COMMENT]: Struct chứa User ID đã được xác thực, lưu trong Request Extensions
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct AuthUser(pub Uuid);

// [COMMENT]: Middleware xác thực định danh người dùng từ header "x-user-id" do Edge Proxy (ACR/Envoy) inject
pub async fn identity_middleware(mut request: Request, next: Next) -> Response {
    let raw = match request
        .headers()
        .get("x-user-id")
        .and_then(|v| v.to_str().ok())
        .map(str::trim)
        .filter(|v| !v.is_empty())
    {
        Some(v) => v,
        None => {
            return (StatusCode::UNAUTHORIZED, "missing required identity header").into_response()
        }
    };

    let user_id = match Uuid::parse_str(raw) {
        Ok(id) => id,
        Err(_) => {
            return (StatusCode::UNAUTHORIZED, "invalid identity header format").into_response()
        }
    };

    // [COMMENT]: Gắn thông tin AuthUser vào request extensions để các handler bên trong sử dụng
    request.extensions_mut().insert(AuthUser(user_id));

    next.run(request).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::to_bytes;
    use axum::http::Request;
    use axum::middleware::from_fn;
    use axum::response::IntoResponse;
    use axum::routing::get;
    use axum::Router;
    use tower::ServiceExt;

    #[tokio::test]
    async fn identity_middleware_injects_auth_user_extension() {
        let user_id = Uuid::new_v4();

        async fn dummy_handler(
            axum::extract::Extension(AuthUser(extracted_id)): axum::extract::Extension<AuthUser>,
        ) -> Response {
            (StatusCode::OK, extracted_id.to_string()).into_response()
        }

        let app = Router::new()
            .route("/test", get(dummy_handler))
            .layer(from_fn(identity_middleware));

        let request = Request::builder()
            .uri("/test")
            .header("x-user-id", user_id.to_string())
            .body(axum::body::Body::empty())
            .unwrap();

        let response = app.oneshot(request).await.unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let body = to_bytes(response.into_body(), 1024).await.unwrap();
        assert_eq!(body.as_ref(), user_id.to_string().as_bytes());
    }

    #[tokio::test]
    async fn identity_middleware_rejects_missing_header() {
        async fn dummy_handler() -> Response {
            StatusCode::OK.into_response()
        }

        let app = Router::new()
            .route("/test", get(dummy_handler))
            .layer(from_fn(identity_middleware));

        let request = Request::builder()
            .uri("/test")
            .body(axum::body::Body::empty())
            .unwrap();

        let response = app.oneshot(request).await.unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body = to_bytes(response.into_body(), 1024).await.unwrap();
        assert_eq!(body.as_ref(), b"missing required identity header");
    }

    #[tokio::test]
    async fn identity_middleware_rejects_empty_or_whitespace_header() {
        async fn dummy_handler() -> Response {
            StatusCode::OK.into_response()
        }

        let app = Router::new()
            .route("/test", get(dummy_handler))
            .layer(from_fn(identity_middleware));

        for empty_val in ["", "   ", "  \t  "] {
            let request = Request::builder()
                .uri("/test")
                .header("x-user-id", empty_val)
                .body(axum::body::Body::empty())
                .unwrap();

            let response = app.clone().oneshot(request).await.unwrap();
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
            let body = to_bytes(response.into_body(), 1024).await.unwrap();
            assert_eq!(body.as_ref(), b"missing required identity header");
        }
    }

    #[tokio::test]
    async fn identity_middleware_rejects_malformed_uuid() {
        async fn dummy_handler() -> Response {
            StatusCode::OK.into_response()
        }

        let app = Router::new()
            .route("/test", get(dummy_handler))
            .layer(from_fn(identity_middleware));

        for invalid_val in ["not-a-uuid", "12345", "xxxx-yyyy-zzzz"] {
            let request = Request::builder()
                .uri("/test")
                .header("x-user-id", invalid_val)
                .body(axum::body::Body::empty())
                .unwrap();

            let response = app.clone().oneshot(request).await.unwrap();
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
            let body = to_bytes(response.into_body(), 1024).await.unwrap();
            assert_eq!(body.as_ref(), b"invalid identity header format");
        }
    }
}
