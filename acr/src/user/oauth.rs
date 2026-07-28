use crate::config::{Config, OAuthProviderConfig};
use crate::infra::iam_proto::auth::{
    VerifyExternalIdentityRequest, VerifyExternalIdentityResponse,
};
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::infra::vault::VaultClient;
use crate::observability::logger::Logger;
use crate::pkg::cookie::{
    COOKIE_ACCESS_KEY, COOKIE_ACCESS_SECRET, COOKIE_ACCESS_TOKEN, COOKIE_CLIENT_DEVICE_ID,
    COOKIE_REFRESH_TOKEN, COOKIE_TENANT_ID, COOKIE_ZONE_CODE,
};
use crate::token::TokenManager;
use crate::user::login::{issue_mfa_challenge, release_user_session, MfaChallengeContext};
use crate::user::session_proof::canonicalize_public_key;
use base64::Engine;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::{CheckRequest, CheckResponse};
use futures_util::StreamExt;
use jsonwebtoken::{decode, decode_header, Algorithm, DecodingKey, Validation};
use prost::Message;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::{Mutex, Semaphore};
use tonic::{Response, Status};
use url::form_urlencoded;
use uuid::Uuid;

const GOOGLE_AUTH_URL: &str = "https://accounts.google.com/o/oauth2/v2/auth";
const GOOGLE_TOKEN_URL: &str = "https://oauth2.googleapis.com/token";
const GOOGLE_JWKS_URL: &str = "https://www.googleapis.com/oauth2/v3/certs";
const GITHUB_AUTH_URL: &str = "https://github.com/login/oauth/authorize";
const GITHUB_TOKEN_URL: &str = "https://github.com/login/oauth/access_token";
const GITHUB_API_URL: &str = "https://api.github.com";
const STATE_KEY_PREFIX: &str = "iam:oauth:state:";
const STATE_TTL_SECONDS: u64 = 300;
const GOOGLE_JWKS_CACHE_TTL: Duration = Duration::from_secs(600);
const OAUTH_CALLBACK_CONCURRENCY: usize = 64;

#[derive(Clone)]
pub struct OAuthProviderService {
    google: Option<ProviderRuntime>,
    github: Option<ProviderRuntime>,
    http: reqwest::Client,
    state_ttl_secs: u64,
    google_jwks: Arc<Mutex<Option<CachedGoogleJwks>>>,
    callback_slots: Arc<Semaphore>,
}

#[derive(Clone)]
struct ProviderRuntime {
    config: OAuthProviderConfig,
    client_secret: String,
}

struct CachedGoogleJwks {
    keys: Vec<GoogleJwk>,
    expires_at: Instant,
}

#[derive(Debug, Serialize, Deserialize)]
struct OAuthState {
    provider: String,
    operation_id: String,
    code_verifier: String,
    nonce: String,
    device_public_key: String,
    client_device_id: String,
    device_name: String,
    device_type: String,
    trust_device: bool,
    zone_id: String,
    zone_code: String,
    return_to: String,
}

#[derive(Debug)]
struct CanonicalIdentity {
    provider: &'static str,
    subject: String,
    email: String,
    display_name: String,
    avatar_url: Option<String>,
    email_verified_at: i64,
}

#[derive(Debug, Deserialize)]
struct GoogleTokenResponse {
    id_token: Option<String>,
}

#[derive(Debug, Deserialize)]
struct GoogleClaims {
    sub: String,
    email: String,
    email_verified: bool,
    azp: Option<String>,
    name: Option<String>,
    picture: Option<String>,
    nonce: Option<String>,
}

#[derive(Debug, Deserialize)]
struct GoogleJwks {
    keys: Vec<GoogleJwk>,
}

#[derive(Clone, Debug, Deserialize)]
struct GoogleJwk {
    kid: String,
    kty: String,
    alg: Option<String>,
    n: String,
    e: String,
}

#[derive(Debug, Deserialize)]
struct GitHubTokenResponse {
    access_token: Option<String>,
}

#[derive(Debug, Deserialize)]
struct GitHubUser {
    id: u64,
    login: Option<String>,
    name: Option<String>,
    avatar_url: Option<String>,
}

#[derive(Debug, Deserialize)]
struct GitHubEmail {
    email: String,
    primary: bool,
    verified: bool,
}

impl OAuthProviderService {
    pub async fn new(config: &Config, vault: Arc<VaultClient>) -> Result<Self, String> {
        let http = reqwest::Client::builder()
            .timeout(Duration::from_secs(8))
            .redirect(reqwest::redirect::Policy::none())
            .user_agent("aurora-acr-oauth/1")
            .build()
            .map_err(|error| format!("build OAuth HTTP client: {error}"))?;

        let google = load_runtime(&config.oauth.google, &vault, "google").await?;
        let github = load_runtime(&config.oauth.github, &vault, "github").await?;
        Ok(Self {
            google,
            github,
            http,
            state_ttl_secs: config.oauth.state_ttl_secs.clamp(60, STATE_TTL_SECONDS),
            google_jwks: Arc::new(Mutex::new(None)),
            callback_slots: Arc::new(Semaphore::new(OAUTH_CALLBACK_CONCURRENCY)),
        })
    }

