pub mod pricing_event_proto {
    include!(concat!(env!("OUT_DIR"), "/billing.pricing.v1.rs"));
}

#[allow(dead_code)]
pub mod storage_usage_report_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.storage.metering.v1.rs"));
}

pub mod lock;
pub mod runtime;
pub mod snapshot;

#[cfg(test)]
#[path = "../../tests/unit/pricing.rs"]
mod tests;

pub use lock::{acquire_billing_lease, release_billing_lease};
pub use runtime::{PricingRuntime, run_pricing_listener};
pub use snapshot::BillingPricingLease;
