use super::is_user_visible_zone_status;

#[test]
fn user_catalog_only_exposes_active_or_draining_zones() {
    assert!(is_user_visible_zone_status("active"));
    assert!(is_user_visible_zone_status("draining"));

    for hidden_status in ["planned", "maintenance", "disabled", "inactive", ""] {
        assert!(
            !is_user_visible_zone_status(hidden_status),
            "{hidden_status} must not be exposed in the user catalog"
        );
    }
}
