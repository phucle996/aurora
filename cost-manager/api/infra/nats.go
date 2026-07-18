package infra

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"cost-manager/api/internal/config"
	"cost-manager/api/pkg/logger"
	"github.com/nats-io/nats.go"
)

func ConnectNats(cfg *config.NATSCfg) (*nats.Conn, error) {
	const op = "infra.nats.connect"
	if cfg == nil {
		return nil, fmt.Errorf("nats config is required")
	}
	opts := []nats.Option{nats.Name("Cost Manager API")}
	if cfg.TLSEnabled {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.CACertPath != "" {
			pem, err := os.ReadFile(cfg.CACertPath)
			if err != nil {
				return nil, fmt.Errorf("read NATS CA: %w", err)
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("parse NATS CA: no certificate found")
			}
			tlsConfig.RootCAs = roots
		}
		if (cfg.CertPath == "") != (cfg.KeyPath == "") {
			return nil, fmt.Errorf("NATS client certificate and key must be configured together")
		}
		if cfg.CertPath != "" {
			certificate, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
			if err != nil {
				return nil, fmt.Errorf("load NATS mTLS certificate: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{certificate}
		}
		opts = append(opts, nats.Secure(tlsConfig))
	}

	retryDelay, err := time.ParseDuration(cfg.RetryInterval)
	if err != nil || retryDelay <= 0 {
		return nil, fmt.Errorf("invalid NATS retry interval %q", cfg.RetryInterval)
	}
	attempts := cfg.MaxRetries
	if attempts < 1 {
		attempts = 1
	}
	var nc *nats.Conn
	var connectErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		nc, connectErr = nats.Connect(cfg.Addr, opts...)
		if connectErr == nil {
			break
		}
		if attempt < attempts {
			time.Sleep(retryDelay)
		}
	}
	if connectErr != nil {
		return nil, fmt.Errorf("failed to connect to NATS after %d attempts: %w", attempts, connectErr)
	}

	logger.SysInfo(op, "Successfully connected to NATS broker")
	return nc, nil
}
