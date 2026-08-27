/*
============================================================================
🗺️ ARCHITECTURAL COMPONENT: CENTRAL APPLICATION CONFIGURATION
============================================================================
CONTRACT:
1. Định nghĩa bootstrap config từ file .env và OS env.
2. Chỉ giữ Vault authentication bootstrap và non-secret infrastructure policy;
   connector đọc connection record trực tiếp từ Vault.
3. Đảm bảo tính Immutable trong suốt process lifecycle.

SOT: file này là source of truth cho toàn bộ cấu hình tĩnh của application.

BOUNDARY:
1. Bootstrap chỉ chứa thông tin deployment và Vault authentication.
2. Khi sử dụng env, phải sử dụng các function helper của file này để tránh
   đọc trực tiếp từ process environment.
============================================================================
*/

package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config là cấu trúc cấu hình gốc gom nhóm tất cả các cấu hình thành phần.
type Config struct {
	App      AppCfg
	Security SecurityCfg
	Vault    VaultCfg
	Psql     PsqlCfg
	Redis    RedisCfg
	// [COMMENT]: AuthRedis là security-state/authz projection; không dùng làm cache business chung.
	AuthRedis RedisCfg
	Kafka     KafkaCfg
	OTel      OTelCfg
	SchemaSQL SchemaSQLCfg
}

// VaultCfg contains only the bootstrap material required to let the
// application read its connection snapshot. PostgreSQL/Redis credentials are
// intentionally absent from this environment-level contract when Vault mode
// is enabled.
type VaultCfg struct {
	Addr              string
	Token             string
	RoleID            string
	SecretID          string
	KubernetesRole    string
	KubernetesJWTPath string
	Timeout           time.Duration
	MaxRetries        int
}

// OTelCfg lưu trữ cấu hình tĩnh cho OpenTelemetry.
type OTelCfg struct {
	Enabled       bool
	FailStrategy  string
	ExporterType  string
	Endpoint      string
	Insecure      bool
	SamplingRatio float64
	ExportTimeout time.Duration
	BatchTimeout  time.Duration
	BatchMaxSize  int
	BatchMaxQueue int
	// TLS định cấu hình kết nối bảo mật TLS/mTLS cho OTel exporter
	TLSMode    string
	CACertPath string
	CertPath   string
	KeyPath    string
}

// AppInfo chứa thông tin định danh ứng dụng (Tên và Phiên bản).
type AppInfo struct {
	Name    string
	Version string
}

// AppCfg lưu trữ thông tin cấu hình cơ bản của Web Application và HTTP Server.
type AppCfg struct {
	Info               AppInfo
	AppName            string
	AppVersion         string
	TimeZone           string
	HTTPPort           int
	LogLV              string
	PublicDomain       string
	TrustedProxies     []string
	AllowedOrigins     []string
	OAuthAllowedScopes []string
}

// SecurityCfg lưu trữ các tham số bảo mật, thời hạn TTL của các loại Token và Session.
type SecurityCfg struct {
	OneTimeTokenTTL         time.Duration
	OneTimeTokenReplicaAcks int
	OneTimeTokenReplicaWait time.Duration
	RefreshTokenTTL         time.Duration
}

// PsqlCfg chứa các thông số kết nối cơ sở dữ liệu PostgreSQL và connection pool.
type PsqlCfg struct {
	MaxConns      int
	MinConns      int
	MaxConnLife   time.Duration
	MaxConnIdle   time.Duration
	PingTimeout   time.Duration
	MaxRetries    int
	RetryInterval time.Duration
}

// RedisCfg chứa các thông số kết nối Redis cho cache và job queuing.
type RedisCfg struct {
	DialTimeout   time.Duration
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	PoolSize      int
	MinIdleConns  int
	PingTimeout   time.Duration
	MaxRetries    int
	RetryInterval time.Duration
	// [COMMENT]: Chỉ Shared Redis durable Stream publisher dùng hai ngưỡng này;
	// Auth Redis không tái sử dụng chúng làm business policy.
	DurableReplicaAcks int
	DurableWait        time.Duration
}

// KafkaCfg là durable platform transport; Redis chính không được dùng làm Job Queue.
type KafkaCfg struct {
	Brokers              []string
	ClientID             string
	SecurityProtocol     string
	Username             string
	Password             string
	CACertPath           string
	CertPath             string
	KeyPath              string
	IAMVerificationTopic string
	TopicPrefix          string
}

