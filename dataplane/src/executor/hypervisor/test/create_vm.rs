use super::create_vm::canonical_vm_config_hash;
use crate::executor::hypervisor::hypervisor_proto::VmCreateV1;

#[test]
fn canonical_config_hash_matches_the_controlplane_contract() {
    let command = VmCreateV1 {
        schema_version: 1,
        vm_id: Vec::new(),
        provider_name: String::new(),
        image: "ubuntu-24.04".to_string(),
        cpu_cores: 2,
        memory_mb: 4096,
        disk_gb: 32,
        ssh_public_key: "ssh-ed25519 AAAA".to_string(),
        config_hash: Vec::new(),
    };
    let encoded = canonical_vm_config_hash(&command)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    assert_eq!(
        encoded,
        "590305e12d66bb14022d10bbd6f404ca04ea71416f73ea5d37d160a94f20f3aa"
    );
}

#[test]
fn immutable_vm_spec_fields_change_the_config_hash() {
    let mut command = VmCreateV1 {
        schema_version: 1,
        vm_id: Vec::new(),
        provider_name: String::new(),
        image: "debian-12".to_string(),
        cpu_cores: 4,
        memory_mb: 8192,
        disk_gb: 64,
        ssh_public_key: "ssh-ed25519 BBBB".to_string(),
        config_hash: Vec::new(),
    };
    let original = canonical_vm_config_hash(&command);
    command.disk_gb += 1;
    assert_ne!(canonical_vm_config_hash(&command), original);
}
