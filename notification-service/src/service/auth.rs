use crate::service::ports::{AuthError, AuthVerifier};
use std::collections::HashMap;
use std::sync::Arc;

// [COMMENT]: Thông tin định danh xác thực bóc tách từ Cookie client
#[derive(Debug)]
pub enum AuthCredentials {
    Admin {
        access_token: String,
        access_key: String,
        access_secret: String,
    },
    User {
        access_token: String,
        access_key: String,
        access_secret: String,
    },
}

// [COMMENT]: Lỗi phân quyền / xác thực kết nối Realtime Centrifugo
#[derive(Debug)]
pub enum ConnectAuthError {
    MissingCredentials,
    InvalidCredentials,
    Unavailable,
    InvalidResponse,
}

impl ConnectAuthError {
    pub fn status_code(&self) -> axum::http::StatusCode {
        match self {
            Self::MissingCredentials | Self::InvalidCredentials => {
                axum::http::StatusCode::UNAUTHORIZED
            }
            Self::Unavailable => axum::http::StatusCode::SERVICE_UNAVAILABLE,
            Self::InvalidResponse => axum::http::StatusCode::INTERNAL_SERVER_ERROR,
        }
    }
}

// [COMMENT]: Service chịu trách nhiệm authorize kết nối WebSocket Centrifugo và xác minh với ACR
pub struct ConnectAuthorizer {
    verifier: Arc<dyn AuthVerifier>,
}

impl ConnectAuthorizer {
    pub fn new(verifier: Arc<dyn AuthVerifier>) -> Self {
        Self { verifier }
    }

    // [COMMENT]: Trích xuất cookie, chọn credentials và gọi ACR xác minh
    pub async fn authorize(&self, cookie_header: &str) -> Result<String, ConnectAuthError> {
        let cookies = parse_cookies(cookie_header);
        let credentials = if cookies.contains_key("admin_api_token") {
            AuthCredentials::Admin {
                access_token: required_cookie(&cookies, "admin_api_token")?,
                access_key: required_cookie(&cookies, "access_key")?,
                access_secret: required_cookie(&cookies, "access_secret")?,
            }
        } else if cookies.contains_key("access_token") {
            AuthCredentials::User {
                access_token: required_cookie(&cookies, "access_token")?,
                access_key: required_cookie(&cookies, "access_key")?,
                access_secret: required_cookie(&cookies, "access_secret")?,
            }
        } else {
            return Err(ConnectAuthError::MissingCredentials);
        };

        // [COMMENT]: Gửi RPC xác thực sang ACR thông qua verifier port
        let principal = match self.verifier.verify(credentials).await {
            Ok(principal) => principal,
            Err(AuthError::Invalid) => return Err(ConnectAuthError::InvalidCredentials),
            Err(AuthError::Unavailable(_)) => return Err(ConnectAuthError::Unavailable),
            Err(AuthError::Protocol(_)) => return Err(ConnectAuthError::InvalidResponse),
        };

        if uuid::Uuid::parse_str(&principal.id).is_err() {
            return Err(ConnectAuthError::InvalidResponse);
        }
        Ok(principal.id)
    }
}

fn parse_cookies(header: &str) -> HashMap<String, String> {
    header
        .split(';')
        .filter_map(|part| {
            let (name, value) = part.trim().split_once('=')?;
            let name = name.trim();
            let value = value.trim().trim_matches('"');
            (!name.is_empty() && !value.is_empty()).then(|| (name.to_owned(), value.to_owned()))
        })
        .collect()
}

fn required_cookie(
    cookies: &HashMap<String, String>,
    name: &str,
) -> Result<String, ConnectAuthError> {
    cookies
        .get(name)
        .filter(|value| !value.is_empty())
        .cloned()
        .ok_or(ConnectAuthError::MissingCredentials)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::service::ports::{AuthError, AuthenticatedPrincipal};
    use futures_util::future::BoxFuture;

    struct Verifier {
        result: Result<AuthenticatedPrincipal, AuthError>,
    }

    impl AuthVerifier for Verifier {
        fn verify<'a>(
            &'a self,
            _credentials: AuthCredentials,
        ) -> BoxFuture<'a, Result<AuthenticatedPrincipal, AuthError>> {
            Box::pin(std::future::ready(match &self.result {
                Ok(principal) => Ok(AuthenticatedPrincipal {
                    id: principal.id.clone(),
                }),
                Err(AuthError::Invalid) => Err(AuthError::Invalid),
                Err(AuthError::Unavailable(message)) => {
                    Err(AuthError::Unavailable(message.clone()))
                }
                Err(AuthError::Protocol(message)) => Err(AuthError::Protocol(message.clone())),
            }))
        }
    }

    #[tokio::test]
    async fn admin_cookie_takes_precedence_and_preserves_equals_in_secret() {
        let authorizer = ConnectAuthorizer::new(Arc::new(Verifier {
            result: Ok(AuthenticatedPrincipal {
                id: uuid::Uuid::new_v4().to_string(),
            }),
        }));
        let result = authorizer
            .authorize(
                "access_token=user; admin_api_token=admin; access_key=key; access_secret=a=b=c",
            )
            .await;
        assert!(result.is_ok());
    }

    #[tokio::test]
    async fn missing_cookie_is_rejected_without_verifier_call() {
        let authorizer = ConnectAuthorizer::new(Arc::new(Verifier {
            result: Err(AuthError::Invalid),
        }));
        assert!(matches!(
            authorizer.authorize("access_token=token").await,
            Err(ConnectAuthError::MissingCredentials)
        ));
    }

    #[tokio::test]
    async fn verifier_failures_map_to_stable_http_classes() {
        for (result, expected) in [
            (
                Err(AuthError::Invalid),
                ConnectAuthError::InvalidCredentials,
            ),
            (
                Err(AuthError::Unavailable("redis unavailable".to_string())),
                ConnectAuthError::Unavailable,
            ),
            (
                Err(AuthError::Protocol("malformed protobuf".to_string())),
                ConnectAuthError::InvalidResponse,
            ),
        ] {
            let authorizer = ConnectAuthorizer::new(Arc::new(Verifier { result }));
            let error = authorizer
                .authorize("access_token=token; access_key=key; access_secret=secret")
                .await
                .expect_err("authorization must fail");
            assert_eq!(error.status_code(), expected.status_code());
        }
    }

    #[tokio::test]
    async fn invalid_principal_from_auth_backend_fails_closed() {
        let authorizer = ConnectAuthorizer::new(Arc::new(Verifier {
            result: Ok(AuthenticatedPrincipal {
                id: "not-a-uuid".to_string(),
            }),
        }));
        assert!(matches!(
            authorizer
                .authorize("access_token=token; access_key=key; access_secret=secret")
                .await,
            Err(ConnectAuthError::InvalidResponse)
        ));
    }

    #[tokio::test]
    async fn incomplete_admin_cookie_does_not_fall_back_to_user_credentials() {
        let authorizer = ConnectAuthorizer::new(Arc::new(Verifier {
            result: Ok(AuthenticatedPrincipal {
                id: uuid::Uuid::new_v4().to_string(),
            }),
        }));
        assert!(authorizer
            .authorize(
                "admin_api_token=admin; access_token=user; access_key=key; access_secret=secret",
            )
            .await
            .is_ok());
        assert!(matches!(
            authorizer
                .authorize("admin_api_token=admin; access_token=user; access_secret=secret")
                .await,
            Err(ConnectAuthError::MissingCredentials)
        ));
    }
}