    fn runtime(&self, provider: &str) -> Option<&ProviderRuntime> {
        match provider {
            "google" => self.google.as_ref(),
            "github" => self.github.as_ref(),
            _ => None,
        }
    }

    fn authorization_url(
        &self,
        provider: &str,
        runtime: &ProviderRuntime,
        state: &OAuthState,
        state_token: &str,
    ) -> String {
        let challenge = pkce_challenge(&state.code_verifier);
        let mut query = form_urlencoded::Serializer::new(String::new());
        query.append_pair("client_id", &runtime.config.client_id);
        query.append_pair("redirect_uri", &runtime.config.redirect_uri);
        query.append_pair("response_type", "code");
        query.append_pair("state", state_token);
        query.append_pair("code_challenge", &challenge);
        query.append_pair("code_challenge_method", "S256");
        if provider == "google" {
            query.append_pair("scope", "openid email profile");
            query.append_pair("nonce", &state.nonce);
            query.append_pair("access_type", "online");
            format!("{GOOGLE_AUTH_URL}?{}", query.finish())
        } else {
            query.append_pair("scope", "read:user user:email");
            format!("{GITHUB_AUTH_URL}?{}", query.finish())
        }
    }

    async fn exchange_and_verify(
        &self,
        provider: &str,
        runtime: &ProviderRuntime,
        code: &str,
        state: &OAuthState,
    ) -> Result<CanonicalIdentity, String> {
        if provider == "google" {
            let token = self
                .http
                .post(GOOGLE_TOKEN_URL)
                .form(&[
                    ("code", code),
                    ("client_id", runtime.config.client_id.as_str()),
                    ("client_secret", runtime.client_secret.as_str()),
                    ("redirect_uri", runtime.config.redirect_uri.as_str()),
                    ("grant_type", "authorization_code"),
                    ("code_verifier", state.code_verifier.as_str()),
                ])
                .send()
                .await
                .map_err(|_| "PROVIDER_UNAVAILABLE".to_string())?;
            let body = bounded_json(token, "Google token response").await?;
            let token: GoogleTokenResponse = serde_json::from_slice(&body)
                .map_err(|_| "PROVIDER_IDENTITY_INVALID".to_string())?;
            let id_token = token
                .id_token
                .ok_or_else(|| "PROVIDER_IDENTITY_INVALID".to_string())?;
            self.verify_google_id_token(&id_token, state, runtime).await
        } else {
            let token = self
                .http
                .post(GITHUB_TOKEN_URL)
                .header("Accept", "application/json")
                .form(&[
                    ("client_id", runtime.config.client_id.as_str()),
                    ("client_secret", runtime.client_secret.as_str()),
                    ("code", code),
                    ("redirect_uri", runtime.config.redirect_uri.as_str()),
                    ("code_verifier", state.code_verifier.as_str()),
                ])
                .send()
                .await
                .map_err(|_| "PROVIDER_UNAVAILABLE".to_string())?;
            let body = bounded_json(token, "GitHub token response").await?;
            let token: GitHubTokenResponse = serde_json::from_slice(&body)
                .map_err(|_| "PROVIDER_IDENTITY_INVALID".to_string())?;
            let access_token = token
                .access_token
                .filter(|value| !value.is_empty())
                .ok_or_else(|| "PROVIDER_IDENTITY_INVALID".to_string())?;

            let user_response = self
                .http
                .get(format!("{GITHUB_API_URL}/user"))
                .bearer_auth(&access_token)
                .header("Accept", "application/vnd.github+json")
                .header("X-GitHub-Api-Version", "2022-11-28")
                .send()
                .await
                .map_err(|_| "PROVIDER_UNAVAILABLE".to_string())?;
            let user_body = bounded_json(user_response, "GitHub user response").await?;
            let user: GitHubUser = serde_json::from_slice(&user_body)
                .map_err(|_| "PROVIDER_IDENTITY_INVALID".to_string())?;

            let email_response = self
                .http
                .get(format!("{GITHUB_API_URL}/user/emails"))
                .bearer_auth(&access_token)
                .header("Accept", "application/vnd.github+json")
                .header("X-GitHub-Api-Version", "2022-11-28")
                .query(&[("per_page", "100")])
                .send()
                .await
                .map_err(|_| "PROVIDER_UNAVAILABLE".to_string())?;
            let email_body = bounded_json(email_response, "GitHub email response").await?;
            let emails: Vec<GitHubEmail> = serde_json::from_slice(&email_body)
                .map_err(|_| "PROVIDER_IDENTITY_INVALID".to_string())?;
            let email = emails
                .into_iter()
                .find(|entry| entry.primary && entry.verified)
                .ok_or_else(|| "PROVIDER_EMAIL_UNVERIFIED".to_string())?;
            let display_name = user.name.or(user.login).unwrap_or_else(|| {
                email
                    .email
                    .split('@')
                    .next()
                    .unwrap_or("Aurora user")
                    .to_string()
            });
            Ok(canonical_identity(
                "github",
                user.id.to_string(),
                email.email,
                display_name,
                user.avatar_url,
            )?)
        }
    }

