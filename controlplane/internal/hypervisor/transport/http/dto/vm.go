package hypervisorDTO

type CreateVMRequest struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	CPUCores     int32  `json:"cpu_cores"`
	MemoryMB     int64  `json:"memory_mb"`
	DiskGB       int64  `json:"disk_gb"`
	SSHPublicKey string `json:"ssh_public_key"`
}
