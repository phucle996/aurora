use super::proxmox::ProxmoxVmConfig;

#[test]
fn proxmox_vm_config_decodes_supported_boot_disk_fields() {
    let config: ProxmoxVmConfig = serde_json::from_str(
        r#"{
            "description": "Managed by Aurora",
            "bootdisk": "virtio0",
            "virtio0": "local-lvm:vm-100-disk-0,size=32G"
        }"#,
    )
    .expect("valid Proxmox config fixture");
    assert_eq!(config.bootdisk.as_deref(), Some("virtio0"));
    assert_eq!(
        config.virtio0.as_deref(),
        Some("local-lvm:vm-100-disk-0,size=32G")
    );
}
