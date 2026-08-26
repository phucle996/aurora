use std::collections::HashMap;
use std::time::{SystemTime, UNIX_EPOCH};

use base64::Engine;
use ed25519_dalek::{Signer, SigningKey};
use sha2::{Digest, Sha256};

use super::{verify_assertion, RuntimeReadError};

fn signed_headers(
    path: &str,
) -> (
    HashMap<String, ed25519_dalek::VerifyingKey>,
    HashMap<String, String>,
    String,
) {
    let signing_key = SigningKey::from_bytes(&[7_u8; 32]);
    let zone_id = uuid::Uuid::new_v4().to_string();
    let route = path.split('?').next().expect("runtime path");
    let segments = route.split('/').collect::<Vec<_>>();
    let module = segments.get(4).expect("module");
    let resource_type = segments.get(5).expect("resource type");
    let resource_id = segments.get(6).expect("resource id");
    let panel_id = segments.get(7).expect("panel");
    let component_id = segments.get(8).copied();
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_secs() as i64;
    let assertion = serde_json::json!({
        "schema_version": 1,
        "jti": uuid::Uuid::new_v4().to_string(),
        "actor_id": uuid::Uuid::new_v4().to_string(),
        "owner_id": uuid::Uuid::new_v4().to_string(),
        "owner_type": "PERSONAL",
        "workspace_id": uuid::Uuid::new_v4().to_string(),
        "zone_id": zone_id,
        "module": module,
        "resource_type": resource_type,
        "resource_id": resource_id,
        "panel_id": panel_id,
        "component_id": component_id,
        "capability": "runtime.read",
        "method": "GET",
        "path_hash": format!("{:x}", Sha256::digest(path.as_bytes())),
        "issued_at": now,
        "expires_at": now + 10,
        "audience": "zone-public-edge-gateway",
        "issuer": "aurora-acr",
        "key_id": "runtime:v1"
    });
    let encoded = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .encode(serde_json::to_vec(&assertion).expect("assertion JSON"));
    let signature = base64::engine::general_purpose::STANDARD
        .encode(signing_key.sign(encoded.as_bytes()).to_bytes());
    let keys = HashMap::from([("runtime:v1".to_string(), signing_key.verifying_key())]);
    let headers = HashMap::from([
        ("x-aurora-runtime-assertion".to_string(), encoded),
        ("x-aurora-runtime-signature".to_string(), signature),
        (
            "x-aurora-runtime-key-id".to_string(),
            "runtime:v1".to_string(),
        ),
    ]);
    (keys, headers, zone_id)
}

#[test]
fn signed_runtime_assertion_is_bound_to_exact_request() {
    let resource_id = uuid::Uuid::new_v4();
    let path =
        format!("/zone-public/v1/runtime/mail/consumer/{resource_id}/health?from_seconds=60");
    let (keys, headers, zone_id) = signed_headers(&path);
    let assertion =
        verify_assertion(&keys, &zone_id, &headers, "GET", &path).expect("valid signed assertion");
    assert_eq!(assertion.resource_id, resource_id.to_string());

    assert!(matches!(
        verify_assertion(
            &keys,
            &zone_id,
            &headers,
            "GET",
            &path.replace("health", "logs")
        ),
        Err(RuntimeReadError::Denied(_))
    ));
}

#[test]
fn signed_runtime_assertion_rejects_query_scope_widening() {
    let resource_id = uuid::Uuid::new_v4();
    let path = format!("/zone-public/v1/runtime/mail/consumer/{resource_id}/health?query=all");
    let (keys, headers, zone_id) = signed_headers(&path);
    assert!(matches!(
        verify_assertion(&keys, &zone_id, &headers, "GET", &path),
        Err(RuntimeReadError::Denied("RUNTIME_QUERY_INVALID"))
    ));
}

#[test]
fn signed_runtime_assertion_requires_a_bounded_window() {
    let resource_id = uuid::Uuid::new_v4();
    let path = format!("/zone-public/v1/runtime/mail/consumer/{resource_id}/health");
    let (keys, headers, zone_id) = signed_headers(&path);
    assert!(matches!(
        verify_assertion(&keys, &zone_id, &headers, "GET", &path),
        Err(RuntimeReadError::Denied("RUNTIME_ASSERTION_BINDING_INVALID"))
    ));
}

#[test]
fn signed_runtime_assertion_accepts_generic_resource_and_component() {
    let resource_id = uuid::Uuid::new_v4();
    let path = format!(
        "/zone-public/v1/runtime/hypervisor/vm/{resource_id}/metrics/nic-0?from_seconds=30"
    );
    let (keys, headers, zone_id) = signed_headers(&path);
    let assertion =
        verify_assertion(&keys, &zone_id, &headers, "GET", &path).expect("valid generic assertion");
    assert_eq!(assertion.module, "hypervisor");
    assert_eq!(assertion.resource_type, "vm");
    assert_eq!(assertion.component_id.as_deref(), Some("nic-0"));
}
