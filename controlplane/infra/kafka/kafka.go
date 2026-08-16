package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"controlplane/internal/config"
	"controlplane/internal/observability"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Producer đóng gói idempotent producer; mọi publish chỉ thành công sau acks=all.
type Producer struct {
	client  *kgo.Client
	metrics observability.DependencyRecorder
}

func NewProducer(ctx context.Context, cfg *config.KafkaCfg, metrics observability.DependencyRecorder) (*Producer, error) {
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
	return &Producer{client: client, metrics: metrics}, nil
}

func (p *Producer) Publish(ctx context.Context, topic string, key, value []byte) error {
	startedAt := time.Now()
	if p == nil || p.client == nil {
		return errors.New("kafka: producer is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := otel.Tracer("aurora-controlplane.kafka").Start(
		ctx,
		"kafka.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
	)
	defer span.End()
	// Topic/key/value remain out of telemetry: all can encode customer routing
	// or data. The central dependency recorder adds only bounded dimensions.
	span.SetAttributes(
		attribute.String("messaging.system", "kafka"),
		attribute.String("messaging.operation", "publish"),
	)
	err := p.client.ProduceSync(ctx, &kgo.Record{
		Topic: topic,
		Key:   key,
		Value: value,
	}).FirstErr()
	result, reason := observability.ResultSuccess, observability.ReasonNone
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			result, reason = observability.ResultFailure, observability.ReasonTimeout
		case errors.Is(err, context.Canceled):
			result, reason = observability.ResultFailure, observability.ReasonCanceled
		default:
			result, reason = observability.ResultFailure, observability.ReasonUnavailable
		}
	}
	span.SetAttributes(
		attribute.String("aurora.result", string(result)),
		attribute.String("aurora.reason", string(reason)),
	)
	if result == observability.ResultFailure {
		span.SetStatus(codes.Error, string(reason))
	}
	// Topic is deliberately excluded: deployments may use per-environment names,
	// while the stable operation label is sufficient for alerting.
	p.metrics.ObserveDependency(ctx, "kafka", "publish", result, reason, time.Since(startedAt))
	return err
}

// EnsureTopic provisions a dynamic workflow destination through the same
// authenticated Kafka client used for publish. The caller owns when this
// capability is needed and caches successful provisioning per destination.
func (p *Producer) EnsureTopic(ctx context.Context, topic string, partitions int32, retention time.Duration) error {
	if p == nil || p.client == nil {
		return errors.New("kafka: producer is unavailable")
	}
	if strings.TrimSpace(topic) == "" || partitions < 1 || retention <= 0 {
		return errors.New("kafka: invalid topic provisioning request")
	}
	retentionMillis := fmt.Sprintf("%d", retention.Milliseconds())
	request := kmsg.NewPtrCreateTopicsRequest()
	request.TimeoutMillis = 10_000
	request.Topics = []kmsg.CreateTopicsRequestTopic{{
		Topic:             topic,
		NumPartitions:     partitions,
		ReplicationFactor: -1,
		Configs: []kmsg.CreateTopicsRequestTopicConfig{{
			Name:  "retention.ms",
			Value: &retentionMillis,
		}},
	}}
	response, err := p.client.Request(ctx, request)
	if err != nil {
		return fmt.Errorf("kafka: create topic %s: %w", topic, err)
	}
	created, ok := response.(*kmsg.CreateTopicsResponse)
	if !ok || len(created.Topics) != 1 {
		return fmt.Errorf("kafka: create topic %s returned an invalid response", topic)
	}
	topicErr := kerr.ErrorForCode(created.Topics[0].ErrorCode)
	if topicErr != nil && !errors.Is(topicErr, kerr.TopicAlreadyExists) {
		return fmt.Errorf("kafka: create topic %s: %w", topic, topicErr)
	}
	return nil
}

func (p *Producer) Close() {
	if p != nil && p.client != nil {
		p.client.Close()
	}
}
