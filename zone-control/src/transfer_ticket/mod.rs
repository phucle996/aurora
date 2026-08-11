pub mod app;
pub mod config;
pub mod store;

pub mod transfer_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.zone.transfer.v1.rs"));
}

pub const TRANSFER_TICKET_SCHEMA_VERSION: u32 = 1;
