use sha2::{Digest, Sha256};

pub fn sha256_hex(value: &[u8]) -> String {
    format!("{:x}", Sha256::digest(value))
}

pub fn path_targets_bucket(path: &str, bucket_name: &str, key_prefix: &str) -> bool {
    let path_only = path.split('?').next().unwrap_or(path);
    let lowered = path_only.to_ascii_lowercase();
    if lowered.contains("%2f")
        || lowered.contains("%5c")
        || lowered.contains("%2e")
        || path_only.contains("//")
        || path_only.split('/').any(|segment| segment == "..")
    {
        return false;
    }
    let expected = format!("/storage/v1/buckets/{bucket_name}");
    if !path_only.starts_with(&expected) {
        return false;
    }
    if path_only.len() > expected.len() && path_only.as_bytes()[expected.len()] != b'/' {
        return false;
    }
    if key_prefix.is_empty() {
        return true;
    }
    path_only
        .split_once("/objects/")
        .map(|(_, key)| key.starts_with(key_prefix))
        .unwrap_or(path_only.ends_with("/objects"))
}
