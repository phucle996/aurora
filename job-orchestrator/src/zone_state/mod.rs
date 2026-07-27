pub mod metadata;
mod policy;
mod processor;
pub mod store;
pub mod watchdog;
pub mod worker;

pub mod proto {
    include!(concat!(env!("OUT_DIR"), "/zone.rs"));
}
