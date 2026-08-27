use super::is_sdk_bucket_list;

#[test]
fn bucket_root_get_is_the_only_sdk_list_exemption() {
    for path in [
        "/personal-bucket",
        "/personal-bucket/",
        "/personal-bucket?list-type=2&prefix=folder/",
    ] {
        assert!(is_sdk_bucket_list("GET", path), "expected list: {path}");
    }

    for path in [
        "/personal-bucket/object",
        "/personal-bucket/folder/",
        "/personal-bucket/object?prefix=ignored",
        "/personal-bucket%2Fobject",
        "/personal-bucket//",
        "/",
    ] {
        assert!(
            !is_sdk_bucket_list("GET", path),
            "expected admission-gated object request: {path}"
        );
    }
    assert!(!is_sdk_bucket_list("PUT", "/personal-bucket"));
}
