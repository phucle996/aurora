//! Zone-local HPKE keyring and protected job payload opener.

use std::collections::HashMap;
use std::fs;
use std::path::Path;
use std::sync::Arc;

use base64::Engine as _;
use curve25519_dalek::constants::X25519_BASEPOINT;
use curve25519_dalek::montgomery::MontgomeryPoint;
use prost::Message;
use ring::{aead, hmac};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use uuid::Uuid;
use zeroize::Zeroize;

use crate::infra::kafka::transport_proto::{PayloadEncodingV1, ProtectedPayloadV1};

const MAX_PROTECTED_PAYLOAD_BYTES: usize = 1_000_256;
const MAX_PLAINTEXT_BYTES: usize = 1_000_000;
const HPKE_INFO: &[u8] = b"aurora.platform.job-payload.v1";

#[derive(Clone)]
pub struct PayloadKeyring {
    keys: Arc<HashMap<Uuid, Arc<PrivateKey>>>,
    readiness: Arc<Vec<LoadedPayloadKey>>,
}

struct PrivateKey([u8; 32]);

impl Drop for PrivateKey {
    fn drop(&mut self) {
        // Private material must not remain in allocator-owned memory after a
        // graceful keyring replacement or process shutdown.
        self.0.zeroize();
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct LoadedPayloadKey {
    pub key_id: Uuid,
    pub public_key_fingerprint: [u8; 32],
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PayloadKeyFile {
    keys: Vec<PayloadKeyEntry>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PayloadKeyEntry {
    key_id: String,
    private_key: String,
}

#[derive(Debug, Eq, PartialEq)]
pub struct PayloadOpenError {
    pub code: &'static str,
    pub message: String,
    pub retryable: bool,
}

impl PayloadOpenError {
    fn permanent(code: &'static str, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            retryable: false,
        }
    }

    fn retryable(code: &'static str, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            retryable: true,
        }
    }
}

impl PayloadKeyring {
    pub fn load(path: &Path) -> Result<Self, String> {
        let raw = fs::read(path).map_err(|error| {
            format!(
                "read JOB_PAYLOAD_PRIVATE_KEYS_FILE {} failed: {error}",
                path.display()
            )
        })?;
        if raw.is_empty() || raw.len() > 64 * 1024 {
            return Err("job payload private-key file must be in 1..=65536 bytes".to_string());
        }
        let document: PayloadKeyFile = serde_json::from_slice(&raw)
            .map_err(|error| format!("decode job payload private-key file failed: {error}"))?;
        if document.keys.is_empty() || document.keys.len() > 64 {
            return Err("job payload keyring must contain 1..=64 keys".to_string());
        }

        let mut keys = HashMap::with_capacity(document.keys.len());
        let mut readiness = Vec::with_capacity(document.keys.len());
        for entry in document.keys {
            let key_id = Uuid::parse_str(entry.key_id.trim())
                .map_err(|_| "job payload key_id must be a canonical UUID".to_string())?;
            if entry.key_id != key_id.to_string() {
                return Err("job payload key_id must use canonical lowercase UUID text".to_string());
            }
            let mut private_bytes = base64::engine::general_purpose::STANDARD
                .decode(entry.private_key.as_bytes())
                .map_err(|_| {
                    "job payload private_key must be padded standard Base64".to_string()
                })?;
            if base64::engine::general_purpose::STANDARD.encode(&private_bytes) != entry.private_key
                || private_bytes.len() != 32
            {
                return Err(
                    "job payload private_key must be canonical Base64 of exactly 32 bytes"
                        .to_string(),
                );
            }
            let mut private_raw = [0_u8; 32];
            private_raw.copy_from_slice(&private_bytes);
            private_bytes.zeroize();
            let private_key = PrivateKey(private_raw);
            let public_key = X25519_BASEPOINT.mul_clamped(private_key.0).to_bytes();
            let fingerprint: [u8; 32] = Sha256::digest(public_key).into();
            if keys.insert(key_id, Arc::new(private_key)).is_some() {
                return Err("job payload keyring contains a duplicate key_id".to_string());
            }
            readiness.push(LoadedPayloadKey {
                key_id,
                public_key_fingerprint: fingerprint,
            });
        }
        readiness.sort_by_key(|item| item.key_id);
        Ok(Self {
            keys: Arc::new(keys),
            readiness: Arc::new(readiness),
        })
    }

    pub fn loaded_keys(&self) -> &[LoadedPayloadKey] {
        self.readiness.as_ref()
    }

    // AAD fences remain explicit at the cryptographic boundary. Collapsing
    // them into a mutable object risks authenticating a field set from another flow.
    #[allow(clippy::too_many_arguments)]
    pub fn open(
        &self,
        protected_wire: &[u8],
        expected_zone_id: Uuid,
        source_domain: &str,
        job_topic: &str,
        resource_id: &str,
        job_version: u32,
        payload_schema_version: u32,
    ) -> Result<Vec<u8>, PayloadOpenError> {
        if protected_wire.is_empty() || protected_wire.len() > MAX_PROTECTED_PAYLOAD_BYTES {
            return Err(PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_SIZE_INVALID",
                "protected payload size is outside the platform limit",
            ));
        }
        let protected = ProtectedPayloadV1::decode(protected_wire).map_err(|_| {
            PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_PROTO_INVALID",
                "ProtectedPayloadV1 decode failed",
            )
        })?;
        if protected.schema_version != 1
            || protected.encoding
                != PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32
            || protected.encapsulated_key.len() != 32
            || protected.ciphertext.len() < 16
            || protected.plaintext_size == 0
            || protected.plaintext_size as usize > MAX_PLAINTEXT_BYTES
            || protected.ciphertext.len() != protected.plaintext_size as usize + 16
        {
            return Err(PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_INVALID",
                "ProtectedPayloadV1 security fields are invalid",
            ));
        }
        let recipient_zone_id = Uuid::from_slice(&protected.recipient_zone_id).map_err(|_| {
            PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_ZONE_INVALID",
                "protected recipient Zone must be a 16-byte UUID",
            )
        })?;
        if recipient_zone_id != expected_zone_id {
            return Err(PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_ZONE_MISMATCH",
                "protected recipient Zone does not match the consumer Zone",
            ));
        }
        let key_id = Uuid::from_slice(&protected.key_id).map_err(|_| {
            PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_KEY_ID_INVALID",
                "protected key_id must be a 16-byte UUID",
            )
        })?;
        let private_key = self.keys.get(&key_id).ok_or_else(|| {
            // Missing local material is a deployment/readiness failure. Keep
            // the Kafka offset uncommitted so a corrected rollout can recover.
            PayloadOpenError::retryable(
                "JOB_PROTECTED_PAYLOAD_KEY_UNAVAILABLE",
                "protected payload key is not loaded by this Dataplane",
            )
        })?;
        let encapsulated_key: [u8; 32] =
            protected
                .encapsulated_key
                .as_slice()
                .try_into()
                .map_err(|_| {
                    PayloadOpenError::permanent(
                        "JOB_PROTECTED_PAYLOAD_KEM_INVALID",
                        "protected encapsulated key is not an X25519 public key",
                    )
                })?;
        let shared_dh = MontgomeryPoint(encapsulated_key)
            .mul_clamped(private_key.0)
            .to_bytes();
        if shared_dh == [0_u8; 32] {
            // RFC 7748 requires rejecting low-order inputs that collapse the
            // shared secret to all zeroes.
            return Err(PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_KEM_INVALID",
                "protected encapsulated key failed X25519 agreement",
            ));
        }
        let recipient_public_key = X25519_BASEPOINT.mul_clamped(private_key.0).to_bytes();
        let shared_secret =
            derive_kem_shared_secret(&shared_dh, &encapsulated_key, &recipient_public_key)
                .map_err(|_| {
                    PayloadOpenError::permanent(
                        "JOB_PROTECTED_PAYLOAD_KEM_INVALID",
                        "protected payload KEM derivation failed",
                    )
                })?;
        let (key, nonce) = derive_key_schedule(&shared_secret, HPKE_INFO)?;
        let aad = additional_data(
            key_id,
            expected_zone_id,
            source_domain,
            job_topic,
            resource_id,
            job_version,
            payload_schema_version,
        )?;
        let unbound = aead::UnboundKey::new(&aead::AES_256_GCM, &key).map_err(|_| {
            PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_CRYPTO_INVALID",
                "HPKE content key is invalid",
            )
        })?;
        let less_safe = aead::LessSafeKey::new(unbound);
        let nonce = aead::Nonce::try_assume_unique_for_key(&nonce).map_err(|_| {
            PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_CRYPTO_INVALID",
                "HPKE nonce is invalid",
            )
        })?;
        let mut plaintext = protected.ciphertext;
        let opened = less_safe
            .open_in_place(nonce, aead::Aad::from(aad.as_slice()), &mut plaintext)
            .map_err(|_| {
                PayloadOpenError::permanent(
                    "JOB_PROTECTED_PAYLOAD_AUTH_FAILED",
                    "protected payload authentication failed",
                )
            })?;
        let opened_len = opened.len();
        plaintext.truncate(opened_len);
        if plaintext.len() != protected.plaintext_size as usize {
            return Err(PayloadOpenError::permanent(
                "JOB_PROTECTED_PAYLOAD_SIZE_MISMATCH",
                "opened payload length does not match the protected envelope",
            ));
        }
        Ok(plaintext)
    }
}

