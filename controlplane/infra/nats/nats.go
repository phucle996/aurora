package nats

import (
	"context"
	"controlplane/internal/config"
	"controlplane/pkg/logger"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

var (
	certPathMu     sync.RWMutex
	activeCertPath string
	activeKeyPath  string
)

// UpdateDynamicCerts cập nhật động các đường dẫn cert/key cho kết nối mTLS.
func UpdateDynamicCerts(certPath, keyPath string) {
	certPathMu.Lock()
	defer certPathMu.Unlock()
	activeCertPath = certPath
	activeKeyPath = keyPath
	logger.SysInfoFields("NATS_MTLS_ROTATION", "updated active dynamic mTLS cert paths", logger.Fields{
		"cert_path": certPath,
		"key_path":  keyPath,
	})
}

// NewNatsConn khởi tạo kết nối HA đến hạ tầng NATS Server có hỗ trợ auto-reconnect, TLS và retries lúc startup.
func NewNatsConn(ctx context.Context, cfg *config.Config) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.Name(cfg.App.AppName),
		nats.PingInterval(cfg.Nats.PingInterval),
		nats.MaxPingsOutstanding(cfg.Nats.MaxPingOut),
		nats.MaxReconnects(-1), // Reconnect vô hạn trong môi trường HA
		nats.ReconnectWait(cfg.Nats.RetryInterval),

		// Handlers theo dõi trạng thái kết nối
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if err != nil {
				logger.SysWarn("nats.infra", fmt.Sprintf("NATS disconnected: %v", err))
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.SysInfoFields("nats.infra", "NATS reconnected successfully", logger.Fields{
				"connected_url": nc.ConnectedUrl(),
			})
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			logger.SysWarn("nats.infra", "NATS connection is closed permanently")
		}),
		nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
			logger.SysErrorFields("nats.infra", "NATS subscription error", err, logger.Fields{
				"subject": sub.Subject,
			})
		}),
	}

	if cfg.Nats.TLSEnabled {
		tlsCfg, err := buildTLSConfig(&cfg.Nats)
		if err != nil {
			return nil, fmt.Errorf("nats: failed to build TLS config: %w", err)
		}
		opts = append(opts, nats.Secure(tlsCfg))
	}

	var nc *nats.Conn
	var lastErr error

	// Vòng lặp kết nối khởi động có retry (Cold start resilience)
	for attempt := 1; attempt <= cfg.Nats.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		nc, lastErr = nats.Connect(cfg.Nats.URL, opts...)
		if lastErr == nil {
			logger.SysInfoFields("nats.infra", "NATS connected successfully", logger.Fields{
				"url": cfg.Nats.URL,
			})
			return nc, nil
		}

		logger.SysWarn("nats.infra", fmt.Sprintf("NATS connection attempt %d/%d failed: %v", attempt, cfg.Nats.MaxRetries, lastErr))

		if attempt < cfg.Nats.MaxRetries {
			time.Sleep(cfg.Nats.RetryInterval)
		}
	}

	return nil, fmt.Errorf("nats: failed to connect after %d attempts: %w", cfg.Nats.MaxRetries, lastErr)
}

func buildTLSConfig(cfg *config.NatsCfg) (*tls.Config, error) {
	tlsCfg := &tls.Config{}

	// Trích xuất hostname từ NATS URL để khớp SNI
	host := cfg.URL
	if strings.Contains(host, "://") {
		parts := strings.Split(host, "://")
		if len(parts) > 1 {
			host = parts[1]
		}
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	tlsCfg.ServerName = strings.TrimSpace(host)

	if cfg.CACertPath != "" {
		caCert, err := os.ReadFile(cfg.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("nats: failed to read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		tlsCfg.RootCAs = pool
	}

	if (cfg.CertPath != "" && cfg.KeyPath != "") || true {
		// Nạp chứng chỉ ban đầu nếu có để khởi động nhanh
		if cfg.CertPath != "" && cfg.KeyPath != "" {
			cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
			if err == nil {
				tlsCfg.Certificates = []tls.Certificate{cert}
			}
		}

		// Sử dụng GetClientCertificate để hỗ trợ xoay vòng nóng mTLS mà không cần khởi động lại dịch vụ
		tlsCfg.GetClientCertificate = func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			certPathMu.RLock()
			cPath := activeCertPath
			kPath := activeKeyPath
			certPathMu.RUnlock()

			if cPath == "" || kPath == "" {
				cPath = cfg.CertPath
				kPath = cfg.KeyPath
			}

			if cPath == "" || kPath == "" {
				return nil, fmt.Errorf("nats: no client cert paths configured for mTLS handshake")
			}

			cert, err := tls.LoadX509KeyPair(cPath, kPath)
			if err != nil {
				return nil, fmt.Errorf("nats: failed to load client cert dynamically: %w", err)
			}
			return &cert, nil
		}
	}

	return tlsCfg, nil
}
