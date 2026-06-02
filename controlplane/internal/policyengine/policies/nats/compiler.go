package nats

import (
	errorx "controlplane/internal/policyengine/errorx"
	"fmt"
	"os"
	"strings"
)

// Compile kiểm tra tính hợp lệ và sự tồn tại thực tế của các tệp chứng chỉ mTLS.
func Compile(src NatsPolicy) (CompiledPolicy, error) {
	out := CompiledPolicy{}

	caPath := strings.TrimSpace(src.TLS.CACertPath)
	certPath := strings.TrimSpace(src.TLS.CertPath)
	keyPath := strings.TrimSpace(src.TLS.KeyPath)

	if caPath != "" {
		if _, err := os.Stat(caPath); err != nil {
			return CompiledPolicy{}, fmt.Errorf("%w: nats: ca_cert_path file not found: %w", errorx.ErrPolicyInvalid, err)
		}
		out.TLS.CACertPath = caPath
	}

	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return CompiledPolicy{}, fmt.Errorf("%w: nats: both cert_path and key_path are required for mTLS", errorx.ErrPolicyInvalid)
		}
		if _, err := os.Stat(certPath); err != nil {
			return CompiledPolicy{}, fmt.Errorf("%w: nats: cert_path file not found: %w", errorx.ErrPolicyInvalid, err)
		}
		if _, err := os.Stat(keyPath); err != nil {
			return CompiledPolicy{}, fmt.Errorf("%w: nats: key_path file not found: %w", errorx.ErrPolicyInvalid, err)
		}
		out.TLS.CertPath = certPath
		out.TLS.KeyPath = keyPath
	}

	return out, nil
}
