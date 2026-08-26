use super::{runtime_component_token, zone_code_token};

#[test]
fn zone_code_is_a_single_dns_label() {
    for value in ["z1", "vn-south-1", "a"] {
        assert!(zone_code_token(value), "rejected {value}");
    }
    for value in ["", "-z1", "z1-", "Z1", "z1.aurora.local"] {
        assert!(!zone_code_token(value), "accepted {value}");
    }
}

#[test]
fn runtime_component_is_bounded_and_path_safe() {
    assert!(runtime_component_token("nic-0.rx"));
    assert!(!runtime_component_token(""));
    assert!(!runtime_component_token("../../secret"));
    assert!(!runtime_component_token(&"x".repeat(129)));
}
