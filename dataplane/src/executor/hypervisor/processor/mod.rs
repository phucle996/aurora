mod create_vm;
mod proxmox;

pub(super) use create_vm::execute_vm_create;
pub(crate) use proxmox::{ProxmoxClient, ProxmoxNode};

#[cfg(test)]
#[path = "../test/create_vm.rs"]
mod create_vm_tests;

#[cfg(test)]
#[path = "../test/proxmox.rs"]
mod proxmox_tests;
