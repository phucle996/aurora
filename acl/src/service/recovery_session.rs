use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::config::Config;
use crate::core::session::{RecoverySessionCache, SessionManager};
use crate::core::token::{Claims, TokenManager};
use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;
use crate::service::ext_authz::{extract_cookie_value, sha256_hash};

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

// [COMMENT]: Dựng response OK cho Envoy để tiếp tục chuyển tiếp request lên upstream
fn build_success_response(
    user_id: &str,
    role: &str,
    level: i32,
    tenant_id: &str,
    new_jwt: &str,
    new_access_key: &str,
    new_access_secret: &str,
    cookie_header: &str,
) -> Result<Response<CheckResponse>, Status> {
    let mut ok_response = CheckResponse::with_status(Status::ok("authorized"));
    ok_response.set_http_response(envoy_types::ext_authz::v3::pb::OkHttpResponse::default());

    // [COMMENT]: Lấy client_device_id từ cookie hoặc sinh ngẫu nhiên nếu thiếu để tracking thiết bị
    let device_id = extract_cookie_value(cookie_header, "client_device_id")
        .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());

    if let Some(ref mut http_resp) = ok_response.http_response {
        use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
        if let HttpResponse::OkResponse(ref mut ok) = http_resp {
            use envoy_types::pb::envoy::config::core::v3::{HeaderValue, HeaderValueOption};
            ok.headers.push(HeaderValueOption {
                header: Some(HeaderValue {
                    key: "x-user-id".to_string(),
                    value: user_id.to_string(),
                    ..Default::default()
                }),
                ..Default::default()
            });
            ok.headers.push(HeaderValueOption {
                header: Some(HeaderValue {
                    key: "x-device-id".to_string(),
                    value: device_id.clone(),
                    ..Default::default()
                }),
                ..Default::default()
            });
            ok.headers.push(HeaderValueOption {
                header: Some(HeaderValue {
                    key: "x-user-role".to_string(),
                    value: role.to_string(),
                    ..Default::default()
                }),
                ..Default::default()
            });
            ok.headers.push(HeaderValueOption {
                header: Some(HeaderValue {
                    key: "x-user-level".to_string(),
                    value: level.to_string(),
                    ..Default::default()
                }),
                ..Default::default()
            });
            if !tenant_id.is_empty() {
                ok.headers.push(HeaderValueOption {
                    header: Some(HeaderValue {
                        key: "x-tenant-id".to_string(),
                        value: tenant_id.to_string(),
                        ..Default::default()
                    }),
                    ..Default::default()
                });
            }

            // [COMMENT]: Bơm Set-Cookie headers chứa bộ ba Trinity mới trả về cho client
            let cookies = vec![
                format!(
                    "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax",
                    new_jwt
                ),
                format!(
                    "access_key={}; Path=/; HttpOnly; Secure; SameSite=Lax",
                    new_access_key
                ),
                format!(
                    "access_secret={}; Path=/; HttpOnly; Secure; SameSite=Lax",
                    new_access_secret
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
    control_plane_client: &Arc<ControlPlaneClient>,
    config: &Config,
    cookie_header: &str,
    err_msg: &str,
    method: &str,
    path: &str,
) -> Result<Response<CheckResponse>, Status> {
    // [COMMENT]: Mặc định nếu không có refresh token hoặc xác thực thất bại sẽ xóa sạch cookies
    let mut clear_refresh_token = true;
    let mut final_err_msg = err_msg.to_string();

    // [COMMENT]: Kiểm tra xem client có mang theo refresh_token cookie không
    if let Some(refresh_token) = extract_cookie_value(cookie_header, "refresh_token") {
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
                    &cached.role,
                    cached.level,
                    &cached.tenant_id,
                    &cached.new_jwt,
                    &cached.new_access_key,
                    &cached.new_access_secret,
                    cookie_header,
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
                                &cached.role,
                                cached.level,
                                &cached.tenant_id,
                                &cached.new_jwt,
                                &cached.new_access_key,
                                &cached.new_access_secret,
                                cookie_header,
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

        // [COMMENT]: Gọi gRPC xác thực refresh token tới Controlplane
        match control_plane_client
            .verify_opaque_refresh_token(refresh_token.clone(), "user".to_string())
            .await
        {
            Ok(verify_res) if verify_res.valid => {
                // [COMMENT]: 1. Sinh bộ Trinity Credentials mới gồm Access Key và Access Secret
                let new_access_key = uuid::Uuid::now_v7().to_string();
                let new_access_secret = uuid::Uuid::new_v4().to_string();
                let new_ash = sha256_hash(&new_access_secret);

                // [COMMENT]: 2. Lấy client_device_id từ cookie hoặc sinh ngẫu nhiên nếu thiếu để tracking thiết bị
                let device_id = extract_cookie_value(cookie_header, "client_device_id")
                    .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());

                // [COMMENT]: 3. Tính toán thời hạn hết hạn của phiên dựa trên cấu hình session_ttl_secs
                let now_unix = chrono::Utc::now().timestamp();
                let exp_unix = now_unix + config.session_ttl_secs as i64;

                // [COMMENT]: 4. Tạo Claims mới cho JWT Access Token (không bao gồm zone_id, zone xử lý sau)
                let new_claims = Claims {
                    sub: verify_res.user_id.clone(),
                    role: verify_res.role.clone(),
                    lvl: verify_res.level,
                    tenant_id: if verify_res.tenant_id.is_empty() {
                        None
                    } else {
                        Some(verify_res.tenant_id.clone())
                    },
                    zone_id: None,
                    access_key: new_access_key.clone(),
                    jti: uuid::Uuid::new_v4().to_string(),
                    iss: Some("aurora-acl".to_string()),
                    exp: exp_unix,
                    iat: now_unix,
                };

                // [COMMENT]: 5. Ký JWT Token mới thông qua Vault Transit Engine
                match token_mgr.generate_token(&new_claims).await {
                    Ok(new_jwt) => {
                        // [COMMENT]: 6. Lưu session mới vào Redis L2 và đồng bộ active index key của user
                        match session_mgr
                            .register_session(
                                &verify_res.user_id,
                                &new_access_key,
                                &new_ash,
                                &device_id,
                            )
                            .await
                        {
                            Ok(_) => {
                                Logger::sys_info(
                                    "ext_authz.recovery_session",
                                    &format!(
                                        "Transparent session recovery successful for user_id={}",
                                        verify_res.user_id
                                    ),
                                );

                                // [COMMENT]: Lưu thông tin phục hồi thành công vào cache để các request song song khác đọc
                                let cache_item = RecoverySessionCache {
                                    user_id: verify_res.user_id.clone(),
                                    role: verify_res.role.clone(),
                                    level: verify_res.level,
                                    tenant_id: verify_res.tenant_id.clone(),
                                    new_jwt: new_jwt.clone(),
                                    new_access_key: new_access_key.clone(),
                                    new_access_secret: new_access_secret.clone(),
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
                                    &new_jwt,
                                    &new_access_key,
                                    &new_access_secret,
                                    cookie_header,
                                );
                            }
                            Err(e) => {
                                Logger::sys_error(
                                    "ext_authz.recovery_session",
                                    "Failed to register recovered session in Redis L2",
                                    &e.to_string(),
                                );
                                if is_leader {
                                    release_recovery_lock(session_mgr, &token_hash).await;
                                }
                                // [COMMENT]: Lỗi hạ tầng Redis -> KHÔNG xóa refresh_token để giữ phiên cho client thử lại sau
                                clear_refresh_token = false;
                                final_err_msg =
                                    format!("infra error: redis register failed ({})", e);
                            }
                        }
                    }
                    Err(e) => {
                        Logger::sys_error(
                            "ext_authz.recovery_session",
                            "Failed to generate recovered JWT token via Vault",
                            &e.to_string(),
                        );
                        if is_leader {
                            release_recovery_lock(session_mgr, &token_hash).await;
                        }
                        // [COMMENT]: Lỗi hạ tầng Vault -> KHÔNG xóa refresh_token để giữ phiên cho client thử lại sau
                        clear_refresh_token = false;
                        final_err_msg = format!("infra error: vault sign failed ({})", e);
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
