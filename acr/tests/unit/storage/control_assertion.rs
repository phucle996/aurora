use super::{
    action_for_request, contains_xml_element, path_targets_bucket, storage_body_is_allowed,
};

#[test]
fn storage_control_route_is_classified_and_bound() {
    let list = "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=2";
    assert_eq!(action_for_request("GET", list), Some("ListBucket"));
    assert_eq!(
        action_for_request(
            "GET",
            "/zone-control/v1/storage/buckets/ws-bucket/unreviewed/objects?list-type=2"
        ),
        None
    );
    assert!(path_targets_bucket(list, "ws-bucket", ""));
    assert!(!path_targets_bucket(list, "ws-bucket", "logs/"));
    assert!(path_targets_bucket(
        "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=2&prefix=logs%2F",
        "ws-bucket",
        "logs/"
    ));
    assert!(!path_targets_bucket(
        "/zone-control/v1/storage/buckets/ws-bucket/objects?prefix=logs%2F&prefix=",
        "ws-bucket",
        "logs/"
    ));
    assert!(!path_targets_bucket(
        "/zone-control/v1/storage/buckets/ws-bucket/objects?acl&list-type=2",
        "ws-bucket",
        ""
    ));
    assert!(!path_targets_bucket(
        "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=1",
        "ws-bucket",
        ""
    ));
    assert!(!path_targets_bucket(
        "/zone-control/v1/storage/buckets/ws-bucket/objects/a?versionId=1",
        "ws-bucket",
        ""
    ));

    let object = "/zone-control/v1/storage/buckets/ws-bucket/objects/logs/2026/a.json";
    assert_eq!(action_for_request("HEAD", object), Some("GetObject"));
    assert!(path_targets_bucket(object, "ws-bucket", "logs/"));
    assert!(!path_targets_bucket(object, "other-bucket", "logs/"));
}

#[test]
fn ambiguous_storage_path_is_rejected() {
    assert!(!path_targets_bucket(
        "/zone-control/v1/storage/buckets/ws-bucket/objects/%2e%2e/private",
        "ws-bucket",
        ""
    ));
    assert!(!path_targets_bucket(
        "/zone-control/v1/storage/buckets/ws-bucket//objects/a",
        "ws-bucket",
        ""
    ));
}

#[test]
fn bulk_delete_does_not_inherit_version_delete() {
    assert!(contains_xml_element(
        b"<Delete><Object><Key>a</Key><VersionId>v1</VersionId></Object></Delete>",
        "versionid"
    ));
    assert!(!contains_xml_element(
        b"<Delete><Object><Key>versionid.txt</Key></Object></Delete>",
        "versionid"
    ));
    assert!(!storage_body_is_allowed("ListBucket", b"unexpected"));
    assert!(!storage_body_is_allowed("PutObjectTagging", b""));
}
