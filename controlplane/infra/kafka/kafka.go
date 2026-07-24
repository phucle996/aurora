package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"strings"
	"time"

	"controlplane/internal/config"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
)

// Producer đóng gói idempotent producer; mọi publish chỉ thành công sau acks=all.
type Producer struct {
	client *kgo.Client
}

func NewProducer(ctx context.Context, cfg *config.KafkaCfg) (*Producer, error) {
	if cfg == nil || len(cfg.Brokers) == 0 {
		return nil, errors.New("kafka: at least one bootstrap broker is required")
	}
	options := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(cfg.ClientID),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.ZstdCompression()),
		kgo.ProducerLinger(5 * time.Millisecond),
		kgo.ProducerBatchMaxBytes(64 * 1024),
		kgo.RecordRetries(10),
		// [COMMENT]: Tự động thử lại khi gặp lỗi unknown topic do cache metadata của franz-go chưa kịp nạp từ broker.
		kgo.UnknownTopicRetries(10),
		kgo.RecordDeliveryTimeout(60 * time.Second),
	}

	securityProtocol := strings.ToLower(strings.TrimSpace(cfg.SecurityProtocol))
	if securityProtocol == "ssl" || securityProtocol == "sasl_plain_ssl" {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.CACertPath != "" {
			caPEM, err := os.ReadFile(cfg.CACertPath)
			if err != nil {
				return nil, err
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caPEM) {
				return nil, errors.New("kafka: CA certificate is invalid")
			}
			tlsConfig.RootCAs = pool
		}
		if cfg.CertPath != "" || cfg.KeyPath != "" {
			certificate, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
			if err != nil {
				return nil, err
			}
			tlsConfig.Certificates = []tls.Certificate{certificate}
		}
		options = append(options, kgo.DialTLSConfig(tlsConfig))
	}
	if securityProtocol == "sasl_plaintext" || securityProtocol == "sasl_plain_ssl" {
		if cfg.Username == "" || cfg.Password == "" {
			return nil, errors.New("kafka: SASL username and password are required")
		}
		options = append(options, kgo.SASL(plain.Auth{
			User: cfg.Username,
			Pass: cfg.Password,
		}.AsMechanism()))
	}
	if securityProtocol != "plaintext" && securityProtocol != "ssl" &&
		securityProtocol != "sasl_plaintext" && securityProtocol != "sasl_plain_ssl" {
		return nil, errors.New("kafka: unsupported security protocol")
	}

	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, err
	}
	// [COMMENT]: Không Ping broker ở bootstrap: mail xác minh là best-effort và login resend là recovery path.
	// PublishSync vẫn fail-close theo từng request; Kubernetes không restart toàn Controlplane vì outage Kafka ngắn.
	return &Producer{client: client}, nil
}

func (p *Producer) Publish(ctx context.Context, topic string, key, value []byte) error {
	if p == nil || p.client == nil {
		return errors.New("kafka: producer is unavailable")
	}
	return p.client.ProduceSync(ctx, &kgo.Record{
		Topic: topic,
		Key:   key,
		Value: value,
	}).FirstErr()
}

func (p *Producer) Close() {
	if p != nil && p.client != nil {
		p.client.Close()
	}
}