#[cfg(test)]
impl PayloadKeyring {
    pub(crate) fn for_test() -> Self {
        let key_id = Uuid::from_u128(1);
        let private_key = PrivateKey([0x42; 32]);
        let public_key = X25519_BASEPOINT.mul_clamped(private_key.0).to_bytes();
        let fingerprint: [u8; 32] = Sha256::digest(public_key).into();
        let mut keys = HashMap::new();
        keys.insert(key_id, Arc::new(private_key));
        Self {
            keys: Arc::new(keys),
            readiness: Arc::new(vec![LoadedPayloadKey {
                key_id,
                public_key_fingerprint: fingerprint,
            }]),
        }
    }

    #[allow(clippy::too_many_arguments)]
    pub(crate) fn protect_for_test(
        &self,
        zone_id: Uuid,
        source_domain: &str,
        job_topic: &str,
        resource_id: &str,
        job_version: u32,
        payload_schema_version: u32,
        plaintext: &[u8],
    ) -> Vec<u8> {
        let (key_id, recipient_private) = self.keys.iter().next().expect("test keyring key");
        let recipient_public = X25519_BASEPOINT.mul_clamped(recipient_private.0).to_bytes();
        let ephemeral_private = [0x24; 32];
        let encapsulated_key = X25519_BASEPOINT.mul_clamped(ephemeral_private).to_bytes();
        let shared_dh = MontgomeryPoint(recipient_public)
            .mul_clamped(ephemeral_private)
            .to_bytes();
        let shared_secret =
            derive_kem_shared_secret(&shared_dh, &encapsulated_key, &recipient_public)
                .expect("test KEM schedule");
        let (key, nonce) =
            derive_key_schedule(&shared_secret, HPKE_INFO).expect("test key schedule");
        let aad = additional_data(
            *key_id,
            zone_id,
            source_domain,
            job_topic,
            resource_id,
            job_version,
            payload_schema_version,
        )
        .expect("test AAD");
        let unbound = aead::UnboundKey::new(&aead::AES_256_GCM, &key).expect("test AES key");
        let less_safe = aead::LessSafeKey::new(unbound);
        let nonce = aead::Nonce::try_assume_unique_for_key(&nonce).expect("test nonce");
        let mut ciphertext = plaintext.to_vec();
        less_safe
            .seal_in_place_append_tag(nonce, aead::Aad::from(aad), &mut ciphertext)
            .expect("seal test protected payload");

        ProtectedPayloadV1 {
            schema_version: 1,
            recipient_zone_id: zone_id.as_bytes().to_vec(),
            key_id: key_id.as_bytes().to_vec(),
            encoding: PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32,
            encapsulated_key: encapsulated_key.to_vec(),
            ciphertext,
            plaintext_size: plaintext.len() as u32,
        }
        .encode_to_vec()
    }
}

