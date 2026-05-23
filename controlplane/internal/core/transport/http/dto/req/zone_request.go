package coreReq

type CreateZoneRequest struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type UpdateZoneStatusRequest struct {
	Status string `json:"status"`
}

type UpsertZoneServiceRequest struct {
	ServiceType string `json:"service_type"`
	Enabled     bool   `json:"enabled"`
}
