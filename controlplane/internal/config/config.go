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
3. đây là immutable config , không phải dynamic config (policyengine)
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
	App        AppCfg
	Security   SecurityCfg
	Psql       PsqlCfg
	Redis      RedisCfg
	RedisJob   RedisCfg
	GRPC       GRPCCfg
	Telegram   TelegramCfg
	Prometheus PrometheusCfg
	SchemaSQL  SchemaSQLCfg
	Agent      AgentCfg
}

// PrometheusCfg lưu trữ các tham số kết nối và truy vấn metrics tới Prometheus Server.
type PrometheusCfg struct {
	BaseURL      string
	QueryTimeout time.Duration
	DefaultStep  time.Duration
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
	RuntimeMasterKey          string
	AccessSecretTTL           time.Duration
	OneTimeTokenTTL           time.Duration
	RefreshTokenTTL           time.Duration
	AdminAPITokenTTL          time.Duration
	DeviceActiveTTL           time.Duration
	AdminSessionTTL           time.Duration
	AdminTrustedDeviceTTL     time.Duration
	OAuthAuthorizationCodeTTL time.Duration
	SecretCacheTTL            time.Duration
}

// PsqlCfg chứa các thông số kết nối cơ sở dữ liệu PostgreSQL và connection pool.
type PsqlCfg struct {
	Host          string
	Port          int
	User          string
	Password      string
	DBName        string
	Schema        string
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
}

// TelegramCfg lưu thông tin tích hợp thông báo lỗi và cảnh báo qua Telegram Bot.
type TelegramCfg struct {
	BotToken string
	ChatID   string
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
	Core string
	IAM  string
	Mail string
}

// AgentCfg chứa cấu hình cấp phát chứng chỉ mTLS cho các Agent kết nối vào gRPC Server.
type AgentCfg struct {
	CACertPath string
	CAKeyPath  string
	CertTTL    time.Duration
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
			RuntimeMasterKey:          strings.TrimSpace(getEnv("SECURITY_RUNTIME_MASTER_KEY", "")),
			AccessSecretTTL:           15 * time.Minute,
			OneTimeTokenTTL:           15 * time.Minute,
			RefreshTokenTTL:           168 * time.Hour,
			AdminAPITokenTTL:          15 * 24 * time.Hour,
			DeviceActiveTTL:           168 * time.Hour,
			AdminSessionTTL:           30 * time.Minute,
			AdminTrustedDeviceTTL:     30 * 24 * time.Hour,
			OAuthAuthorizationCodeTTL: 5 * time.Minute,
			SecretCacheTTL:            30 * time.Second,
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
			Addr:          getEnv("REDIS_ADDR", "localhost:6379"),
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
		},
		RedisJob: RedisCfg{
			Addr:          getEnv("REDIS_JOB_ADDR", "localhost:6380"),
			Password:      getEnv("REDIS_JOB_PASSWORD", ""),
			DB:            getEnvAsInt("REDIS_JOB_DB", 0),
			TLSEnabled:    getEnvAsBool("REDIS_JOB_TLS_ENABLED", false),
			CACertPath:    getEnv("REDIS_JOB_TLS_CA", ""),
			CertPath:      getEnv("REDIS_JOB_TLS_CERT", ""),
			KeyPath:       getEnv("REDIS_JOB_TLS_KEY", ""),
			DialTimeout:   2 * time.Second,
			ReadTimeout:   500 * time.Millisecond,
			WriteTimeout:  500 * time.Millisecond,
			PoolSize:      100,
			MinIdleConns:  10,
			PingTimeout:   2 * time.Second,
			MaxRetries:    5,
			RetryInterval: 1 * time.Second,
		},
		GRPC: GRPCCfg{
			Port:             getEnv("GRPC_PORT", "9443"),
			PublicAddr:       getEnv("GRPC_PUBLIC_ADDR", "localhost:9443"),
			TLSCertPath:      getEnv("GRPC_TLS_CERT", ""),
			TLSKeyPath:       getEnv("GRPC_TLS_KEY", ""),
			ClientCACertPath: getEnv("GRPC_CLIENT_CA", ""),
		},
		Telegram: TelegramCfg{
			BotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
			ChatID:   getEnv("TELEGRAM_CHAT_ID", ""),
		},

		Prometheus: PrometheusCfg{
			BaseURL:      getEnv("PROMETHEUS_BASE_URL", "http://127.0.0.1:9090"),
			QueryTimeout: 5 * time.Second,
			DefaultStep:  15 * time.Second,
		},
		SchemaSQL: SchemaSQLCfg{
			Core: "core",
			IAM:  "iam",
			Mail: "mail",
		},
		Agent: AgentCfg{
			CACertPath: getEnv("AGENT_CA_CERT_PATH", ""),
			CAKeyPath:  getEnv("AGENT_CA_KEY_PATH", ""),
			CertTTL:    8760 * time.Hour,
		},
	}
}