fn additional_data(
    key_id: Uuid,
    zone_id: Uuid,
    source_domain: &str,
    job_topic: &str,
    resource_id: &str,
    job_version: u32,
    payload_schema_version: u32,
) -> Result<Vec<u8>, PayloadOpenError> {
    let fields = [source_domain, job_topic, resource_id];
    if fields
        .iter()
        .any(|field| field.is_empty() || field.len() > u16::MAX as usize)
    {
        return Err(PayloadOpenError::permanent(
            "JOB_PROTECTED_PAYLOAD_AAD_INVALID",
            "protected payload route cannot be encoded as V1 AAD",
        ));
    }
    let mut aad =
        Vec::with_capacity(96 + source_domain.len() + job_topic.len() + resource_id.len());
    aad.extend_from_slice(b"AURORA-JOB-PAYLOAD-AAD-V1\0");
    aad.extend_from_slice(key_id.as_bytes());
    aad.extend_from_slice(zone_id.as_bytes());
    for field in fields {
        aad.extend_from_slice(&(field.len() as u16).to_be_bytes());
        aad.extend_from_slice(field.as_bytes());
    }
    aad.extend_from_slice(&job_version.to_be_bytes());
    aad.extend_from_slice(&payload_schema_version.to_be_bytes());
    Ok(aad)
}

