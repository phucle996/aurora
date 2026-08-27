use super::*;
use crate::application::auth::AuthCredentials;
use crate::application::ports::{AuthError, AuthVerifier, AuthenticatedPrincipal};
use axum::body::to_bytes;
use futures_util::future::BoxFuture;
use std::sync::atomic::{AtomicUsize, Ordering};

struct Verifier {
    result: Result<AuthenticatedPrincipal, AuthError>,
    calls: AtomicUsize,
}

impl AuthVerifier for Verifier {
    fn verify<'a>(
        &'a self,
        _credentials: AuthCredentials,
    ) -> BoxFuture<'a, Result<AuthenticatedPrincipal, AuthError>> {
        self.calls.fetch_add(1, Ordering::Relaxed);
        Box::pin(std::future::ready(match &self.result {
            Ok(principal) => Ok(AuthenticatedPrincipal {
                id: principal.id.clone(),
            }),
            Err(AuthError::Invalid) => Err(AuthError::Invalid),
            Err(AuthError::Unavailable(reason)) => Err(AuthError::Unavailable(reason.clone())),
            Err(AuthError::Protocol(reason)) => Err(AuthError::Protocol(reason.clone())),
        }))
    }
}

#[tokio::test]
async fn authorization_failures_keep_status_and_generic_body() {
    let cookies = "access_token=token; access_key=key; access_secret=secret";
    for (cookie, result, expected_status, expected_calls) in [
        ("", Err(AuthError::Invalid), StatusCode::UNAUTHORIZED, 0),
        (
            "access_token=token",
            Err(AuthError::Invalid),
            StatusCode::UNAUTHORIZED,
            0,
        ),
        (
            cookies,
            Err(AuthError::Invalid),
            StatusCode::UNAUTHORIZED,
            1,
        ),
        (
            cookies,
            Err(AuthError::Unavailable("private dependency details".into())),
            StatusCode::SERVICE_UNAVAILABLE,
            1,
        ),
        (
            cookies,
            Err(AuthError::Protocol("private protocol details".into())),
            StatusCode::INTERNAL_SERVER_ERROR,
            1,
        ),
        (
            cookies,
            Ok(AuthenticatedPrincipal {
                id: "not-a-uuid".into(),
            }),
            StatusCode::INTERNAL_SERVER_ERROR,
            1,
        ),
    ] {
        let verifier = Arc::new(Verifier {
            result,
            calls: AtomicUsize::new(0),
        });
        let authorizer = ConnectAuthorizer::new(verifier.clone());
        let mut headers = HeaderMap::new();
        headers.insert("cookie", cookie.parse().expect("cookie header"));
        let error = authorize(&authorizer, &headers)
            .await
            .expect_err("request must be denied");
        let response = error.into_response();
        assert_eq!(response.status(), expected_status);
        assert_eq!(
            response.headers()["content-type"],
            "text/plain; charset=utf-8"
        );
        assert_eq!(
            to_bytes(response.into_body(), 1024)
                .await
                .expect("error body")
                .as_ref(),
            b"request rejected"
        );
        assert_eq!(verifier.calls.load(Ordering::Relaxed), expected_calls);
    }
}

#[tokio::test]
async fn authorization_returns_only_the_verified_principal() {
    let user_id = Uuid::new_v4();
    let verifier = Arc::new(Verifier {
        result: Ok(AuthenticatedPrincipal {
            id: user_id.to_string(),
        }),
        calls: AtomicUsize::new(0),
    });
    let authorizer = ConnectAuthorizer::new(verifier.clone());
    let mut headers = HeaderMap::new();
    headers.insert(
        "cookie",
        "access_token=token; access_key=key; access_secret=secret"
            .parse()
            .expect("cookie header"),
    );
    headers.insert(
        "x-user-id",
        Uuid::new_v4()
            .to_string()
            .parse()
            .expect("untrusted identity header"),
    );
    assert_eq!(
        authorize(&authorizer, &headers)
            .await
            .expect("verified subject"),
        user_id
    );
    assert_eq!(verifier.calls.load(Ordering::Relaxed), 1);
}

#[tokio::test]
async fn internal_principal_conversion_error_preserves_empty_500() {
    assert!(std::mem::size_of::<TimelineAuthorizationError>() <= 2);
    let response = TimelineAuthorizationError::InvalidPrincipal.into_response();
    assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
    assert!(!response.headers().contains_key("content-type"));
    assert!(to_bytes(response.into_body(), 1024)
        .await
        .expect("empty error body")
        .is_empty());
}