    async fn verify_google_id_token(
        &self,
        id_token: &str,
        state: &OAuthState,
        runtime: &ProviderRuntime,
    ) -> Result<CanonicalIdentity, String> {
        let header =
            decode_header(id_token).map_err(|_| "PROVIDER_IDENTITY_INVALID".to_string())?;
        if header.alg != Algorithm::RS256 {
            return Err("PROVIDER_IDENTITY_INVALID".to_string());
        }
        let kid = header
            .kid
            .as_deref()
            .ok_or_else(|| "PROVIDER_IDENTITY_INVALID".to_string())?;
        let key = self.google_jwk(kid).await?;
        let decoding_key = DecodingKey::from_rsa_components(&key.n, &key.e)
            .map_err(|_| "PROVIDER_IDENTITY_INVALID".to_string())?;
        let mut validation = Validation::new(Algorithm::RS256);
        validation.set_audience(&[&runtime.config.client_id]);
        validation.set_issuer(&["https://accounts.google.com", "accounts.google.com"]);
        validation.leeway = 60;
        let token = decode::<GoogleClaims>(id_token, &decoding_key, &validation)
            .map_err(|_| "PROVIDER_IDENTITY_INVALID".to_string())?;
        if token
            .claims
            .azp
            .as_deref()
            .is_some_and(|azp| azp != runtime.config.client_id.as_str())
        {
            return Err("PROVIDER_IDENTITY_INVALID".to_string());
        }
        if token.claims.nonce.as_deref() != Some(state.nonce.as_str()) {
            return Err("PROVIDER_IDENTITY_INVALID".to_string());
        }
        if !token.claims.email_verified {
            return Err("PROVIDER_EMAIL_UNVERIFIED".to_string());
        }
        let display_name = token.claims.name.unwrap_or_else(|| {
            token
                .claims
                .email
                .split('@')
                .next()
                .unwrap_or("Aurora user")
                .to_string()
        });
        canonical_identity(
            "google",
            token.claims.sub,
            token.claims.email,
            display_name,
            token.claims.picture,
        )
    }

    async fn google_jwk(&self, kid: &str) -> Result<GoogleJwk, String> {
        // One fetch per pod at a time avoids a login burst stampeding Google's
        // JWKS endpoint. A missing kid forces refresh so key rotation converges.
        let mut cached = self.google_jwks.lock().await;
        if let Some(entry) = cached.as_ref() {
            if entry.expires_at > Instant::now() {
                if let Some(key) = entry.keys.iter().find(|key| valid_google_jwk(key, kid)) {
                    return Ok(key.clone());
                }
            }
        }

        let jwks_response = self
            .http
            .get(GOOGLE_JWKS_URL)
            .send()
            .await
            .map_err(|_| "PROVIDER_UNAVAILABLE".to_string())?;
        let jwks_body = bounded_json(jwks_response, "Google JWKS response").await?;
        let jwks: GoogleJwks = serde_json::from_slice(&jwks_body)
            .map_err(|_| "PROVIDER_IDENTITY_INVALID".to_string())?;
        let key = jwks
            .keys
            .iter()
            .find(|key| valid_google_jwk(key, kid))
            .cloned()
            .ok_or_else(|| "PROVIDER_IDENTITY_INVALID".to_string())?;
        *cached = Some(CachedGoogleJwks {
            keys: jwks.keys,
            expires_at: Instant::now() + GOOGLE_JWKS_CACHE_TTL,
        });
        Ok(key)
    }

    pub async fn handle(
        &self,
        session_mgr: &Arc<SessionManager>,
        token_mgr: &Arc<TokenManager>,
        redis_client: &redis::Client,
        shared_redis: &Arc<SharedRedisBus>,
        config: &Config,
        client_headers: &HashMap<String, String>,
        req: &CheckRequest,
        method: &str,
        path: &str,
    ) -> Option<Result<Response<CheckResponse>, Status>> {
        let path_without_query = path.split('?').next().unwrap_or(path);
        let rest = path_without_query.strip_prefix("/api/v1/auth/oauth/")?;
        let (provider, action) = rest.split_once('/')?;
        if !matches!(provider, "google" | "github") || !matches!(action, "start" | "callback") {
            return None;
        }
        let runtime = match self.runtime(provider) {
            Some(runtime) => runtime,
            None => {
                if action == "callback" {
                    Logger::sys_warn(
                        "user.oauth",
                        "OAuth callback reached a disabled provider",
                        "provider_disabled",
                    );
                    return Some(Ok(Response::new(oauth_failure_redirect("/"))));
                }
                return Some(Ok(Response::new(error_json(
                    HttpStatusCode::NotFound,
                    "OAUTH_PROVIDER_DISABLED",
                ))));
            }
        };
        if action == "start" {
            if method != "POST" {
                return Some(Ok(Response::new(error_json(
                    HttpStatusCode::MethodNotAllowed,
                    "Method not allowed",
                ))));
            }
            return Some(
                self.handle_start(
                    session_mgr,
                    shared_redis,
                    redis_client,
                    runtime,
                    provider,
                    req,
                    client_headers,
                )
                .await,
            );
        }
        if method != "GET" {
            return Some(Ok(Response::new(error_json(
                HttpStatusCode::MethodNotAllowed,
                "Method not allowed",
            ))));
        }
        let _callback_permit = match self.callback_slots.clone().try_acquire_owned() {
            Ok(permit) => permit,
            Err(_) => {
                Logger::sys_warn(
                    "user.oauth",
                    "OAuth callback capacity exhausted",
                    "callback_backpressure",
                );
                return Some(Ok(Response::new(oauth_failure_redirect("/"))));
            }
        };
        let callback = self.handle_callback(
            session_mgr,
            token_mgr,
            shared_redis,
            config,
            client_headers,
            runtime,
            provider,
            path,
        );
        Some(
            match tokio::time::timeout(Duration::from_secs(13), callback).await {
                Ok(result) => result,
                Err(_) => {
                    Logger::sys_warn(
                        "user.oauth",
                        "OAuth callback exceeded its end-to-end budget",
                        "callback_timeout",
                    );
                    Ok(Response::new(oauth_failure_redirect("/")))
                }
            },
        )
    }