fn derive_kem_shared_secret(
    dh: &[u8],
    encapsulated_key: &[u8],
    recipient_public_key: &[u8],
) -> Result<Vec<u8>, PayloadOpenError> {
    let kem_suite_id = b"KEM\x00\x20";
    let eae_prk = labeled_extract(&[], kem_suite_id, b"eae_prk", dh);
    let mut kem_context = Vec::with_capacity(64);
    kem_context.extend_from_slice(encapsulated_key);
    kem_context.extend_from_slice(recipient_public_key);
    labeled_expand(&eae_prk, kem_suite_id, b"shared_secret", &kem_context, 32)
}

fn derive_key_schedule(
    shared_secret: &[u8],
    info: &[u8],
) -> Result<([u8; 32], [u8; 12]), PayloadOpenError> {
    let suite_id = b"HPKE\x00\x20\x00\x01\x00\x02";
    let psk_id_hash = labeled_extract(&[], suite_id, b"psk_id_hash", &[]);
    let info_hash = labeled_extract(&[], suite_id, b"info_hash", info);
    let mut context = Vec::with_capacity(65);
    context.push(0); // HPKE base mode.
    context.extend_from_slice(&psk_id_hash);
    context.extend_from_slice(&info_hash);
    let secret = labeled_extract(shared_secret, suite_id, b"secret", &[]);
    let key: [u8; 32] = labeled_expand(&secret, suite_id, b"key", &context, 32)?
        .try_into()
        .map_err(|_| crypto_error())?;
    let nonce: [u8; 12] = labeled_expand(&secret, suite_id, b"base_nonce", &context, 12)?
        .try_into()
        .map_err(|_| crypto_error())?;
    Ok((key, nonce))
}

