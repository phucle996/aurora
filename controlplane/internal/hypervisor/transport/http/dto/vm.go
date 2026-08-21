package hypervisorDTO

type CreateVMRequest struct {
	Name                string                   `json:"name"`
	ImageID             string                   `json:"image_id"`
	ResourceProfileCode string                   `json:"resource_profile_code"`
	AdditionalDisks     []CreateVMAdditionalDisk `json:"additional_disks"`
	SSHPublicKey        string                   `json:"ssh_public_key"`
}

type CreateVMAdditionalDisk struct {
	SizeGB int64 `json:"size_gb"`
}