    async fn handle_start(
        &self,
        session_mgr: &Arc<SessionManager>,
        shared_redis: &Arc<SharedRedisBus>,
        redis_client: &redis::Client,
        runtime: &ProviderRuntime,
        provider: &str,
        req: &CheckRequest,
        client_headers: &HashMap<String, String>,
    ) -> Result<Response<CheckResponse>, Status> {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct StartPayload {
            device_public_key: Option<String>,
            device_name: Option<String>,
            device_type: Option<String>,
            trust_device: Option<bool>,
            zone_code: Option<String>,
            return_to: Option<String>,
        }
        let body = request_body(req);
        let payload: StartPayload = match serde_json::from_slice(&body) {
            Ok(payload) => payload,
            Err(_) => {
                return Ok(Response::new(error_json(
                    HttpStatusCode::BadRequest,
                    "Invalid OAuth start payload",
                )))
            }
        };
        let zone_code = payload
            .zone_code
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase();
        if zone_code.is_empty() || zone_code == "global" || zone_code.len() > 64 {
            return Ok(Response::new(error_json(
                HttpStatusCode::BadRequest,
                "zone_code is required",
            )));
        }
        let Some((zone_id, status)) = crate::infra::zone::resolve_code_to_id_and_status(
            shared_redis,
            redis_client,
            &zone_code,
        )
        .await
        else {
            return Ok(Response::new(error_json(
                HttpStatusCode::Forbidden,
                "Zone unavailable",
            )));
        };
        if !matches!(Uuid::parse_str(&zone_id), Ok(value) if !value.is_nil())
            || (status != "active" && status != "draining")
        {
            return Ok(Response::new(error_json(
                HttpStatusCode::Forbidden,
                "Zone unavailable",
            )));
        }
        let public_key =
            match canonicalize_public_key(payload.device_public_key.as_deref().unwrap_or_default())
            {
                Ok(key) => key,
                Err(_) => {
                    return Ok(Response::new(error_json(
                        HttpStatusCode::BadRequest,
                        "Invalid device public key",
                    )))
                }
            };
        let return_to = payload.return_to.unwrap_or_else(|| "/".to_string());
        if return_to.len() > 2048 || !safe_return_to(&return_to) {
            return Ok(Response::new(error_json(
                HttpStatusCode::BadRequest,
                "Invalid return path",
            )));
        }
        let device_name = payload.device_name.unwrap_or_default().trim().to_string();
        let device_type = payload.device_type.unwrap_or_default().trim().to_string();
        if device_name.len() > 120
            || device_type.len() > 64
            || device_name.chars().any(char::is_control)
            || device_type.chars().any(char::is_control)
        {
            return Ok(Response::new(error_json(
                HttpStatusCode::BadRequest,
                "Invalid device metadata",
            )));
        }
        let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
        let client_device_id = crate::gateway::ext_authz::extract_cookie_value(
            &cookie_header,
            COOKIE_CLIENT_DEVICE_ID,
        )
        .and_then(|value| Uuid::parse_str(&value).ok())
        .filter(|value| !value.is_nil())
        .map(|value| value.to_string())
        .unwrap_or_else(|| Uuid::new_v4().to_string());
        let state = OAuthState {
            provider: provider.to_string(),
            operation_id: Uuid::now_v7().to_string(),
            code_verifier: random_token(),
            nonce: random_token(),
            device_public_key: public_key,
            client_device_id,
            device_name,
            device_type,
            trust_device: payload.trust_device.unwrap_or(false),
            zone_id,
            zone_code,
            return_to,
        };
        let state_token = random_token();
        let key = state_key(provider, &state_token);
        let state_json = serde_json::to_string(&state)
            .map_err(|_| Status::internal("OAuth state serialization failed"))?;
        let mut connection = session_mgr
            .get_connection()
            .await
            .map_err(|_| Status::unavailable("OAuth state service unavailable"))?;
        let stored: Option<String> = redis::cmd("SET")
            .arg(key)
            .arg(state_json)
            .arg("EX")
            .arg(self.state_ttl_secs)
            .arg("NX")
            .query_async(&mut connection)
            .await
            .map_err(|_| Status::unavailable("OAuth state service unavailable"))?;
        // NX is the one-time state write boundary. A missing reply means Redis did not
        // persist the state, so never send a provider redirect that cannot complete.
        if stored.is_none() {
            return Err(Status::unavailable("OAuth state service unavailable"));
        }
        let url = self.authorization_url(provider, runtime, &state, &state_token);
        let body = serde_json::json!({
            "authorization_url": url,
            "expires_in": self.state_ttl_secs,
        });
        Ok(Response::new(local_json(HttpStatusCode::Ok, body)))
    }

