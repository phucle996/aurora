use base64::Engine;
use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::config::Config;
use crate::core::session::{RecoverySessionCache, SessionManager};
use crate::core::token::TokenManager;
use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;
// [COMMENT]: Dùng release_user_session để gen Trinity (access_key, access_secret, JWT, Redis L2)
// thay vì viết lại logic từ đầu
use crate::pkg::cookie::*;
use crate::pkg::header::*;
use crate::service::ext_authz::{extract_cookie_value, sha256_hash};
use crate::service::session::release_session::release_user_session;

// [COMMENT]: Giải phóng lock recovery session trong Redis L2
async fn release_recovery_lock(session_mgr: &SessionManager, token_hash: &str) {
    if let Err(e) = session_mgr.release_recovery_lock(token_hash).await {
        Logger::sys_error(
            "ext_authz.recovery_session",
            "Failed to release recovery lock",
            &e.to_string(),
        );
    }
}

// [COMMENT]: Giải mã không an toàn (unsafe decode) payload của JWT token cũ (bị hết hạn/lỗi chữ ký)
// để trích xuất user_id (uid) và tenant_id (tnc / tenant_id) phục vụ việc gửi sang Controlplane để so khớp.
// Sử dụng struct UnsafeClaims tối giản để tránh parse fail khi thiếu các system claims như exp/iat ở môi trường dev.
fn decode_jwt_claims_unsafe(token: &str) -> Option<(String, Option<String>)> {
    #[derive(serde::Deserialize)]
    struct UnsafeClaims {
        #[serde(rename = "uid")]
        uid: String,
        #[serde(rename = "tnc", alias = "tenant_id")]
        tenant_id: Option<String>,
    }

    let parts: Vec<&str> = token.split('.').collect();
    if parts.len() != 3 {
        return None;
    }
    let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;
    let payload_bytes = url_engine.decode(parts[1]).ok()?;
    let parsed: UnsafeClaims = serde_json::from_slice(&payload_bytes).ok()?;
    Some((parsed.uid, parsed.tenant_id))
}

