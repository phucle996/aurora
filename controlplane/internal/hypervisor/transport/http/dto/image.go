package hypervisorDTO

type RegisterImageMetadataRequest struct {
	Name         string `json:"name"`
	Code         string `json:"code"`
	Distribution string `json:"distribution"`
	Release      string `json:"release"`
	Revision     int64  `json:"revision"`
	Architecture string `json:"architecture"`
	Format       string `json:"format"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
}