    async fn handle_callback(
        &self,
        session_mgr: &Arc<SessionManager>,
        token_mgr: &Arc<TokenManager>,
        shared_redis: &Arc<SharedRedisBus>,
        config: &Config,
        client_headers: &HashMap<String, String>,
        runtime: &ProviderRuntime,
        provider: &str,
        path: &str,
    ) -> Result<Response<CheckResponse>, Status> {
        let query = path
            .split_once('?')
            .map(|(_, query)| query)
            .unwrap_or_default();
        if query.len() > 8192 {
            Logger::sys_warn(
                "user.oauth",
                "OAuth callback query rejected",
                "query_too_large",
            );
            return Ok(Response::new(oauth_failure_redirect("/")));
        }
        let mut params = HashMap::new();
        for (key, value) in form_urlencoded::parse(query.as_bytes()).into_owned() {
            if matches!(key.as_str(), "state" | "code" | "error") && params.contains_key(&key) {
                Logger::sys_warn(
                    "user.oauth",
                    "OAuth callback query rejected",
                    "duplicate_security_parameter",
                );
                return Ok(Response::new(oauth_failure_redirect("/")));
            }
            params.insert(key, value);
        }
        let state_token = params.get("state").cloned().unwrap_or_default();
        if state_token.is_empty() || state_token.len() > 512 {
            Logger::sys_warn(
                "user.oauth",
                "OAuth callback state rejected",
                "invalid_state",
            );
            return Ok(Response::new(oauth_failure_redirect("/")));
        }
        let state_json = match consume_state(session_mgr, provider, &state_token).await {
            Ok(Some(state_json)) => state_json,
            // A missing value is a replay or expiry, not an infrastructure outage. Redirect
            // without revealing whether a valid state existed.
            Ok(None) => {
                Logger::sys_warn(
                    "user.oauth",
                    "OAuth callback state rejected",
                    "state_missing",
                );
                return Ok(Response::new(oauth_failure_redirect("/")));
            }
            Err(_) => {
                Logger::sys_error(
                    "user.oauth",
                    "OAuth callback state service unavailable",
                    "state_store_error",
                );
                return Ok(Response::new(oauth_failure_redirect("/")));
            }
        };
        let state: OAuthState = match serde_json::from_str(&state_json) {
            Ok(state) => state,
            Err(_) => {
                Logger::sys_warn(
                    "user.oauth",
                    "OAuth callback state rejected",
                    "state_decode",
                );
                return Ok(Response::new(oauth_failure_redirect("/")));
            }
        };
        if state.provider != provider {
            Logger::sys_warn(
                "user.oauth",
                "OAuth callback state rejected",
                "provider_mismatch",
            );
            return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
        }
        if params.get("error").is_some() {
            Logger::sys_warn(
                "user.oauth",
                "OAuth provider denied callback",
                "provider_denied",
            );
            return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
        }
        let code = params.get("code").cloned().unwrap_or_default();
        if code.is_empty() || code.len() > 4096 {
            Logger::sys_warn("user.oauth", "OAuth callback code rejected", "invalid_code");
            return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
        }
        let identity = match self
            .exchange_and_verify(provider, runtime, &code, &state)
            .await
        {
            Ok(identity) => identity,
            Err(error) => {
                Logger::sys_warn("user.oauth", "Provider identity rejected", &error);
                return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
            }
        };
        let req = VerifyExternalIdentityRequest {
            operation_id: state.operation_id.clone(),
            schema_version: 1,
            provider: identity.provider.to_string(),
            provider_subject: identity.subject,
            provider_email: identity.email,
            email_verified_at: identity.email_verified_at,
            display_name: identity.display_name,
            avatar_url: identity.avatar_url.unwrap_or_default(),
            client_device_id: state.client_device_id.clone(),
            device_name: state.device_name.clone(),
            device_type: state.device_type.clone(),
            public_key: state.device_public_key.clone(),
            trust_device: state.trust_device,
            zone_code: state.zone_code.clone(),
            client_ip: client_headers
                .get("x-forwarded-for")
                .cloned()
                .unwrap_or_default(),
            user_agent: client_headers
                .get("user-agent")
                .cloned()
                .unwrap_or_default(),
        };
        let mut payload = Vec::new();
        if req.encode(&mut payload).is_err() {
            Logger::sys_error(
                "user.oauth",
                "OAuth IAM request serialization failed",
                "request_encode",
            );
            return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
        }
        let response_payload = match shared_redis
            .request(
                "iam.auth.verify_external_identity",
                "iam.auth.verify_external_identity.reply.",
                payload,
                Duration::from_secs(10),
            )
            .await
        {
            Ok(payload) => payload,
            Err(_) => {
                Logger::sys_error(
                    "user.oauth",
                    "OAuth IAM request failed",
                    "authentication_service_unavailable",
                );
                return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
            }
        };
        let response = match VerifyExternalIdentityResponse::decode(response_payload.as_slice()) {
            Ok(response) => response,
            Err(_) => {
                Logger::sys_error(
                    "user.oauth",
                    "OAuth IAM response decode failed",
                    "response_decode",
                );
                return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
            }
        };
        if !response.valid {
            let internal_reason = if response.error_message.is_empty() {
                "AUTHENTICATION_UNAVAILABLE"
            } else {
                response.error_message.as_str()
            };
            Logger::sys_warn(
                "user.oauth",
                "OAuth identity login rejected by IAM",
                internal_reason,
            );
            return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
        }
        if response.zone_code != state.zone_code {
            Logger::sys_error(
                "user.oauth",
                "OAuth IAM response violated zone binding",
                "zone_mismatch",
            );
            return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
        }
        if response.mfa_required {
            if Uuid::parse_str(&response.mfa_setting_id).is_err() {
                Logger::sys_error(
                    "user.oauth",
                    "OAuth MFA response violated enrollment binding",
                    "mfa_setting_id_invalid",
                );
                return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
            }
            let context = MfaChallengeContext {
                user_id: response.user_id.clone(),
                username: response.username.clone(),
                tenant_domain: String::new(),
                mfa_setting_id: response.mfa_setting_id.clone(),
                role_id: response.role_id.clone(),
                level: response.level,
                tenant_id: response.tenant_id.clone(),
                zone_id: state.zone_id.clone(),
                zone_code: state.zone_code.clone(),
                client_device_id: state.client_device_id.clone(),
                public_key: state.device_public_key.clone(),
                client_proof_public_key: state.device_public_key.clone(),
                trust_device: state.trust_device,
                device_name: state.device_name.clone(),
                device_type: state.device_type.clone(),
                client_ip: client_headers
                    .get("x-forwarded-for")
                    .cloned()
                    .unwrap_or_default(),
                user_agent: client_headers
                    .get("user-agent")
                    .cloned()
                    .unwrap_or_default(),
            };
            let (challenge_id, expires_in) = match issue_mfa_challenge(session_mgr, context).await {
                Ok(value) => value,
                Err(_) => {
                    return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
                }
            };
            return Ok(Response::new(oauth_mfa_redirect(
                &state.return_to,
                &challenge_id,
                expires_in,
            )));
        }
        let refresh_max_age = if state.trust_device {
            let max_age = response.refresh_token_expires_at - chrono::Utc::now().timestamp();
            if response.refresh_token.is_empty() || max_age <= 0 {
                Logger::sys_error(
                    "user.oauth",
                    "OAuth IAM response contained invalid refresh state",
                    "refresh_state_invalid",
                );
                return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
            }
            Some(max_age)
        } else {
            None
        };
        let session = release_user_session(
            session_mgr,
            token_mgr,
            config,
            &response.user_id,
            &response.username,
            &response.role_id,
            response.level,
            &response.tenant_id,
            &state.zone_id,
            &response.client_device_id,
            &response.client_device_id,
            &response.client_proof_public_key,
        )
        .await;
        let session = match session {
            Ok(session) => session,
            Err(_) => {
                Logger::sys_error(
                    "user.oauth",
                    "OAuth session issuance failed",
                    "session_issue",
                );
                return Ok(Response::new(oauth_failure_redirect(&state.return_to)));
            }
        };
        Ok(oauth_session_response(
            config,
            session,
            &response.refresh_token,
            refresh_max_age,
            &state.zone_code,
            &state.return_to,
        ))
    }
}

