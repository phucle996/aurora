use super::{parse_tenant_domain, parse_tenant_id};

#[test]
fn tenant_switch_query_is_percent_decoded_by_name() {
    let path = "/api/v1/tenant/go-to-tenant?ignored=1&tenant_domain=acme.example&tenant_id=10000000-0000-4000-8000-000000000001";
    assert_eq!(parse_tenant_domain(path).as_deref(), Some("acme.example"));
    assert_eq!(
        parse_tenant_id(path).as_deref(),
        Some("10000000-0000-4000-8000-000000000001")
    );
}

#[test]
fn tenant_switch_query_does_not_accept_prefix_collisions() {
    let path = "/api/v1/tenant/go-to-tenant?tenant_domain_suffix=evil&tenant_id_suffix=bad";
    assert_eq!(parse_tenant_domain(path), None);
    assert_eq!(parse_tenant_id(path), None);
}
