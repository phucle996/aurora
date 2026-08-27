mod create_vm;
mod delete_vm;
mod image;
mod proxmox;

pub(super) use create_vm::execute_vm_create;
pub(super) use delete_vm::execute_vm_delete;
pub(crate) use image::ImageObjectStore;
pub(crate) use image::{execute_image_delete, execute_image_import};
pub(crate) use proxmox::ProxmoxClient;

#[cfg(test)]
#[path = "../test/create_vm.rs"]
mod create_vm_tests;

#[cfg(test)]
pub(crate) use proxmox::tests::mock_provider;