async fn load_runtime(
    config: &OAuthProviderConfig,
    vault: &Arc<VaultClient>,
    provider: &str,
) -> Result<Option<ProviderRuntime>, String> {
    if !config.enabled {
        return Ok(None);
    }
    if config.client_id.trim().is_empty()
        || config.client_id.len() > 512
        || config.client_secret_path.trim().is_empty()
        || config.redirect_uri.trim().is_empty()
    {
        return Err(format!(
            "OAuth {provider} is enabled but client configuration is incomplete"
        ));
    }
    if !config
        .client_secret_path
        .starts_with("secret/data/acr/oauth/")
    {
        return Err(format!(
            "OAuth {provider} client secret path is outside the ACR OAuth Vault namespace"
        ));
    }
    let redirect = url::Url::parse(&config.redirect_uri)
        .map_err(|_| format!("OAuth {provider} redirect URI is invalid"))?;
    let is_local_http = redirect.scheme() == "http"
        && matches!(redirect.host_str(), Some("localhost") | Some("127.0.0.1"));
    if (redirect.scheme() != "https" && !is_local_http)
        || redirect.username() != ""
        || redirect.password().is_some()
        || redirect.query().is_some()
        || redirect.fragment().is_some()
        || redirect.path() != format!("/api/v1/auth/oauth/{provider}/callback")
    {
        return Err(format!(
            "OAuth {provider} redirect URI must be the provider callback over HTTPS"
        ));
    }
    let secret = vault
        .read_secret(&config.client_secret_path)
        .await
        .map_err(|_| format!("OAuth {provider} client secret unavailable from Vault"))?;
    let client_secret = secret
        .get("data")
        .and_then(|value| value.get("data"))
        .and_then(|value| value.get("client_secret"))
        .or_else(|| {
            secret
                .get("data")
                .and_then(|value| value.get("client_secret"))
        })
        .and_then(|value| value.as_str())
        .filter(|value| !value.trim().is_empty() && value.len() <= 4096)
        .ok_or_else(|| format!("OAuth {provider} Vault secret has no client_secret"))?
        .to_string();
    Ok(Some(ProviderRuntime {
        config: config.clone(),
        client_secret,
    }))
}

