package mailReq

import "github.com/google/uuid"

type CreateEndpointRequest struct {
	ZoneID           uuid.UUID              `json:"zone_id" binding:"required"`
	Name             string                 `json:"name" binding:"required"`
	Provider         string                 `json:"provider" binding:"required,oneof=smtp sendgrid mailgun"`
	ConnectionConfig map[string]interface{} `json:"connection_config" binding:"required"`
}

type UpdateEndpointRequest struct {
	Name             string                 `json:"name" binding:"required"`
	ConnectionConfig map[string]interface{} `json:"connection_config" binding:"required"`
	IsActive         bool                   `json:"is_active"`
}
