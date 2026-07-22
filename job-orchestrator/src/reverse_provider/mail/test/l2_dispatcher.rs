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
