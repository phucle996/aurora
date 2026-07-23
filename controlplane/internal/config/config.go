/*
============================================================================
🗺️ ARCHITECTURAL COMPONENT: CENTRAL APPLICATION CONFIGURATION
============================================================================
CONTRACT:
1.Định nghĩa cấu trúc dữ liệu và khởi tạo centralized config cho application từ file .env và OS env.
2.Đảm bảo tính Immutable trong suốt process lifecycle.

SOT: file này là source of truth cho toàn bộ cấu hình tĩnh của application.

BOUNDARY:
1. file này chỉ load immutable config từ .env và OS env, không validate dữ liệu.
2. khi sử dụng env, phải sử dụng các function helper của file này để avoid
4. chỉ parse theo kiểu dữ liệu , không xác định tính đúng sai logic.
============================================================================
*/

package config

import (
	"os"
	"strings"
	"time"
)

// Config là cấu trúc cấu hình gốc gom nhóm tất cả các cấu hình thành phần.
type Config struct {
	App      AppCfg
	Security SecurityCfg
	Psql     PsqlCfg
	Redis    RedisCfg
	// [COMMENT]: AuthRedis là security-state/authz projection; không dùng làm cache business chung.
	AuthRedis RedisCfg
	Kafka     KafkaCfg
	NATS      NATSCfg
	GRPC      GRPCCfg
	OTel      OTelCfg
	SchemaSQL SchemaSQLCfg
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

// AppCfg lưu trữ thông tin cấu hình cơ bản của Web Application và HTTP Server.
type AppCfg struct {
	AppName            string
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
	RuntimeMasterKey        string
	OneTimeTokenTTL         time.Duration
	OneTimeTokenReplicaAcks int
	OneTimeTokenReplicaWait time.Duration
	RefreshTokenTTL         time.Duration
}

// PsqlCfg chứa các thông số kết nối cơ sở dữ liệu PostgreSQL và connection pool.
type PsqlCfg struct {
	Host          string
	Port          int
	User          string
	Password      string
	DBName        string
	SSLMode       string
	TLSEnabled    bool
	CACertPath    string
	CertPath      string
	KeyPath       string
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
	Addr          string
	Username      string
	Password      string
	DB            int
	TLSEnabled    bool
	CACertPath    string
	CertPath      string
	KeyPath       string
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
}

// NATSCfg chứa thông số kết nối NATS Core hỗ trợ TLS/mTLS.
type NATSCfg struct {
	Addr          string
	TLSEnabled    bool
	CACertPath    string
	CertPath      string
	KeyPath       string
	MaxRetries    int
	RetryInterval time.Duration
}

// GRPCCfg lưu thông số kết nối của gRPC Server và cấu hình TLS/mTLS.
type GRPCCfg struct {
	Port             string
	PublicAddr       string
	TLSCertPath      string
	TLSKeyPath       string
	ClientCACertPath string
}

// SchemaSQLCfg định nghĩa tên SQL Schema cho từng phân hệ trong PostgreSQL.
type SchemaSQLCfg struct {
	Hierarchy  string
	IAM        string
	Mail       string
	Hypervisor string // [NEW COMMENT]: Tên SQL schema lưu trữ thông tin của phân hệ Hypervisor Nodes
	Storage    string // [COMMENT]: Tên SQL schema dành riêng cho phân hệ Object Storage (MinIO)
}

// LoadConfig đọc cấu hình từ environment variables của hệ thống.
// Nếu APP_NAME trống, hàm sẽ lấy hostname của máy làm AppName hoặc fallback về controlplane.
func LoadConfig() *Config {
	appName := getEnv("APP_NAME", "")
	if appName == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			appName = hostname
		} else {
			appName = "controlplane"
		}
	}

	return &Config{
		App: AppCfg{
			AppName:            appName,
			TimeZone:           getEnv("APP_TIMEZONE", "UTC"),
			HTTPPort:           getEnvAsInt("APP_HTTP_PORT", 8080),
			PublicDomain:       strings.TrimSpace(getEnv("APP_PUBLIC_DOMAIN", "")),
			TrustedProxies:     getEnvAsCSV("APP_TRUSTED_PROXIES", nil),
			AllowedOrigins:     getEnvAsCSV("APP_ALLOWED_ORIGINS", nil),
			OAuthAllowedScopes: getEnvAsCSV("APP_OAUTH_ALLOWED_SCOPES", []string{"profile", "email", "offline_access"}),
		},

		Security: SecurityCfg{
			RuntimeMasterKey: getEnv("SECURITY_MASTER_KEY", "aurora-storage-master-secret-key-32bytes"),
			OneTimeTokenTTL:  getEnvAsDuration("IAM_OTT_TTL", 15*time.Minute),
			// [COMMENT]: Production mặc định đòi ít nhất một replica ACK; dev đơn node phải override về 0 rõ ràng.
			OneTimeTokenReplicaAcks: getEnvAsInt("IAM_OTT_REPLICA_ACKS", 1),
			OneTimeTokenReplicaWait: getEnvAsDuration("IAM_OTT_REPLICA_WAIT", time.Second),
			RefreshTokenTTL:         30 * 24 * time.Hour,
		},
		Psql: PsqlCfg{
			Host:          getEnv("PSQL_HOST", "localhost"),
			Port:          getEnvAsInt("PSQL_PORT", 5432),
			User:          getEnv("PSQL_USER", "postgres"),
			Password:      getEnv("PSQL_PASSWORD", ""),
			DBName:        getEnv("PSQL_DBNAME", "controlplane"),
			SSLMode:       getEnv("PSQL_SSLMODE", "disable"),
			TLSEnabled:    getEnvAsBool("PSQL_TLS_ENABLED", false),
			CACertPath:    getEnv("PSQL_TLS_CA", ""),
			CertPath:      getEnv("PSQL_TLS_CERT", ""),
			KeyPath:       getEnv("PSQL_TLS_KEY", ""),
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
			Addr:          getEnv("REDIS_ADDR", "localhost:6379"),
			Username:      getEnv("REDIS_USERNAME", ""),
			Password:      getEnv("REDIS_PASSWORD", ""),
			DB:            getEnvAsInt("REDIS_DB", 0),
			TLSEnabled:    getEnvAsBool("REDIS_TLS_ENABLED", false),
			CACertPath:    getEnv("REDIS_TLS_CA", ""),
			CertPath:      getEnv("REDIS_TLS_CERT", ""),
			KeyPath:       getEnv("REDIS_TLS_KEY", ""),
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
			Addr:          getEnv("AUTH_REDIS_ADDR", "localhost:16380"),
			Username:      getEnv("AUTH_REDIS_USERNAME", "controlplane"),
			Password:      getEnv("AUTH_REDIS_PASSWORD", ""),
			DB:            0,
			TLSEnabled:    getEnvAsBool("AUTH_REDIS_TLS_ENABLED", false),
			CACertPath:    getEnv("AUTH_REDIS_TLS_CA", ""),
			CertPath:      getEnv("AUTH_REDIS_TLS_CERT", ""),
			KeyPath:       getEnv("AUTH_REDIS_TLS_KEY", ""),
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
			Brokers:              getEnvAsCSV("KAFKA_BOOTSTRAP_SERVERS", []string{"localhost:19092", "localhost:29092", "localhost:39092"}),
			ClientID:             appName,
			SecurityProtocol:     strings.ToLower(getEnv("KAFKA_SECURITY_PROTOCOL", "plaintext")),
			Username:             getEnv("KAFKA_USERNAME", ""),
			Password:             getEnv("KAFKA_PASSWORD", ""),
			CACertPath:           getEnv("KAFKA_TLS_CA", ""),
			CertPath:             getEnv("KAFKA_TLS_CERT", ""),
			KeyPath:              getEnv("KAFKA_TLS_KEY", ""),
			IAMVerificationTopic: getEnv("KAFKA_IAM_VERIFICATION_TOPIC", "aurora.iam.account-verification.v1"),
		},
		GRPC: GRPCCfg{
			Port:             getEnv("GRPC_PORT", "9443"),
			PublicAddr:       getEnv("GRPC_PUBLIC_ADDR", "localhost:9443"),
			TLSCertPath:      getEnv("GRPC_TLS_CERT", ""),
			TLSKeyPath:       getEnv("GRPC_TLS_KEY", ""),
			ClientCACertPath: getEnv("GRPC_CLIENT_CA", ""),
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
			Hierarchy:  "hierarchy",
			IAM:        "iam",
			Mail:       "mail",
			Hypervisor: "hypervisor", // [NEW COMMENT]: Khởi tạo giá trị mặc định là schema 'hypervisor'
			Storage:    "storage",    // [COMMENT]: Khởi tạo tên schema mặc định cho Object Storage là 'storage'
		},
		NATS: NATSCfg{
			Addr:          getEnv("NATS_ADDR", "nats://localhost:4222"),
			TLSEnabled:    getEnvAsBool("NATS_TLS_ENABLED", false),
			CACertPath:    getEnv("NATS_TLS_CA", ""),
			CertPath:      getEnv("NATS_TLS_CERT", ""),
			KeyPath:       getEnv("NATS_TLS_KEY", ""),
			MaxRetries:    getEnvAsInt("NATS_MAX_RETRIES", 5),
			RetryInterval: getEnvAsDuration("NATS_RETRY_INTERVAL", 2*time.Second),
		},
	}
}
