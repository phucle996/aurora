pub mod mail {
    include!(concat!(env!("OUT_DIR"), "/mail.runtime.v1.rs"));
}

pub mod storage {
    include!(concat!(env!("OUT_DIR"), "/storage.rs"));
}

pub fn verify_generated_contracts() {
    // These messages are used by sibling services. Constructing them here keeps
    // protobuf drift visible to the compiler even when JO does not consume them.
    let _ = storage::BucketCreateSync::default();
    let _ = storage::CredentialSync::default();
    let _ = storage::BucketDeleteSync::default();
    let _ = storage::StorageAccessPrepareRequest::default();
    let _ = storage::StorageAccessPrepareResponse::default();
    let _ = storage::StorageAccessRecord::default();
    let _ = mail::MailDispatchEnvelopeV1::default();
}
