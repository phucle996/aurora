// ============================================================================
// 📂 FILE: policies/otel/compiler.go - Trình Biên Dịch & Xác Thực OpenTelemetry
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Biên dịch cấu hình thô từ YAML sang định dạng trung gian có kiểm soát (Compiled).
//   - Thực thi triết lý Fail-Fast: Kiểm duyệt toàn bộ cấu hình, timeout, giới hạn batch,
//     và tệp tin chứng chỉ TLS/mTLS.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Tệp [types.go](file:///home/phucle/Desktop/New/controlplane/internal/policyengine/policies/otel/types.go) trong cùng package.
//
// 🔒 RANH GIỚI BẢO MẬT/NGHIỆP VỤ (BOUNDARY):
//   - Chỉ chấp nhận các exporter type và TLS Mode được định nghĩa trước.
//   - Bắt buộc kiểm tra sự tồn tại thực tế của tệp chứng chỉ trên đĩa bằng `os.Stat`
//     trước khi cho phép swap snapshot cấu hình, loại bỏ nguy cơ crash runtime exporter.
//
// 🔄 CALLSITE FLOW:
//   - Được gọi bởi bộ điều phối chính `runtime.compilePolicies` khi tải hoặc nạp lại cấu hình.
//
// ============================================================================

package otel

import (
	errorx "controlplane/internal/policyengine/errorx"
	"fmt"
	"os"
	"strings"
	"time"
)

