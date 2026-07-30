// [COMMENT]: P01 compiles the inner contract but deliberately does not make a
// route dispatchable. P05 must change this only together with JO/DP consumers,
// topic ACLs and result settlement; an outbox row cannot accidentally enable it.
const MANAGED_SERVICE_ROUTE_ENABLED: bool = false;

/// Authoritative command/result route allow-list shared by changefeed and
/// result settlement. Adding a new executor requires updating this boundary
/// before JO will publish or accept its contract.
pub fn is_registered(source_domain: &str, job_topic: &str) -> bool {
    match source_domain {
        "MAIL" => matches!(
            job_topic,
            "mail.consumer.upsert"
                | "mail.consumer.delete"
                | "mail.template.version_published"
                | "mail.template.deleted"
        ),
        "STORAGE" => matches!(
            job_topic,
            "storage.bucket.create"
                | "storage.bucket.delete"
                | "storage.bucket.resize"
                | "storage.credential.create"
                | "storage.credential.delete"
                | "storage.access.prepare"
        ),
        "HYPERVISOR" => matches!(
            job_topic,
            "hypervisor.vm.create" | "hypervisor.image.import" | "hypervisor.image.delete"
        ),
        "MANAGED_SERVICE" => {
            MANAGED_SERVICE_ROUTE_ENABLED && job_topic == "managed_service.instance.execute"
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::is_registered;

    #[test]
    fn route_requires_the_expected_source_domain() {
        assert!(is_registered("STORAGE", "storage.bucket.create"));
        assert!(is_registered("STORAGE", "storage.access.prepare"));
        assert!(!is_registered("STORAGE", "storage.object.sts"));
        assert!(!is_registered("MAIL", "storage.bucket.create"));
        assert!(!is_registered("STORAGE", "storage.unknown"));
        assert!(is_registered("HYPERVISOR", "hypervisor.vm.create"));
        assert!(is_registered("HYPERVISOR", "hypervisor.image.import"));
        assert!(is_registered("HYPERVISOR", "hypervisor.image.delete"));
        assert!(!is_registered("STORAGE", "hypervisor.vm.create"));
        assert!(!is_registered(
            "MANAGED_SERVICE",
            "managed_service.instance.execute"
        ));
    }
}
