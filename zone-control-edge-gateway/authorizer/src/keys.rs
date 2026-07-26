use std::collections::HashMap;

use base64::Engine;
use ed25519_dalek::{Signature, VerifyingKey};

use crate::error::AuthzError;

#[derive(Clone)]
pub struct AssertionKeys {
    keys: HashMap<String, VerifyingKey>,
}

impl AssertionKeys {
    pub fn new(keys: HashMap<String, [u8; 32]>) -> Result<Self, AuthzError> {
        let keys = keys
            .into_iter()
            .map(|(id, bytes)| {
                VerifyingKey::from_bytes(&bytes)
                    .map(|key| (id, key))
                    .map_err(|_| AuthzError::Configuration("invalid Ed25519 public key".into()))
            })
            .collect::<Result<_, _>>()?;
        Ok(Self { keys })
    }

    pub fn verify(&self, key_id: &str, message: &[u8], signature: &str) -> Result<(), AuthzError> {
        let key = self
            .keys
            .get(key_id)
            .ok_or(AuthzError::Denied("ASSERTION_KEY_UNKNOWN"))?;
        let signature = base64::engine::general_purpose::STANDARD
            .decode(signature)
            .map_err(|_| AuthzError::Denied("ASSERTION_SIGNATURE_ENCODING_INVALID"))?;
        let signature: [u8; 64] = signature
            .try_into()
            .map_err(|_| AuthzError::Denied("ASSERTION_SIGNATURE_SIZE_INVALID"))?;
        key.verify_strict(message, &Signature::from_bytes(&signature))
            .map_err(|_| AuthzError::Denied("ASSERTION_SIGNATURE_INVALID"))
    }
}
