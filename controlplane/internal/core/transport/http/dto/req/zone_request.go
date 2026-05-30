package coreReq

type CreateZoneRequest struct {
	Code             string `json:"code"              binding:"required"`
	Name             string `json:"name"              binding:"required"`
	Location         string `json:"location"`
	EnableHypervisor bool   `json:"enable_hypervisor" binding:"required"`
	EnableStorage    bool   `json:"enable_storage"    binding:"required"`
	EnableMail       bool   `json:"enable_mail"       binding:"required"`
	EnableK8s        bool   `json:"enable_k8s"        binding:"required"`
	EnableAI         bool   `json:"enable_ai"         binding:"required"`
}

type UpdateZoneStatusRequest struct {
	Status string `json:"status"`
}

type UpsertZoneServiceRequest struct {
	ServiceType string `json:"service_type"`
	Enabled     bool   `json:"enabled"`
}