// [COMMENT]: Dựng response OK cho Envoy để tiếp tục chuyển tiếp request lên upstream
fn build_success_response(
    user_id: &str,
    role_id: &str,
    level: i32,
    tenant_id: &str,
    new_jwt: &str,
    new_access_key: &str,
    new_access_secret: &str,
    cookie_header: &str,
    zone_id: &str,
    zone_code: &str,
) -> Result<Response<CheckResponse>, Status> {
    let mut ok_response = CheckResponse::with_status(Status::ok("authorized"));
    ok_response.set_http_response(envoy_types::ext_authz::v3::pb::OkHttpResponse::default());

    // [COMMENT]: Lấy client_device_id từ cookie hoặc sinh ngẫu nhiên nếu thiếu để tracking thiết bị
    let device_id = extract_cookie_value(cookie_header, COOKIE_CLIENT_DEVICE_ID)
        .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());

    // [COMMENT]: Lấy workspace_id từ cookie client gửi lên (user luôn có ít nhất 1 workspace)
    // ACR forward trực tiếp xuống CP qua header x-workspace-id mà không đưa vào JWT session
    let workspace_id = extract_cookie_value(cookie_header, COOKIE_WORKSPACE_ID);

    if let Some(ref mut http_resp) = ok_response.http_response {
        use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
        if let HttpResponse::OkResponse(ref mut ok) = http_resp {
            use envoy_types::pb::envoy::config::core::v3::{HeaderValue, HeaderValueOption};
            ok.headers.push(HeaderValueOption {
                header: Some(HeaderValue {
                    key: HEADER_X_USER_ID.to_string(),
                    value: user_id.to_string(),
                    ..Default::default()
                }),
                ..Default::default()
            });
            ok.headers.push(HeaderValueOption {
                header: Some(HeaderValue {
                    key: HEADER_X_DEVICE_ID.to_string(),
                    value: device_id.clone(),
                    ..Default::default()
                }),
                ..Default::default()
            });
            // [COMMENT]: Inject x-user-role-id — UUID của role đang hoạt động, dùng cho L1 cache lookup
            ok.headers.push(HeaderValueOption {
                header: Some(HeaderValue {
                    key: HEADER_X_USER_ROLE_ID.to_string(),
                    value: role_id.to_string(),
                    ..Default::default()
                }),
                ..Default::default()
            });
            ok.headers.push(HeaderValueOption {
                header: Some(HeaderValue {
                    key: HEADER_X_USER_LEVEL.to_string(),
                    value: level.to_string(),
                    ..Default::default()
                }),
                ..Default::default()
            });
            if !tenant_id.is_empty() {
                ok.headers.push(HeaderValueOption {
                    header: Some(HeaderValue {
                        key: HEADER_X_TENANT_ID.to_string(),
                        value: tenant_id.to_string(),
                        ..Default::default()
                    }),
                    ..Default::default()
                });
            }

            // [COMMENT]: Forward workspace_id cookie thành x-workspace-id header cho upstream CP
            // Không lưu workspace_id trong JWT — chỉ đọc từ cookie và forward trực tiếp
            if let Some(ws_id) = workspace_id {
                ok.headers.push(HeaderValueOption {
                    header: Some(HeaderValue {
                        key: HEADER_X_WORKSPACE_ID.to_string(),
                        value: ws_id,
                        ..Default::default()
                    }),
                    ..Default::default()
                });
            }

            // [COMMENT]: Chỉ inject header x-zone-id sang microservices, không gửi x-zone-code
            ok.headers.push(HeaderValueOption {
                header: Some(HeaderValue {
                    key: HEADER_X_ZONE_ID.to_string(),
                    value: zone_id.to_string(),
                    ..Default::default()
                }),
                ..Default::default()
            });

            // [COMMENT]: Bơm Set-Cookie headers chứa bộ ba Trinity và zone_code mới trả về cho client
            let cookies = vec![
                format!(
                    "{}={}; Path=/; HttpOnly; Secure; SameSite=Lax",
                    COOKIE_ACCESS_TOKEN, new_jwt
                ),
                format!(
                    "{}={}; Path=/; HttpOnly; Secure; SameSite=Lax",
                    COOKIE_ACCESS_KEY, new_access_key
                ),
                format!(
                    "{}={}; Path=/; HttpOnly; Secure; SameSite=Lax",
                    COOKIE_ACCESS_SECRET, new_access_secret
                ),
                format!(
                    "{}={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                    COOKIE_ZONE_CODE, zone_code
                ),
            ];
            for cookie in cookies {
                ok.response_headers_to_add.push(HeaderValueOption {
                    header: Some(HeaderValue {
                        key: "set-cookie".to_string(),
                        value: cookie,
                        ..Default::default()
                    }),
                    ..Default::default()
                });
            }
        }
    }

    Ok(Response::new(ok_response))
}

