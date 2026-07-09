package nats

import (
	"context"
	"controlplane/internal/config"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats.go"
)

// [COMMENT]: NewNATS khởi tạo một kết nối NATS Core Client với cơ chế TLS/mTLS và tự động thử lại khi mất kết nối.
func NewNATS(ctx context.Context, cfg *config.NATSCfg, clientName string) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.Name(clientName),
	}

	// [COMMENT]: Cấu hình TLS/mTLS nếu được kích hoạt
	if cfg.TLSEnabled {
		tlsCfg, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("nats: failed to build TLS config: %w", err)
		}
		opts = append(opts, nats.Secure(tlsCfg))
	}

	var nc *nats.Conn
	var lastErr error

	// [COMMENT]: Vòng lặp tự động retry kết nối theo cấu hình MaxRetries
	for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		nc, lastErr = nats.Connect(cfg.Addr, opts...)
		if lastErr == nil {
			return nc, nil
		}

		if attempt < cfg.MaxRetries {
			time.Sleep(cfg.RetryInterval)
		}
	}

	return nil, fmt.Errorf("nats: failed to connect after %d attempts: %w", cfg.MaxRetries, lastErr)
}

// [COMMENT]: buildTLSConfig thiết lập chứng chỉ CA và cặp khóa client cert/key phục vụ xác thực mTLS hai chiều.
func buildTLSConfig(cfg *config.NATSCfg) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// [COMMENT]: Nạp chứng chỉ CA Root tin cậy
	if cfg.CACertPath != "" {
		caCert, err := os.ReadFile(cfg.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("nats: failed to read CA cert file: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		tlsCfg.RootCAs = pool
	}

	// [COMMENT]: Nạp cặp chứng chỉ Client phục vụ bắt tay mTLS (Mutual TLS)
	if cfg.CertPath != "" && cfg.KeyPath != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("nats: failed to load client cert/key pair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}