async fn bounded_json(response: reqwest::Response, label: &str) -> Result<Vec<u8>, String> {
    const MAX_PROVIDER_RESPONSE_BYTES: usize = 256 * 1024;
    if !response.status().is_success() {
        return Err("PROVIDER_UNAVAILABLE".to_string());
    }
    if response
        .content_length()
        .is_some_and(|length| length > MAX_PROVIDER_RESPONSE_BYTES as u64)
    {
        Logger::sys_warn("user.oauth", label, "provider response too large");
        return Err("PROVIDER_IDENTITY_INVALID".to_string());
    }
    let mut body = Vec::new();
    let mut stream = response.bytes_stream();
    while let Some(chunk) = stream.next().await {
        let chunk = chunk.map_err(|_| "PROVIDER_UNAVAILABLE".to_string())?;
        if body.len().saturating_add(chunk.len()) > MAX_PROVIDER_RESPONSE_BYTES {
            Logger::sys_warn("user.oauth", label, "provider response too large");
            return Err("PROVIDER_IDENTITY_INVALID".to_string());
        }
        body.extend_from_slice(&chunk);
    }
    Ok(body)
}

fn valid_google_jwk(key: &GoogleJwk, kid: &str) -> bool {
    key.kid == kid && key.kty == "RSA" && key.alg.as_deref().unwrap_or("RS256") == "RS256"
}

fn canonical_identity(
    provider: &'static str,
    subject: String,
    email: String,
    display_name: String,
    avatar_url: Option<String>,
) -> Result<CanonicalIdentity, String> {
    let subject = subject.trim().to_string();
    let email = email.trim().to_lowercase();
    let display_name = display_name.trim().to_string();
    if subject.is_empty()
        || subject.len() > 255
        || email.len() > 320
        || !email.contains('@')
        || display_name.is_empty()
        || display_name.len() > 120
        || subject.chars().any(char::is_control)
        || email.chars().any(char::is_control)
        || display_name.chars().any(char::is_control)
    {
        return Err("PROVIDER_IDENTITY_INVALID".to_string());
    }
    let avatar_url =
        avatar_url.filter(|value| value.starts_with("https://") && value.len() <= 2048);
    Ok(CanonicalIdentity {
        provider,
        subject,
        email,
        display_name,
        avatar_url,
        email_verified_at: chrono::Utc::now().timestamp(),
    })
}

fn request_body(req: &CheckRequest) -> Vec<u8> {
    req.attributes
        .as_ref()
        .and_then(|attributes| attributes.request.as_ref())
        .and_then(|request| request.http.as_ref())
        .map(|http| {
            if http.body.is_empty() {
                http.raw_body.clone()
            } else {
                http.body.as_bytes().to_vec()
            }
        })
        .unwrap_or_default()
}

async fn consume_state(
    session_mgr: &SessionManager,
    provider: &str,
    state_token: &str,
) -> Result<Option<String>, String> {
    let mut connection = session_mgr
        .get_connection()
        .await
        .map_err(|error| error.to_string())?;
    let script = r#"
        local value = redis.call('GET', KEYS[1])
        if not value then return nil end
        redis.call('DEL', KEYS[1])
        return value
    "#;
    redis::Script::new(script)
        .key(state_key(provider, state_token))
        .invoke_async::<_, Option<String>>(&mut connection)
        .await
        .map_err(|error| error.to_string())
}

fn state_key(provider: &str, state: &str) -> String {
    let digest = Sha256::digest(format!("{provider}:{state}").as_bytes());
    format!("{STATE_KEY_PREFIX}{provider}:{:x}", digest)
}

fn random_token() -> String {
    let mut hasher = Sha256::new();
    hasher.update(Uuid::new_v4().as_bytes());
    hasher.update(Uuid::new_v4().as_bytes());
    base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(hasher.finalize())
}

fn pkce_challenge(verifier: &str) -> String {
    base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()))
}

fn safe_return_to(value: &str) -> bool {
    value == "/" || (value.starts_with("/billing/authorize?") && !value.starts_with("//"))
}