// SchemaSQLCfg định nghĩa tên SQL Schema cho từng phân hệ trong PostgreSQL.
type SchemaSQLCfg struct {
	Hierarchy      string
	IAM            string
	Mail           string
	Hypervisor     string // [NEW COMMENT]: Tên SQL schema lưu trữ thông tin của phân hệ Hypervisor Nodes
	Storage        string // [COMMENT]: Tên SQL schema dành riêng cho phân hệ Object Storage (MinIO)
	ManagedService string // [COMMENT]: Durable desired state của Managed Service Platform, không chứa runtime hoặc secret plaintext.
}

// LoadConfig đọc cấu hình từ environment variables của hệ thống.
// Nếu APP_NAME trống, hàm sẽ lấy hostname của máy làm AppName hoặc fallback về controlplane.
func LoadConfig() (*Config, error) {
	appName := getEnv("APP_NAME", "")
	if appName == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			appName = hostname
		} else {
			appName = "controlplane"
		}
	}

	allowedOrigins, err := requireEnvAsCSV("APP_ALLOWED_ORIGINS")
	if err != nil {
		return nil, err
	}
	vaultAddr, err := requireEnv("VAULT_ADDR")
	if err != nil {
		return nil, err
	}
	kafkaBrokers, err := requireEnvAsCSV("KAFKA_BOOTSTRAP_SERVERS")
	if err != nil {
		return nil, err
	}
	kafkaSecurityProtocol, err := requireEnv("KAFKA_SECURITY_PROTOCOL")
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(kafkaSecurityProtocol) {
	case "plaintext", "ssl", "sasl_plaintext", "sasl_plain_ssl":
	default:
		return nil, fmt.Errorf("KAFKA_SECURITY_PROTOCOL is invalid")
	}
	kafkaIAMVerificationTopic, err := requireEnv("KAFKA_IAM_VERIFICATION_TOPIC")
	if err != nil {
		return nil, err
	}
	appVersion := getEnv("APP_VERSION", "dev")
	cfg := &Config{
		App: AppCfg{
			Info: AppInfo{
				Name:    appName,
				Version: appVersion,
			},
			AppName:            appName,
			AppVersion:         appVersion,
			TimeZone:           getEnv("APP_TIMEZONE", "UTC"),
			HTTPPort:           getEnvAsInt("APP_HTTP_PORT", 8080),
			PublicDomain:       strings.TrimSpace(getEnv("APP_PUBLIC_DOMAIN", "")),
			TrustedProxies:     getEnvAsCSV("APP_TRUSTED_PROXIES", nil),
			AllowedOrigins:     allowedOrigins,
			OAuthAllowedScopes: getEnvAsCSV("APP_OAUTH_ALLOWED_SCOPES", []string{"profile", "email", "offline_access"}),
		},

		Security: SecurityCfg{
			OneTimeTokenTTL: getEnvAsDuration("IAM_OTT_TTL", 15*time.Minute),
			// [COMMENT]: Production mặc định đòi ít nhất một replica ACK; dev đơn node phải override về 0 rõ ràng.
			OneTimeTokenReplicaAcks: getEnvAsInt("IAM_OTT_REPLICA_ACKS", 1),
			OneTimeTokenReplicaWait: getEnvAsDuration("IAM_OTT_REPLICA_WAIT", time.Second),
			RefreshTokenTTL:         30 * 24 * time.Hour,
		},
		Vault: VaultCfg{
			Addr:              vaultAddr,
			Token:             getEnv("VAULT_TOKEN", ""),
			RoleID:            getEnv("VAULT_ROLE_ID", ""),
			SecretID:          getEnv("VAULT_SECRET_ID", ""),
			KubernetesRole:    getEnv("VAULT_KUBERNETES_ROLE", ""),
			KubernetesJWTPath: getEnv("VAULT_KUBERNETES_JWT_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/token"),
			Timeout:           getEnvAsDuration("VAULT_TIMEOUT", 5*time.Second),
			MaxRetries:        getEnvAsInt("VAULT_MAX_RETRIES", 5),
		},
		Psql: PsqlCfg{
			MaxConns:      100,
			MinConns:      5,
			MaxConnLife:   30 * time.Minute,
			MaxConnIdle:   5 * time.Minute,
			PingTimeout:   5 * time.Second,
			MaxRetries:    5,
			RetryInterval: 3 * time.Second,
		},
		Redis: RedisCfg{
			// [COMMENT]: Shared Redis chứa cache/pubsub/lock và bounded internal Streams;
			// deployment phải persistence và không được dùng allkeys eviction.
			DialTimeout:   2 * time.Second,
			ReadTimeout:   500 * time.Millisecond,
			WriteTimeout:  500 * time.Millisecond,
			PoolSize:      100,
			MinIdleConns:  10,
			PingTimeout:   2 * time.Second,
			MaxRetries:    5,
			RetryInterval: 1 * time.Second,
			// [COMMENT]: Production fail-close nếu chưa có ít nhất một replica fsync.
			// Docker Compose đơn node phải override về 0 một cách minh bạch.
			DurableReplicaAcks: getEnvAsInt("REDIS_DURABLE_REPLICA_ACKS", 1),
			DurableWait:        getEnvAsDuration("REDIS_DURABLE_WAIT", 2*time.Second),
		},
		AuthRedis: RedisCfg{
			// [COMMENT]: Redis Cluster chỉ dùng DB 0; session/authz cô lập bằng prefix + ACL, không dùng SELECT DB 1.
			DialTimeout:   2 * time.Second,
			ReadTimeout:   500 * time.Millisecond,
			WriteTimeout:  500 * time.Millisecond,
			PoolSize:      50,
			MinIdleConns:  5,
			PingTimeout:   2 * time.Second,
			MaxRetries:    5,
			RetryInterval: 1 * time.Second,
		},
		Kafka: KafkaCfg{
			Brokers:              kafkaBrokers,
			ClientID:             appName,
			SecurityProtocol:     strings.ToLower(kafkaSecurityProtocol),
			Username:             getEnv("KAFKA_USERNAME", ""),
			Password:             getEnv("KAFKA_PASSWORD", ""),
			CACertPath:           getEnv("KAFKA_TLS_CA", ""),
			CertPath:             getEnv("KAFKA_TLS_CERT", ""),
			KeyPath:              getEnv("KAFKA_TLS_KEY", ""),
			IAMVerificationTopic: kafkaIAMVerificationTopic,
			TopicPrefix:          getEnv("KAFKA_TOPIC_PREFIX", "aurora"),
		},

		OTel: OTelCfg{
			Enabled:       getEnvAsBool("OTEL_ENABLED", true),
			FailStrategy:  getEnv("OTEL_FAIL_STRATEGY", "fail_open"),
			ExporterType:  getEnv("OTEL_EXPORTER_TYPE", "otlpgrpc"),
			Endpoint:      getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4317"),
			Insecure:      getEnvAsBool("OTEL_EXPORTER_OTLP_INSECURE", true),
			SamplingRatio: getEnvAsFloat64("OTEL_TRACES_SAMPLER_ARG", 1.0),
			ExportTimeout: getEnvAsDuration("OTEL_EXPORT_TIMEOUT", 5*time.Second),
			BatchTimeout:  getEnvAsDuration("OTEL_BSP_SCHEDULE_DELAY", 2*time.Second),
			BatchMaxSize:  getEnvAsInt("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", 512),
			BatchMaxQueue: getEnvAsInt("OTEL_BSP_MAX_QUEUE_SIZE", 2048),
			// Cấu hình các tham số TLS dạng phẳng không lồng nhau
			TLSMode:    getEnv("OTEL_TLS_MODE", "disable"),
			CACertPath: getEnv("OTEL_TLS_CA", ""),
			CertPath:   getEnv("OTEL_TLS_CERT", ""),
			KeyPath:    getEnv("OTEL_TLS_KEY", ""),
		},
		SchemaSQL: SchemaSQLCfg{
			Hierarchy:      "hierarchy",
			IAM:            "iam",
			Mail:           "mail",
			Hypervisor:     "hypervisor", // [NEW COMMENT]: Khởi tạo giá trị mặc định là schema 'hypervisor'
			Storage:        "storage",    // [COMMENT]: Khởi tạo tên schema mặc định cho Object Storage là 'storage'
			ManagedService: "managed_service",
		},
	}
	return cfg, nil
}
