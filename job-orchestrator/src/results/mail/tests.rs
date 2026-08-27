#[test]
fn mail_result_topics_remain_explicit() {
    let source = include_str!("apply.rs");
    for topic in [
        "mail.consumer.upsert",
        "mail.consumer.delete",
        "mail.template.version_published",
        "mail.template.deleted",
    ] {
        assert!(source.contains(topic), "dispatcher must route {topic}");
    }
}

#[test]
fn mail_result_services_keep_infrastructure_first_boundaries_visible() {
    let consumer = include_str!("consumer.rs");
    let template = include_str!("template.rs");
    assert!(consumer.contains("personal_mail_consumer_update_versions"));
    assert!(consumer.contains("tenant_mail_consumer_update_versions"));
    assert!(consumer.contains("config_version < $2"));
    assert!(consumer.contains("desired_state='deleting'"));
    assert!(consumer.contains("desired_state='draining'"));
    assert!(consumer.contains("desired_state='drained'"));
    assert!(template.contains("mail.allow_template_version_mutation"));
    assert!(template.contains("template_revision=candidate.template_revision"));
}
