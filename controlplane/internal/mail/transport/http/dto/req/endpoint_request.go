package mailReq

import "github.com/google/uuid"

// CreateEndpointRequest định nghĩa cấu trúc yêu cầu tạo mới Mail Endpoint dạng phẳng (compatible với admin-ui FE).
type CreateEndpointRequest struct {
	ZoneID         uuid.UUID `json:"zone_id"` // Tùy chọn — server sẽ tự động resolve từ header X-Zone-Code nếu không truyền
	Name           string    `json:"name" binding:"required"`
	Host           string    `json:"host" binding:"required"`
	Port           int       `json:"port" binding:"required"`
	Username       string    `json:"username"`
	Password       string    `json:"password"`
	TLSMode        string    `json:"tls_mode"`
	Status         string    `json:"status"`
	MaxConnections int       `json:"max_connections"`
	Priority       int       `json:"priority"`
	Weight         int       `json:"weight"`
	CACertPEM      *string   `json:"ca_cert_pem"`
	ClientCertPEM  *string   `json:"client_cert_pem"`
	ClientKeyPEM   *string   `json:"client_key_pem"`
}

// UpdateEndpointRequest định nghĩa cấu trúc yêu cầu cập nhật Mail Endpoint dạng phẳng (compatible với admin-ui FE).
type UpdateEndpointRequest struct {
	Name           string  `json:"name" binding:"required"`
	Host           string  `json:"host" binding:"required"`
	Port           int     `json:"port" binding:"required"`
	Username       string  `json:"username"`
	Password       string  `json:"password"`
	TLSMode        string  `json:"tls_mode"`
	Status         string  `json:"status"`
	MaxConnections int     `json:"max_connections"`
	Priority       int     `json:"priority"`
	Weight         int     `json:"weight"`
	CACertPEM      *string `json:"ca_cert_pem"`
	ClientCertPEM  *string `json:"client_cert_pem"`
	ClientKeyPEM   *string `json:"client_key_pem"`
	IsActive       bool    `json:"is_active"`
}
