package hypervisorDTO

type CreateVMRequest struct {
	Name                   string                   `json:"name"`
	ImageID                string                   `json:"image_id"`
	ResourcePlanID         string                   `json:"resource_plan_id"`
	ResourcePlanRevisionID string                   `json:"resource_plan_revision_id"`
	AdditionalDisks        []CreateVMAdditionalDisk `json:"additional_disks"`
	SSHPublicKey           string                   `json:"ssh_public_key"`
}

type CreateVMAdditionalDisk struct {
	SizeGB string `json:"size_gb"`
}
