package nats

// NatsPolicy đại diện cho cấu hình động của NATS trong file policy.yaml.
type NatsPolicy struct {
	TLS NatsTLSPolicy `yaml:"tls"`
}

// NatsTLSPolicy định nghĩa các đường dẫn chứng chỉ mTLS có thể cập nhật nóng.
type NatsTLSPolicy struct {
	CACertPath string `yaml:"ca_cert_path"`
	CertPath   string `yaml:"cert_path"`
	KeyPath    string `yaml:"key_path"`
}

// CompiledPolicy chứa thông số NATS đã được kiểm duyệt an toàn ở runtime.
type CompiledPolicy struct {
	TLS CompiledTLSPolicy
}

// CompiledTLSPolicy chứa các đường dẫn chứng chỉ đã xác thực sự tồn tại trên đĩa.
type CompiledTLSPolicy struct {
	CACertPath string
	CertPath   string
	KeyPath    string
}