fn labeled_extract(salt: &[u8], suite_id: &[u8], label: &[u8], ikm: &[u8]) -> Vec<u8> {
    let key = hmac::Key::new(hmac::HMAC_SHA256, salt);
    let mut context = hmac::Context::with_key(&key);
    context.update(b"HPKE-v1");
    context.update(suite_id);
    context.update(label);
    context.update(ikm);
    context.sign().as_ref().to_vec()
}

fn labeled_expand(
    prk: &[u8],
    suite_id: &[u8],
    label: &[u8],
    info: &[u8],
    length: usize,
) -> Result<Vec<u8>, PayloadOpenError> {
    if length == 0 || length > 255 * 32 || length > u16::MAX as usize {
        return Err(crypto_error());
    }
    let mut labeled_info = Vec::with_capacity(2 + 7 + suite_id.len() + label.len() + info.len());
    labeled_info.extend_from_slice(&(length as u16).to_be_bytes());
    labeled_info.extend_from_slice(b"HPKE-v1");
    labeled_info.extend_from_slice(suite_id);
    labeled_info.extend_from_slice(label);
    labeled_info.extend_from_slice(info);

    let key = hmac::Key::new(hmac::HMAC_SHA256, prk);
    let mut output = Vec::with_capacity(length);
    let mut previous = Vec::new();
    let mut counter = 1_u8;
    while output.len() < length {
        let mut context = hmac::Context::with_key(&key);
        context.update(&previous);
        context.update(&labeled_info);
        context.update(&[counter]);
        previous = context.sign().as_ref().to_vec();
        output.extend_from_slice(&previous);
        counter = counter.checked_add(1).ok_or_else(crypto_error)?;
    }
    output.truncate(length);
    Ok(output)
}

fn crypto_error() -> PayloadOpenError {
    PayloadOpenError::permanent(
        "JOB_PROTECTED_PAYLOAD_CRYPTO_INVALID",
        "HPKE key schedule is invalid",
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Deserialize)]
    struct ProtectedPayloadVector {
        schema_version: u32,
        key_id: String,
        recipient_zone_id: String,
        source_domain: String,
        job_topic: String,
        resource_id: String,
        job_version: u32,
        payload_schema_version: u32,
        plaintext_base64: String,
        protected_payload_base64: String,
    }

    #[test]
    fn opens_the_canonical_go_protected_payload_vector() {
        let vector: ProtectedPayloadVector = serde_json::from_str(include_str!(
            "../../../contracts/testdata/protected_payload_v1.json"
        ))
        .expect("canonical protected-payload vector");
        // The fixed test-only recipient key lives in test code, never in a
        // shared wire fixture that could be mistaken for deployable material.
        assert_eq!(vector.schema_version, 1);
        assert_eq!(vector.key_id, Uuid::from_u128(1).to_string());

        let keyring = PayloadKeyring::for_test();
        let wire = base64::engine::general_purpose::STANDARD
            .decode(vector.protected_payload_base64)
            .expect("vector protected payload");
        let plaintext = keyring
            .open(
                &wire,
                Uuid::parse_str(&vector.recipient_zone_id).expect("vector Zone UUID"),
                &vector.source_domain,
                &vector.job_topic,
                &vector.resource_id,
                vector.job_version,
                vector.payload_schema_version,
            )
            .expect("Rust must open the canonical Go ciphertext");
        let expected = base64::engine::general_purpose::STANDARD
            .decode(vector.plaintext_base64)
            .expect("vector plaintext");
        assert_eq!(plaintext, expected);

        let tampered = keyring
            .open(
                &wire,
                Uuid::parse_str(&vector.recipient_zone_id).unwrap(),
                &vector.source_domain,
                &vector.job_topic,
                "bucket-2",
                vector.job_version,
                vector.payload_schema_version,
            )
            .unwrap_err();
        assert_eq!(tampered.code, "JOB_PROTECTED_PAYLOAD_AUTH_FAILED");
    }
}
