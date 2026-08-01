/// Authoritative command allow-list used by the PostgreSQL changefeed. Managed
/// Service command dispatch is enabled in P05 while its result route remains
/// closed until the P07 settlement transaction exists.
pub fn is_command_registered(source_domain: &str, job_topic: &str) -> bool {
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
        "MANAGED_SERVICE" => job_topic == "managed_service.instance.execute",
        _ => false,
    }
}

/// Result admission is deliberately independent from command dispatch. Sharing
/// this registry would allow a P05 Managed Service result into the generic result
/// worker before P07 can atomically settle its outbox/object/operation fence.
pub fn is_result_registered(source_domain: &str, job_topic: &str) -> bool {
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
        "MANAGED_SERVICE" => false,
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::{is_command_registered, is_result_registered};

    #[test]
    fn route_requires_the_expected_source_domain() {
        assert!(is_command_registered("STORAGE", "storage.bucket.create"));
        assert!(is_command_registered("STORAGE", "storage.access.prepare"));
        assert!(!is_command_registered("STORAGE", "storage.object.sts"));
        assert!(!is_command_registered("MAIL", "storage.bucket.create"));
        assert!(!is_command_registered("STORAGE", "storage.unknown"));
        assert!(is_command_registered("HYPERVISOR", "hypervisor.vm.create"));
        assert!(is_command_registered(
            "HYPERVISOR",
            "hypervisor.image.import"
        ));
        assert!(is_command_registered(
            "HYPERVISOR",
            "hypervisor.image.delete"
        ));
        assert!(!is_command_registered("STORAGE", "hypervisor.vm.create"));
        assert!(is_command_registered(
            "MANAGED_SERVICE",
            "managed_service.instance.execute"
        ));
    }

    #[test]
    fn managed_service_result_remains_closed_until_p07() {
        assert!(!is_result_registered(
            "MANAGED_SERVICE",
            "managed_service.instance.execute"
        ));
        assert!(is_result_registered("STORAGE", "storage.bucket.create"));
    }
}