// Compile phân tích cú pháp, chuyển đổi cấu hình thô và thực thi xác thực toàn bộ chính sách OpenTelemetry.
// Nếu phát hiện bất kỳ thông số nào không hợp lệ, hàm sẽ trả về lỗi `errorx.ErrPolicyInvalid`.
//
// # Tham số:
//   - `src`: Cấu trúc cấu hình thô `OTelPolicy` được đọc từ YAML.
//
// # Trả về:
//   - `CompiledPolicy`: Cấu hình OpenTelemetry đã được kiểm chứng an toàn.
//   - `error`: Lỗi nếu phát hiện giá trị bất hợp lý hoặc không tìm thấy file TLS.
func Compile(src OTelPolicy) (CompiledPolicy, error) {
	out := CompiledPolicy{
		Enabled:       src.Enabled,
		ExporterType:  strings.TrimSpace(src.ExporterType),
		Endpoint:      strings.TrimSpace(src.Endpoint),
		Insecure:      src.Insecure,
		SamplingRatio: src.SamplingRatio,
		BatchMaxSize:  src.BatchMaxSize,
		BatchMaxQueue: src.BatchMaxQueue,
	}

	// Validate FailStrategy bắt buộc phải khai báo rõ ràng, không nhận giá trị mặc định ngầm.
	strategy := strings.TrimSpace(strings.ToLower(src.FailStrategy))
	if strategy != "fail_open" && strategy != "fail_close" {
		return CompiledPolicy{}, fmt.Errorf("%w: otel: fail_strategy must be either 'fail_open' or 'fail_close' (got: '%s')", errorx.ErrPolicyInvalid, src.FailStrategy)
	}
	out.FailStrategy = strategy

	if out.Enabled {
		if out.ExporterType != "otlpgrpc" && out.ExporterType != "otlphttp" {
			return CompiledPolicy{}, fmt.Errorf("%w: otel: unsupported exporter type: %s", errorx.ErrPolicyInvalid, out.ExporterType)
		}
		if out.Endpoint == "" {
			return CompiledPolicy{}, fmt.Errorf("%w: otel: endpoint is required when enabled", errorx.ErrPolicyInvalid)
		}
		if out.SamplingRatio < 0.0 || out.SamplingRatio > 1.0 {
			return CompiledPolicy{}, fmt.Errorf("%w: otel: sampling ratio must be between 0.0 and 1.0", errorx.ErrPolicyInvalid)
		}

		exportTimeoutStr := strings.TrimSpace(src.ExportTimeout)
		if exportTimeoutStr == "" {
			exportTimeoutStr = "5s"
		}
		exportTimeout, err := time.ParseDuration(exportTimeoutStr)
		if err != nil {
			return CompiledPolicy{}, fmt.Errorf("%w: otel: invalid export timeout duration: %w", errorx.ErrPolicyInvalid, err)
		}
		out.ExportTimeout = exportTimeout

		batchTimeoutStr := strings.TrimSpace(src.BatchTimeout)
		if batchTimeoutStr == "" {
			batchTimeoutStr = "2s"
		}
		batchTimeout, err := time.ParseDuration(batchTimeoutStr)
		if err != nil {
			return CompiledPolicy{}, fmt.Errorf("%w: otel: invalid batch timeout duration: %w", errorx.ErrPolicyInvalid, err)
		}
		out.BatchTimeout = batchTimeout

		if out.BatchMaxSize <= 0 {
			return CompiledPolicy{}, fmt.Errorf("%w: otel: batch max size must be positive", errorx.ErrPolicyInvalid)
		}
		if out.BatchMaxQueue <= 0 {
			return CompiledPolicy{}, fmt.Errorf("%w: otel: batch max queue must be positive", errorx.ErrPolicyInvalid)
		}
		if out.BatchMaxQueue < out.BatchMaxSize {
			return CompiledPolicy{}, fmt.Errorf("%w: otel: batch max queue must be larger than or equal to batch max size", errorx.ErrPolicyInvalid)
		}

		out.TLS.Mode = strings.TrimSpace(strings.ToLower(src.TLS.Mode))
		if out.TLS.Mode == "" {
			out.TLS.Mode = "disable"
		}

		if out.TLS.Mode != "disable" && out.TLS.Mode != "tls" && out.TLS.Mode != "mtls" {
			return CompiledPolicy{}, fmt.Errorf("%w: otel: unsupported TLS mode: %s", errorx.ErrPolicyInvalid, out.TLS.Mode)
		}

		if out.TLS.Mode == "tls" || out.TLS.Mode == "mtls" {
			out.TLS.CACertPath = strings.TrimSpace(src.TLS.CACertPath)
			if out.TLS.CACertPath == "" {
				return CompiledPolicy{}, fmt.Errorf("%w: otel: TLS ca_cert_path is required when TLS mode is %s", errorx.ErrPolicyInvalid, out.TLS.Mode)
			}
			if _, err := os.Stat(out.TLS.CACertPath); err != nil {
				return CompiledPolicy{}, fmt.Errorf("%w: otel: ca_cert_path file not found: %w", errorx.ErrPolicyInvalid, err)
			}

			if out.TLS.Mode == "mtls" {
				out.TLS.CertPath = strings.TrimSpace(src.TLS.CertPath)
				out.TLS.KeyPath = strings.TrimSpace(src.TLS.KeyPath)

				if out.TLS.CertPath == "" || out.TLS.KeyPath == "" {
					return CompiledPolicy{}, fmt.Errorf("%w: otel: both cert_path and key_path are required for mtls mode", errorx.ErrPolicyInvalid)
				}
				if _, err := os.Stat(out.TLS.CertPath); err != nil {
					return CompiledPolicy{}, fmt.Errorf("%w: otel: cert_path file not found: %w", errorx.ErrPolicyInvalid, err)
				}
				if _, err := os.Stat(out.TLS.KeyPath); err != nil {
					return CompiledPolicy{}, fmt.Errorf("%w: otel: key_path file not found: %w", errorx.ErrPolicyInvalid, err)
				}
			} else {
				out.TLS.CertPath = ""
				out.TLS.KeyPath = ""
			}
		} else {
			out.TLS.CACertPath = ""
			out.TLS.CertPath = ""
			out.TLS.KeyPath = ""
		}
	} else {
		out.SamplingRatio = 1.0
		out.ExportTimeout = 5 * time.Second
		out.BatchTimeout = 2 * time.Second
		out.BatchMaxSize = 512
		out.BatchMaxQueue = 2048
	}

	return out, nil
}
