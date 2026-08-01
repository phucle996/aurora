pub mod bootstrap;
mod connection;
// Keep CDC lifecycle, durable dispatch, quarantine and zone projection isolated:
// the module boundary makes it harder for a result/zone path to gain access to
// the Managed Service outbox writer by accident.
mod dispatch;
mod pgoutput;
mod quarantine;
mod worker;
mod zone;

pub use worker::ChangefeedWorker;
