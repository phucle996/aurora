use sha2::{Digest, Sha256};

pub fn sha256_hex(value: &[u8]) -> String {
    format!("{:x}", Sha256::digest(value))
}

pub fn storage_action(method: &str, path: &str) -> Option<&'static str> {
    let path_only = path.split('?').next().unwrap_or(path);
    let resource_route = path_only
        .strip_prefix("/zone-control/v1/storage/buckets/")?
        .split_once('/')?;
    if resource_route.0.is_empty() {
        return None;
    }
    let route = resource_route.1;
    if route
        .strip_prefix("objects/")
        .and_then(|object| object.strip_suffix("/tags"))
        .is_some_and(|object| !object.is_empty())
    {
        return match method {
            "GET" => Some("GetObjectTagging"),
            "PUT" => Some("PutObjectTagging"),
            _ => None,
        };
    }
    if route == "bulk-delete" {
        return (method == "POST").then_some("DeleteObject");
    }
    if route == "presign-upload" {
        return (method == "POST").then_some("PutObject");
    }
    if route == "presign-download" {
        return (method == "POST").then_some("GetObject");
    }
    if method == "GET" && route == "objects" {
        return Some("ListBucket");
    }
    if method == "HEAD"
        && route
            .strip_prefix("objects/")
            .is_some_and(|object| !object.is_empty())
    {
        return Some("GetObject");
    }
    None
}

pub fn path_targets_storage_resource(path: &str, bucket_name: &str, key_prefix: &str) -> bool {
    let path_only = path.split('?').next().unwrap_or(path);
    let has_query = path.contains('?');
    let lowered = path_only.to_ascii_lowercase();
    if lowered.contains("%2f")
        || lowered.contains("%5c")
        || lowered.contains("%2e")
        || lowered.contains("%25")
        || path_only.contains('\\')
        || path_only.contains("//")
        || path_only
            .split('/')
            .any(|segment| segment == "." || segment == "..")
    {
        return false;
    }
    let expected = format!("/zone-control/v1/storage/buckets/{bucket_name}");
    if !path_only.starts_with(&expected) {
        return false;
    }
    if path_only.len() > expected.len() && path_only.as_bytes()[expected.len()] != b'/' {
        return false;
    }
    // Only the ListObjectsV2 route has reviewed query semantics. Version IDs
    // and S3 subresources need distinct actions before they can be exposed.
    if has_query && !path_only.ends_with("/objects") {
        return false;
    }
    if key_prefix.is_empty() {
        return !path_only.ends_with("/objects") || list_query_is_allowed(path, key_prefix);
    }
    if let Some((_, key)) = path_only.split_once("/objects/") {
        return key.starts_with(key_prefix);
    }
    path_only.ends_with("/objects") && list_query_is_allowed(path, key_prefix)
}

pub fn storage_body_is_allowed(action: &str, body: &[u8]) -> bool {
    match action {
        "ListBucket" | "GetObject" | "GetObjectTagging" => body.is_empty(),
        "DeleteObject" => !body.is_empty() && !contains_xml_element(body, "versionid"),
        "PutObjectTagging" => !body.is_empty() && std::str::from_utf8(body).is_ok(),
        // Presign request schemas are bounded and signed but are implemented
        // by a separate capability handler rather than forwarded as S3 bytes.
        "PutObject" => true,
        _ => false,
    }
}

fn contains_xml_element(body: &[u8], forbidden_local_name: &str) -> bool {
    let Ok(xml) = std::str::from_utf8(body) else {
        return true;
    };
    let lowercase = xml.to_ascii_lowercase();
    let mut remaining = lowercase.as_str();
    while let Some(start) = remaining.find('<') {
        remaining = &remaining[start + 1..];
        let Some(end) = remaining.find('>') else {
            return false;
        };
        let token = remaining[..end].trim_start_matches('/').trim();
        let qualified_name = token
            .split_ascii_whitespace()
            .next()
            .unwrap_or("")
            .trim_end_matches('/');
        if qualified_name
            .rsplit(':')
            .next()
            .is_some_and(|name| name == forbidden_local_name)
        {
            return true;
        }
        remaining = &remaining[end + 1..];
    }
    false
}

