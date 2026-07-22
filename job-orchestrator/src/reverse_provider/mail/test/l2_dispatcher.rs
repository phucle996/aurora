#[test]
fn mail_result_topics_remain_explicit() {
    let source = include_str!("../l2_dispatcher.rs");
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
    let consumer = include_str!("../service/consumer_result.rs");
    let template = include_str!("../service/template_result.rs");
    assert!(consumer.contains("mail_consumer_update_versions"));
    assert!(consumer.contains("config_version < $2"));
    assert!(!consumer.contains("desired_state='deleting'"));
    assert!(template.contains("mail.allow_template_version_mutation"));
    assert!(template.contains("template_revision=candidate.template_revision"));
}
