use super::{detect_route_group, RouteGroup};

#[test]
fn payment_webhook_uses_bounded_public_rate_group() {
    assert_eq!(
        detect_route_group("/api/v1/billing/webhooks/personal/payment-settled"),
        RouteGroup::PaymentWebhook
    );
    assert_eq!(
        detect_route_group("/api/v1/billing/webhooks/tenant/payment-settled?attempt=2"),
        RouteGroup::PaymentWebhook
    );
    assert_eq!(
        detect_route_group("/api/v1/billing/webhooks/other"),
        RouteGroup::Billing
    );
}

#[test]
fn zone_control_uses_an_isolated_rate_group() {
    assert_eq!(
        detect_route_group("/zone-control/v1/storage/buckets/a/objects?list-type=2"),
        RouteGroup::ZoneControl
    );
}

#[test]
fn me_critical_route_uses_user_critical_budget() {
    assert_eq!(
        detect_route_group("/api/v1/me/critical/hierarchy/tenant-invitations/join"),
        RouteGroup::UserCritical
    );
}