fn list_query_is_allowed(path: &str, key_prefix: &str) -> bool {
    let Some((_, query)) = path.split_once('?') else {
        return false;
    };
    if !has_valid_percent_encoding(query) {
        return false;
    }
    let mut list_type = false;
    let mut prefix = false;
    let mut delimiter = false;
    let mut max_keys = false;
    let mut continuation = false;
    let mut start_after = false;
    let mut encoding = false;
    let mut fetch_owner = false;
    for (name, value) in url::form_urlencoded::parse(query.as_bytes()) {
        // Lossy UTF-8 decoding must not silently turn invalid bytes into a
        // query accepted here but interpreted differently by S3.
        if name.contains('\u{fffd}') || value.contains('\u{fffd}') {
            return false;
        }
        match name.as_ref() {
            "list-type" if !list_type && value == "2" => list_type = true,
            "prefix"
                if !prefix
                    && value.len() <= 1_024
                    && (key_prefix.is_empty() || value.starts_with(key_prefix)) =>
            {
                prefix = true
            }
            "delimiter" if !delimiter && value.len() <= 1_024 => delimiter = true,
            "max-keys"
                if !max_keys
                    && value
                        .parse::<u16>()
                        .is_ok_and(|count| (1..=1_000).contains(&count)) =>
            {
                max_keys = true
            }
            "continuation-token" if !continuation && !value.is_empty() && value.len() <= 4_096 => {
                continuation = true
            }
            "start-after"
                if !start_after
                    && value.len() <= 1_024
                    && (key_prefix.is_empty() || value.starts_with(key_prefix)) =>
            {
                start_after = true
            }
            "encoding-type" if !encoding && value == "url" => encoding = true,
            "fetch-owner" if !fetch_owner && value == "false" => fetch_owner = true,
            _ => return false,
        }
    }
    list_type && (key_prefix.is_empty() || prefix) && !(continuation && start_after)
}

fn has_valid_percent_encoding(value: &str) -> bool {
    let bytes = value.as_bytes();
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'%' {
            if index + 2 >= bytes.len()
                || !bytes[index + 1].is_ascii_hexdigit()
                || !bytes[index + 2].is_ascii_hexdigit()
            {
                return false;
            }
            index += 3;
        } else {
            index += 1;
        }
    }
    true
}

#[cfg(test)]
mod tests {
    use super::{path_targets_storage_resource, storage_action, storage_body_is_allowed};

    #[test]
    fn storage_path_is_bound_to_bucket_and_prefix() {
        assert!(path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/ws-bucket/objects/logs/a.txt",
            "ws-bucket",
            "logs/"
        ));
        assert!(!path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/other/objects/logs/a.txt",
            "ws-bucket",
            "logs/"
        ));
        assert!(!path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/ws-bucket/objects/%2e%2e/private",
            "ws-bucket",
            ""
        ));
        assert!(path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=2&prefix=logs%2F2026%2F",
            "ws-bucket",
            "logs/"
        ));
        assert!(!path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=2",
            "ws-bucket",
            "logs/"
        ));
        assert!(!path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?prefix=logs%2F&prefix=",
            "ws-bucket",
            "logs/"
        ));
        assert!(!path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?prefix=logs%ZZ",
            "ws-bucket",
            "logs/"
        ));
        assert!(!path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?acl&list-type=2",
            "ws-bucket",
            ""
        ));
        assert!(!path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=1",
            "ws-bucket",
            ""
        ));
    }

    #[test]
    fn storage_action_is_bound_to_method_and_route() {
        let list = "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=2";
        assert_eq!(storage_action("GET", list), Some("ListBucket"));
        assert_eq!(storage_action("POST", list), None);
        assert_eq!(
            storage_action(
                "GET",
                "/zone-control/v1/storage/buckets/ws-bucket/unreviewed/objects?list-type=2"
            ),
            None
        );
        assert_eq!(
            storage_action(
                "PUT",
                "/zone-control/v1/storage/buckets/ws-bucket/objects/a/tags"
            ),
            Some("PutObjectTagging")
        );
    }

    #[test]
    fn storage_queries_and_version_delete_are_fail_closed() {
        assert!(!path_targets_storage_resource(
            "/zone-control/v1/storage/buckets/ws-bucket/objects/a?versionId=1",
            "ws-bucket",
            ""
        ));
        assert!(!storage_body_is_allowed(
            "DeleteObject",
            b"<Delete><Object><Key>a</Key><VersionId>v1</VersionId></Object></Delete>"
        ));
        assert!(storage_body_is_allowed(
            "DeleteObject",
            b"<Delete><Object><Key>versionid.txt</Key></Object></Delete>"
        ));
        assert!(!storage_body_is_allowed("ListBucket", b"unexpected"));
        assert!(!storage_body_is_allowed("PutObjectTagging", b""));
    }
}
