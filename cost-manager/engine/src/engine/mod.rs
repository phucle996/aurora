#![allow(unused_imports)]

pub mod pricing_event_proto {
    include!(concat!(env!("OUT_DIR"), "/billing.pricing.v1.rs"));
}

pub mod snapshot;
pub mod runtime;
pub mod lock;
pub mod runner;

#[cfg(test)]
mod tests;

pub use snapshot::{
    BillingPricingLease, CatalogSnapshot, PricingError, ServiceType, TierPricingSnapshot, TierRange,
};
pub use runtime::{run_pricing_listener, PricingRuntime};
pub use lock::{acquire_billing_lease, release_billing_lease, RedisBillingLease};
pub use runner::{run_billing_task, BillingTask, wait_for_next_cycle};
