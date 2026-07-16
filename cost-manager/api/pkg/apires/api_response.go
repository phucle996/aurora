package apires

// APIResponse là cấu trúc JSON chuẩn cho mọi HTTP response của Cost Manager API
type APIResponse struct {
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}
