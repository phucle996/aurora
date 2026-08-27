use super::create_vm::canonical_vm_config_hash;
use crate::executor::hypervisor::hypervisor_proto::{VmCreateAdditionalDiskV1, VmCreateV1};

#[test]
fn canonical_config_hash_matches_the_controlplane_contract() {
    let command = VmCreateV1 {
        schema_version: 1,
        vm_id: Vec::new(),
        provider_name: String::new(),
        image_id: vec![1; 16],
        cpu_cores: 2,
        memory_mb: 4096,
        disk_gb: 64,
        ssh_public_key: "ssh-ed25519 AAAA".to_string(),
        config_hash: Vec::new(),
        image_revision: 1,
        image_sha256: vec![2; 32],
        provider_template_vmid: 9001,
        additional_disks: Vec::new(),
        resource_plan_id: vec![5; 16],
        resource_plan_revision_id: vec![6; 16],
        resource_plan_revision_number: 1,
        resource_plan_content_sha256: vec![7; 32],
    };
    let encoded = canonical_vm_config_hash(&command)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    assert_eq!(
        encoded,
        "8f35b64e80421315a2deab3a351845ed42e323bac39b647e20a9947555c2a45c"
    );
}

#[test]
fn immutable_vm_spec_fields_change_the_config_hash() {
    let mut command = VmCreateV1 {
        schema_version: 1,
        vm_id: Vec::new(),
        provider_name: String::new(),
        image_id: vec![3; 16],
        cpu_cores: 4,
        memory_mb: 8192,
        disk_gb: 128,
        ssh_public_key: "ssh-ed25519 BBBB".to_string(),
        config_hash: Vec::new(),
        image_revision: 1,
        image_sha256: vec![4; 32],
        provider_template_vmid: 9002,
        additional_disks: Vec::new(),
        resource_plan_id: vec![5; 16],
        resource_plan_revision_id: vec![6; 16],
        resource_plan_revision_number: 2,
        resource_plan_content_sha256: vec![7; 32],
    };
    let original = canonical_vm_config_hash(&command);
    command.additional_disks.push(VmCreateAdditionalDiskV1 {
        disk_index: 1,
        size_gb: 32,
    });
    assert_ne!(canonical_vm_config_hash(&command), original);
}
