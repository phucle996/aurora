use super::create_vm::canonical_vm_config_hash;
use crate::executor::hypervisor::hypervisor_proto::VmCreateV1;

#[test]
fn canonical_config_hash_matches_the_controlplane_contract() {
    let command = VmCreateV1 {
        schema_version: 1,
        vm_id: Vec::new(),
        provider_name: String::new(),
        image_id: vec![1; 16],
        cpu_cores: 2,
        memory_mb: 4096,
        disk_gb: 32,
        ssh_public_key: "ssh-ed25519 AAAA".to_string(),
        config_hash: Vec::new(),
        image_revision: 1,
        image_sha256: vec![2; 32],
        provider_template_vmid: 9001,
    };
    let encoded = canonical_vm_config_hash(&command)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    assert_eq!(
        encoded,
        "1c10b8d02b9276f8777195b03061cc7e61d4bf3f9e1540c25b0d4b22ab9920d0"
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
        disk_gb: 64,
        ssh_public_key: "ssh-ed25519 BBBB".to_string(),
        config_hash: Vec::new(),
        image_revision: 1,
        image_sha256: vec![4; 32],
        provider_template_vmid: 9002,
    };
    let original = canonical_vm_config_hash(&command);
    command.disk_gb += 1;
    assert_ne!(canonical_vm_config_hash(&command), original);
}