// [COMMENT]: Thực hiện hồi phục phiên tự động bằng Opaque Refresh Token qua gRPC kết nối tới Controlplane
pub async fn handle_session_recovery(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    zone_mgr: &Arc<crate::core::zone::ZoneManager>,
    control_plane_client: &Arc<ControlPlaneClient>,
    config: &Config,
    cookie_header: &str,
    client_headers: &std::collections::HashMap<String, String>,
    err_msg: &str,
    method: &str,
    path: &str,
) -> Result<Response<CheckResponse>, Status> {
    // [COMMENT]: Khách hàng bắt buộc phải cung cấp ngữ cảnh zone. Nếu thiếu hoặc sai -> Chặn ngay (Fail-Fast)
    let zone_res = crate::service::zone::zone_resolution::resolve_zone_context(
        zone_mgr,
        cookie_header,
        client_headers,
    )
    .await;

    let (resolved_zone_id, resolved_zone_code, resolved_zone_status) = match zone_res {
        Ok(res) => res,
        Err(err) => {
            let msg = match err {
                crate::service::zone::zone_resolution::ZoneResolutionError::Missing => {
                    "Missing zone context during session recovery"
                }
                crate::service::zone::zone_resolution::ZoneResolutionError::InvalidCode(code) => {
                    &format!("Requested zone code not found: {}", code)
                }
            };
            Logger::authz_log("unknown", method, path, "DENIED", msg);
            let mut denied_builder = DeniedHttpResponseBuilder::new();
            denied_builder.set_http_status(HttpStatusCode::BadRequest);
            let mut response = CheckResponse::new();
            response.set_status(Status::invalid_argument("Missing or invalid zone context"));
            response.set_http_response(denied_builder);
            return Ok(Response::new(response));
        }
    };

    // [COMMENT]: Chặn truy cập nếu zone đang không ở trạng thái hoạt động (active hoặc draining)
    if resolved_zone_status != "active" && resolved_zone_status != "draining" {
        Logger::authz_log(
            "unknown",
            method,
            path,
            "DENIED",
            &format!(
                "Forbidden access to inactive zone ({} is {}): user_id=unknown",
                resolved_zone_code, resolved_zone_status
            ),
        );
        let mut denied_builder = DeniedHttpResponseBuilder::new();
        denied_builder.set_http_status(HttpStatusCode::Forbidden);
        let mut response = CheckResponse::new();
        response.set_status(Status::permission_denied(
            "Forbidden access to inactive zone",
        ));
        response.set_http_response(denied_builder);
        return Ok(Response::new(response));
    }

    // [COMMENT]: Mặc định nếu không có refresh token hoặc xác thực thất bại sẽ xóa sạch cookies
    let mut clear_refresh_token = true;
    let mut final_err_msg = err_msg.to_string();

    // [COMMENT]: Kiểm tra xem client có mang theo refresh_token cookie không
    if let Some(refresh_token) = extract_cookie_value(cookie_header, COOKIE_REFRESH_TOKEN) {
        Logger::sys_info(
            "ext_authz.recovery_session",
            &format!(
                "Authentication failed ({}). Attempting transparent session recovery via gRPC to Controlplane...",
                err_msg
            ),
        );

        let token_hash = sha256_hash(&refresh_token);

        // [COMMENT]: Kiểm tra xem đã có kết quả recovery session trong Redis L2 do request song song khác chạy trước đó sinh ra chưa
        match session_mgr.get_recovery_cache(&token_hash).await {
            Ok(Some(cached)) => {
                Logger::sys_info(
                    "ext_authz.recovery_session",
                    "Found valid recovery session cache in Redis L2. Bypassing gRPC and Vault calls.",
                );
                return build_success_response(
                    &cached.user_id,
                    &cached.role_id,
                    cached.level,
                    &cached.tenant_id,
                    &cached.new_jwt,
                    &cached.new_access_key,
                    &cached.new_access_secret,
                    cookie_header,
                    &cached.zone_id,
                    &cached.zone_code,
                );
            }
            Err(e) => {
                Logger::sys_error(
                    "ext_authz.recovery_session",
                    "Failed to check recovery session cache in Redis L2",
                    &e.to_string(),
                );
            }
            Ok(None) => {}
        }

        let mut is_leader = false;
        // [COMMENT]: Thực hiện check lock và polling để chờ kết quả nếu là follower nhằm chống Thundering Herd (Blood Request)
        match session_mgr.try_lock_recovery(&token_hash).await {
            Ok(true) => {
                is_leader = true;
                Logger::sys_info(
                    "ext_authz.recovery_session",
                    "Acquired recovery lock. This request is now the Leader.",
                );
            }
            Ok(false) => {
                Logger::sys_info(
                    "ext_authz.recovery_session",
                    "Lock already acquired by another request. This request is a Follower. Polling for result...",
                );

                // Polling tối đa 30 lần mỗi 100ms (tổng cộng 3 giây)
                let mut attempts = 0;
                while attempts < 30 {
                    tokio::time::sleep(std::time::Duration::from_millis(100)).await;
                    attempts += 1;

                    match session_mgr.get_recovery_cache(&token_hash).await {
                        Ok(Some(cached)) => {
                            Logger::sys_info(
                                "ext_authz.recovery_session",
                                &format!("Follower successfully obtained cached recovery session after {} attempts.", attempts),
                            );
                            return build_success_response(
                                &cached.user_id,
                                &cached.role_id,
                                cached.level,
                                &cached.tenant_id,
                                &cached.new_jwt,
                                &cached.new_access_key,
                                &cached.new_access_secret,
                                cookie_header,
                                &cached.zone_id,
                                &cached.zone_code,
                            );
                        }
                        Ok(None) => {
                            // Check lock exists. Nếu lock mất mà cache vẫn rỗng (leader lỗi/expired), thoát poll và tự chạy
                            match session_mgr.is_recovery_locked(&token_hash).await {
                                Ok(false) => {
                                    Logger::sys_info(
                                        "ext_authz.recovery_session",
                                        "Recovery lock released without cache. Stopping poll to retry as leader/individual.",
                                    );
                                    break;
                                }
                                _ => {}
                            }
                        }
                        Err(e) => {
                            Logger::sys_error(
                                "ext_authz.recovery_session",
                                "Failed to get recovery cache during polling",
                                &e.to_string(),
                            );
                        }
                    }
                }

                // Nếu poll hết giờ hoặc lock bị huỷ giữa chừng mà chưa có cache, thử chiếm lock làm leader mới
                if let Ok(true) = session_mgr.try_lock_recovery(&token_hash).await {
                    is_leader = true;
                    Logger::sys_info(
                        "ext_authz.recovery_session",
                        "Acquired recovery lock after polling timeout. Becoming leader.",
                    );
                } else {
                    Logger::sys_info(
                        "ext_authz.recovery_session",
                        "Failed to acquire lock after poll. Proceeding to recover individually.",
                    );
                }
            }
            Err(e) => {
                Logger::sys_error(
                    "ext_authz.recovery_session",
                    "Redis lock check failed. Proceeding individually.",
                    &e.to_string(),
                );
            }
        }

        // [COMMENT]: Giải mã không an toàn access_token cũ để lấy uid và tenant_id
        let jwt_token_opt = extract_cookie_value(cookie_header, COOKIE_ACCESS_TOKEN);
        let claims_opt = jwt_token_opt
            .as_ref()
            .and_then(|t| decode_jwt_claims_unsafe(t));

        let (user_id, tenant_id) = match claims_opt {
            Some((uid, tid)) => (uid, tid),
            None => {
                Logger::sys_error(
                    "ext_authz.recovery_session",
                    "Unsafe JWT decode failed or access_token cookie missing. Cannot perform session recovery.",
                    "missing_user_context",
                );
                if is_leader {
                    release_recovery_lock(session_mgr, &token_hash).await;
                }
                let mut denied_builder = DeniedHttpResponseBuilder::new();
                denied_builder.set_http_status(HttpStatusCode::Unauthorized);
                let mut response = CheckResponse::new();
                response.set_status(Status::unauthenticated("Missing user context for recovery"));
                response.set_http_response(denied_builder);
                return Ok(Response::new(response));
            }
        };

        // [COMMENT]: Gọi gRPC xác thực refresh token tới Controlplane gửi kèm user_id và tenant_id
        match control_plane_client
            .verify_opaque_refresh_token(refresh_token.clone(), tenant_id, user_id)
            .await
        {
            Ok(verify_res) if verify_res.valid => {
                // [COMMENT]: Kiểm tra ràng buộc admin với zone global
                if !verify_res.role.eq_ignore_ascii_case("admin")
                    && (resolved_zone_code == "global"
                        || resolved_zone_id == "00000000-0000-0000-0000-000000000000")
                {
                    Logger::authz_log(
                        &verify_res.user_id,
                        method,
                        path,
                        "DENIED",
                        &format!(
                            "Forbidden global zone access for non-admin: user_id={}",
                            verify_res.user_id
                        ),
                    );
                    if is_leader {
                        release_recovery_lock(session_mgr, &token_hash).await;
                    }
                    let mut denied_builder = DeniedHttpResponseBuilder::new();
                    denied_builder.set_http_status(HttpStatusCode::Forbidden);
                    let mut response = CheckResponse::new();
                    response
                        .set_status(Status::permission_denied("Forbidden access to global zone"));
                    response.set_http_response(denied_builder);
                    return Ok(Response::new(response));
                }

                // [COMMENT]: Gọi release_user_session để tái sử dụng logic gen Trinity:
                // sinh access_key/secret, ký JWT qua Vault, ghi session vào Redis L2.
                // Truyền resolved_zone_id trực tiếp (đã là UUID) để tránh gọi ZoneManager lần nữa.
                let device_id = extract_cookie_value(cookie_header, COOKIE_CLIENT_DEVICE_ID)
                    .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());

                match release_user_session(
                    session_mgr,
                    token_mgr,
                    zone_mgr,
                    config,
                    &verify_res.user_id,
                    &verify_res.username,
                    &verify_res.role,
                    verify_res.level,
                    &verify_res.tenant_id,
                    &resolved_zone_id, // đã resolved sang UUID rồi, ZoneManager sẽ dùng trực tiếp
                    &device_id,
                    &device_id,
                    false, // trust_device không áp dụng cho recovery flow
                    "",    // refresh_token raw không cần thiết ở đây
                )
                .await
                {
                    Ok(res) => {
                        Logger::sys_info(
                            "ext_authz.recovery_session",
                            &format!(
                                "Transparent session recovery successful for user_id={}",
                                verify_res.user_id
                            ),
                        );

                        // [COMMENT]: Lưu kết quả recovery vào Redis cache để các request song song dùng lại
                        let cache_item = RecoverySessionCache {
                            user_id: verify_res.user_id.clone(),
                            role_id: verify_res.role.clone(),
                            level: verify_res.level,
                            tenant_id: verify_res.tenant_id.clone(),
                            new_jwt: res.access_token.clone(),
                            new_access_key: res.access_key.clone(),
                            new_access_secret: res.access_secret.clone(),
                            zone_id: resolved_zone_id.clone(),
                            zone_code: resolved_zone_code.clone(),
                        };
                        if is_leader {
                            if let Err(e) = session_mgr
                                .set_recovery_cache(&token_hash, &cache_item)
                                .await
                            {
                                Logger::sys_error(
                                    "ext_authz.recovery_session",
                                    "Failed to cache recovery session in Redis L2",
                                    &e.to_string(),
                                );
                            }
                        }

                        return build_success_response(
                            &verify_res.user_id,
                            &verify_res.role,
                            verify_res.level,
                            &verify_res.tenant_id,
                            &res.access_token,
                            &res.access_key,
                            &res.access_secret,
                            cookie_header,
                            &resolved_zone_id,
                            &resolved_zone_code,
                        );
                    }
                    Err(e) => {
                        // [COMMENT]: release_user_session thất bại (Vault hoặc Redis lỗi)
                        // -> KHÔNG xóa refresh_token để client có thể thử lại sau
                        Logger::sys_error(
                            "ext_authz.recovery_session",
                            "release_user_session failed during session recovery",
                            &e.message(),
                        );
                        if is_leader {
                            release_recovery_lock(session_mgr, &token_hash).await;
                        }
                        clear_refresh_token = false;
                        final_err_msg =
                            format!("infra error: release_user_session failed ({})", e.message());
                    }
                }
            }
            Ok(verify_res) => {
                Logger::sys_info(
                    "ext_authz.recovery_session",
                    &format!(
                        "Opaque refresh token verification returned invalid: {}",
                        verify_res.error_message
                    ),
                );
                if is_leader {
                    release_recovery_lock(session_mgr, &token_hash).await;
                }
                // [COMMENT]: Xác thực không hợp lệ thực sự -> Xóa sạch refresh_token cookie
                clear_refresh_token = true;
                final_err_msg = format!("token invalid: {}", verify_res.error_message);
            }
            Err(status) => {
                Logger::sys_error(
                    "ext_authz.recovery_session",
                    "gRPC error when calling VerifyOpaqueRefreshToken",
                    &status.message(),
                );
                if is_leader {
                    release_recovery_lock(session_mgr, &token_hash).await;
                }
                // [COMMENT]: Lỗi kết nối gRPC / DB phía Controlplane -> KHÔNG xóa refresh_token để giữ phiên cho client thử lại sau
                clear_refresh_token = false;
                final_err_msg = format!("infra error: grpc call failed ({})", status.message());
            }
        }
    }

    // [COMMENT]: Nếu không có refresh token hoặc việc khôi phục phiên thất bại:
    // Trả về 401 Unauthorized kèm xóa sạch toàn bộ session cookies tương ứng để đồng bộ trạng thái
    Logger::authz_log(
        "unknown",
        method,
        path,
        "DENIED",
        &format!(
            "Authentication failed ({}). No valid recovery path, clearing session cookies.",
            final_err_msg
        ),
    );

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::Unauthorized);

    // [COMMENT]: Luôn xóa bộ ba Trinity Access Cookies để client router chuyển trạng thái sang unauthenticated
    let mut cookies_to_clear = vec![
        "access_token=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
        "access_key=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
        "access_secret=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
    ];

    // [COMMENT]: Chỉ xóa refresh_token cookie nếu đó là lỗi xác thực (Token hết hạn/bị block), giữ lại nếu là lỗi hạ tầng hệ thống
    if clear_refresh_token {
        cookies_to_clear.push("refresh_token=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT");
    }

    for cookie in cookies_to_clear {
        denied_builder.add_header("set-cookie", cookie, None, false);
    }

    let mut response = CheckResponse::new();
    response.set_status(tonic::Status::unauthenticated(&final_err_msg));
    response.set_http_response(denied_builder);
    Ok(Response::new(response))
}
