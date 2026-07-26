use super::provider_binding::provider_vmid_candidate;

#[test]
fn provider_vmid_candidates_are_deterministic_and_bounded() {
    let resource_id =
        uuid::Uuid::parse_str("019f9ab8-7a4d-7e58-9b48-270f3bf91371").expect("valid fixture UUID");
    let first = provider_vmid_candidate(resource_id, 0);
    assert_eq!(first, provider_vmid_candidate(resource_id, 0));
    assert!((100..=999_999_999).contains(&first));
}

#[test]
fn collision_probe_advances_without_leaving_the_vmid_range() {
    let resource_id =
        uuid::Uuid::parse_str("019f9ab8-7a4d-7e58-9b48-270f3bf91371").expect("valid fixture UUID");
    let first = provider_vmid_candidate(resource_id, 0);
    let second = provider_vmid_candidate(resource_id, 1);
    assert_ne!(first, second);
    assert!((100..=999_999_999).contains(&second));
}
