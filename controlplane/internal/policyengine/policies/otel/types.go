// ============================================================================
// 📂 FILE: policies/otel/types.go - Định Nghĩa Mô Hình Cấu Hình OpenTelemetry
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Định nghĩa schema cấu hình thô YAML (raw) và cấu trúc biên dịch Compiled (runtime)
//     cho hệ thống giám sát phân tán OpenTelemetry (OTel Tracing Infrastructure).
//   - Đảm bảo ánh xạ chính xác các thông số kết nối gRPC/HTTP exporter, timeouts,
//     lấy mẫu (sampling), và các cơ chế bảo mật (TLS/mTLS).
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Trường `policies.otel` trong tệp cấu hình động `runtime/policies/policy.yaml`.
//
// 🔒 RANH GIỚI BẢO MẬT/NGHIỆP VỤ (BOUNDARY):
//   - Định đoạt mức độ hiển thị (observability depth) của toàn bộ request đi qua hệ thống.
//   - Đảm bảo an toàn thông tin vận hành khi gửi dữ liệu tracing sang Collector bên thứ ba
//     thông qua các chế độ bảo mật truyền dẫn TLS/mTLS.
//
// 🔄 CALLSITE FLOW:
//   - Bộ phân tích cú pháp YAML nạp vào struct `OTelPolicy`.
//   - Hệ thống observability toàn cục nạp struct `CompiledPolicy` từ `RuntimePolicies`
//     để thiết lập/cập nhật động `TracerProvider`.
//
// ============================================================================

package otel

import "time"

// OTelPolicy đại diện cho cấu trúc thô (raw) được ánh xạ trực tiếp từ file YAML.
type OTelPolicy struct {
	Enabled       bool          `yaml:"enabled"`
	ExporterType  string        `yaml:"exporter_type"`
	Endpoint      string        `yaml:"endpoint"`
	Insecure      bool          `yaml:"insecure"`
	SamplingRatio float64       `yaml:"sampling_ratio"`
	ExportTimeout string        `yaml:"export_timeout"`
	BatchTimeout  string        `yaml:"batch_timeout"`
	BatchMaxSize  int           `yaml:"batch_max_size"`
	BatchMaxQueue int           `yaml:"batch_max_queue"`
	TLS           OTelTLSPolicy `yaml:"tls"`
}

// OTelTLSPolicy chứa cấu hình bảo mật TLS/mTLS cho exporter.
type OTelTLSPolicy struct {
	Mode       string `yaml:"mode"`
	CACertPath string `yaml:"ca_cert_path"`
	CertPath   string `yaml:"cert_path"`
	KeyPath    string `yaml:"key_path"`
}

// ----------------------------------------------------------------------------
// Compiled Runtime Types
// ----------------------------------------------------------------------------

// CompiledPolicy đại diện cho cấu hình OTel đã được xác thực an toàn ở runtime.
type CompiledPolicy struct {
	Enabled       bool
	ExporterType  string
	Endpoint      string
	Insecure      bool
	SamplingRatio float64
	ExportTimeout time.Duration
	BatchTimeout  time.Duration
	BatchMaxSize  int
	BatchMaxQueue int
	TLS           CompiledTLSPolicy
}

// CompiledTLSPolicy chứa các tham số TLS đã được validate đường dẫn tệp tin thành công.
type CompiledTLSPolicy struct {
	Mode       string
	CACertPath string
	CertPath   string
	KeyPath    string
}
