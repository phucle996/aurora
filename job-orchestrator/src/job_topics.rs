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
                | "storage.object.sts"
        ),
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::is_registered;

    #[test]
    fn route_requires_the_expected_source_domain() {
        assert!(is_registered("STORAGE", "storage.bucket.create"));
        assert!(!is_registered("MAIL", "storage.bucket.create"));
        assert!(!is_registered("STORAGE", "storage.unknown"));
    }
}
