#[allow(dead_code)]
pub mod mail {
    include!(concat!(env!("OUT_DIR"), "/mail.runtime.v1.rs"));
}

#[allow(dead_code)]
pub mod storage {
    include!(concat!(env!("OUT_DIR"), "/storage.rs"));
}

#[allow(dead_code)]
pub mod hypervisor {
    include!(concat!(env!("OUT_DIR"), "/hypervisor.rs"));
}

pub mod storage_usage_report {
    include!(concat!(env!("OUT_DIR"), "/aurora.storage.metering.v1.rs"));
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
    let _ = hypervisor::VmCreateV1::default();
    let _ = hypervisor::VmCreateResultV1::default();
    let _ = hypervisor::ImageImportV1::default();
    let _ = hypervisor::ImageImportResultV1::default();
    let _ = hypervisor::ImageDeleteV1::default();
    let _ = hypervisor::ImageDeleteResultV1::default();
    let _ = mail::MailDispatchEnvelopeV1::default();
}
