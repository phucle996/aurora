mod create_vm;
mod image;
mod proxmox;

pub(super) use create_vm::execute_vm_create;
pub(crate) use image::ImageObjectStore;
pub(crate) use image::{execute_image_delete, execute_image_import};
pub(crate) use proxmox::{ProxmoxClient, ProxmoxNode};

#[cfg(test)]
#[path = "../test/create_vm.rs"]
mod create_vm_tests;

#[cfg(test)]
#[path = "../test/proxmox.rs"]
mod proxmox_tests;
