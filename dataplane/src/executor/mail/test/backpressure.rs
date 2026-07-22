use super::MailBackpressureSnapshot;

#[test]
fn unavailable_transport_has_no_capacity() {
    let snapshot = MailBackpressureSnapshot::calculate(false, false, 0, 100);
    assert_eq!(snapshot.status, "down");
    assert_eq!(snapshot.capacity, 0);
}

#[test]
fn queue_pressure_is_bounded_and_degrades_near_capacity() {
    let degraded = MailBackpressureSnapshot::calculate(false, true, 95, 100);
    assert_eq!(degraded.status, "degraded");
    assert_eq!(degraded.capacity, 5);

    let saturated = MailBackpressureSnapshot::calculate(false, true, 200, 100);
    assert_eq!(saturated.status, "degraded");
    assert_eq!(saturated.capacity, 0);
}
