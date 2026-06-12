package mailReq

import "github.com/google/uuid"

// CreateEndpointRequest định nghĩa cấu trúc yêu cầu tạo mới Mail Endpoint dạng phẳng (compatible với admin-ui FE).
type CreateEndpointRequest struct {
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

// TestConnectionRequest định nghĩa cấu trúc yêu cầu kiểm tra kết nối SMTP (chỉ chứa các trường cần thiết).
type TestConnectionRequest struct {
	ZoneID        uuid.UUID `json:"zone_id" binding:"required"`
	Host          string    `json:"host" binding:"required"`
	Port          int       `json:"port" binding:"required"`
	Username      string    `json:"username" binding:"required"`
	Password      string    `json:"password" binding:"required"`
	TLSMode       string    `json:"tls_mode" binding:"required"`
	CACertPEM     *string   `json:"ca_cert_pem"`
	ClientCertPEM *string   `json:"client_cert_pem"`
	ClientKeyPEM  *string   `json:"client_key_pem"`
}