fn local_json(status: HttpStatusCode, body: serde_json::Value) -> CheckResponse {
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(status);
    builder.add_header("content-type", "application/json", None, false);
    builder.set_body(body.to_string());
    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("Local OAuth response"));
    response.set_http_response(builder);
    response
}

fn error_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    local_json(
        status,
        serde_json::json!({
            "error_message": message,
            "error_code": message,
        }),
    )
}

fn oauth_failure_redirect(return_to: &str) -> CheckResponse {
    let mut query = form_urlencoded::Serializer::new(String::new());
    query.append_pair("oauth_error", "OAUTH_SIGN_IN_FAILED");
    if safe_return_to(return_to) && return_to != "/" {
        query.append_pair("return_to", return_to);
    }
    let destination = format!("/signin?{}", query.finish());
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::SeeOther);
    builder.add_header("location", &destination, None, false);
    builder.set_body("");
    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("OAuth callback completed"));
    response.set_http_response(builder);
    response
}

fn oauth_mfa_redirect(return_to: &str, challenge_id: &str, expires_in: u64) -> CheckResponse {
    let mut query = form_urlencoded::Serializer::new(String::new());
    query.append_pair("mfa_required", "1");
    query.append_pair("challenge_id", challenge_id);
    query.append_pair("expires_in", &expires_in.to_string());
    if safe_return_to(return_to) && return_to != "/" {
        query.append_pair("return_to", return_to);
    }
    let destination = format!("/signin?{}", query.finish());
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::Found);
    builder.add_header("location", &destination, None, false);
    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("MFA_REQUIRED"));
    response.set_http_response(builder);
    response
}

fn oauth_session_response(
    config: &Config,
    session: crate::user::login::ReleaseUserSessionResult,
    refresh_token: &str,
    refresh_max_age: Option<i64>,
    zone_code: &str,
    return_to: &str,
) -> Response<CheckResponse> {
    let domain = if config.app_public_domain.trim().is_empty() {
        String::new()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::SeeOther);
    builder.add_header("location", return_to, None, false);
    builder.add_header(
        "set-cookie",
        &format!(
            "{}={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
            COOKIE_ACCESS_TOKEN, session.access_token, config.session_ttl_secs, domain
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        &format!(
            "{}={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
            COOKIE_ACCESS_KEY, session.access_key, config.session_ttl_secs, domain
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        &format!(
            "{}={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
            COOKIE_ACCESS_SECRET, session.access_secret, config.session_ttl_secs, domain
        ),
        None,
        false,
    );
    if let Some(max_age) = refresh_max_age {
        builder.add_header(
            "set-cookie",
            &format!(
                "{}={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
                COOKIE_REFRESH_TOKEN, refresh_token, max_age, domain
            ),
            None,
            false,
        );
    }
    builder.add_header(
        "set-cookie",
        &format!(
            "{}={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
            COOKIE_CLIENT_DEVICE_ID, session.client_device_id, domain
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        &format!(
            "{}={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
            COOKIE_TENANT_ID, session.tenant_id_val, domain
        ),
        None,
        false,
    );
    builder.add_header(
        "set-cookie",
        &format!(
            "{}={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
            COOKIE_ZONE_CODE, zone_code, domain
        ),
        None,
        false,
    );
    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("OAuth login success"));
    response.set_http_response(builder);
    Response::new(response)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn return_path_allowlist_rejects_external_and_ambiguous_paths() {
        assert!(safe_return_to("/"));
        assert!(safe_return_to("/billing/authorize?request_id=abc"));
        assert!(!safe_return_to("//attacker.example"));
        assert!(!safe_return_to("https://attacker.example"));
        assert!(!safe_return_to("/billing/authorize/other"));
    }

    #[test]
    fn canonical_identity_normalizes_only_provider_verified_fields() {
        let identity = canonical_identity(
            "google",
            " provider-subject ".to_string(),
            " USER@Example.COM ".to_string(),
            " Aurora User ".to_string(),
            Some("http://example.com/avatar".to_string()),
        )
        .expect("identity must be canonical");

        assert_eq!(identity.subject, "provider-subject");
        assert_eq!(identity.email, "user@example.com");
        assert_eq!(identity.display_name, "Aurora User");
        assert!(identity.avatar_url.is_none());
    }

    #[test]
    fn callback_failure_exposes_only_the_generic_error_and_returns_to_signin() {
        use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;

        let response = oauth_failure_redirect("/billing/authorize?request_id=opaque");
        let denied = match response.http_response {
            Some(HttpResponse::DeniedResponse(denied)) => denied,
            _ => panic!("OAuth failure must be an Envoy denied response"),
        };
        let location = denied
            .headers
            .into_iter()
            .filter_map(|option| option.header)
            .find(|header| header.key.eq_ignore_ascii_case("location"))
            .map(|header| header.value)
            .expect("OAuth failure response must redirect");

        assert!(location.starts_with("/signin?"));
        assert!(location.contains("oauth_error=OAUTH_SIGN_IN_FAILED"));
        assert!(location.contains("return_to=%2Fbilling%2Fauthorize%3Frequest_id%3Dopaque"));
        assert!(!location.contains("INVALID_CREDENTIALS"));
        assert!(!location.contains("PROVIDER_"));
    }
}
