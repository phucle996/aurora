package config

import (
	"strings"
	"time"
)

type Config struct {
	App        AppCfg
	OTel       OTelCfg
	Security   SecurityCfg
	Psql       PsqlCfg
	Redis      RedisCfg
	GRPC       GRPCCfg
	Telegram   TelegramCfg
	Dataplane  DataplaneCfg
	Prometheus PrometheusCfg
	SchemaSQL  SchemaSQLCfg
	Agent      AgentCfg
}

type PrometheusCfg struct {
	BaseURL      string
	QueryTimeout time.Duration
	DefaultStep  time.Duration
}

type OTelCfg struct {
	Enabled       bool
	ServiceName   string
	ExporterType  string
	Endpoint      string
	Insecure      bool
	SamplingRatio float64
	ExportTimeout time.Duration
	BatchTimeout  time.Duration
	BatchMaxSize  int
	BatchMaxQueue int
}

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

type SecurityCfg struct {
	RuntimeMasterKey          string
	AccessSecretTTL           time.Duration
	OneTimeTokenTTL           time.Duration
	RefreshTokenTTL           time.Duration
	AdminAPITokenTTL          time.Duration
	DeviceActiveTTL           time.Duration
	AdminSessionTTL           time.Duration
	AdminTrustedDeviceTTL     time.Duration
	AdminAllowedCIDRs         []string
	OAuthAuthorizationCodeTTL time.Duration
	SecretCacheTTL            time.Duration
}

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

type TelegramCfg struct {
	BotToken string
	ChatID   string
}

type GRPCCfg struct {
	Port             string
	PublicAddr       string
	TLSCertPath      string
	TLSKeyPath       string
	ClientCACertPath string
}

type DataplaneCfg struct {
	GRPCTarget     string
	RequestTimeout time.Duration
}

type SchemaSQLCfg struct {
	Core string
	IAM  string
}

type AgentCfg struct {
	CACertPath string
	CAKeyPath  string
	CertTTL    time.Duration
}

func LoadConfig() *Config {

	return &Config{
		App: AppCfg{
			AppName:            getEnv("APP_NAME", "controlplane"),
			TimeZone:           getEnv("APP_TIMEZONE", "UTC"),
			HTTPPort:           getEnvAsInt("APP_HTTP_PORT", 8080),
			PublicDomain:       strings.TrimSpace(getEnv("APP_PUBLIC_DOMAIN", "")),
			TrustedProxies:     getEnvAsCSV("APP_TRUSTED_PROXIES", nil),
			AllowedOrigins:     getEnvAsCSV("APP_ALLOWED_ORIGINS", nil),
			OAuthAllowedScopes: getEnvAsCSV("APP_OAUTH_ALLOWED_SCOPES", []string{"profile", "email", "offline_access"}),
		},
		OTel: OTelCfg{
			Enabled:       getEnvAsBool("OTEL_ENABLED", true),
			ExporterType:  getEnv("OTEL_EXPORTER_TYPE", "otlpgrpc"),
			Endpoint:      getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317"),
			Insecure:      getEnvAsBool("OTEL_EXPORTER_OTLP_INSECURE", true),
			SamplingRatio: getEnvAsFloat64("OTEL_TRACES_SAMPLER_ARG", 1.0),
			ExportTimeout: getEnvAsDuration("OTEL_EXPORT_TIMEOUT", 5*time.Second),
			BatchTimeout:  getEnvAsDuration("OTEL_BSP_SCHEDULE_DELAY", 2*time.Second),
			BatchMaxSize:  getEnvAsInt("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", 512),
			BatchMaxQueue: getEnvAsInt("OTEL_BSP_MAX_QUEUE_SIZE", 2048),
		},
		Security: SecurityCfg{
			RuntimeMasterKey:          strings.TrimSpace(getEnv("SECURITY_RUNTIME_MASTER_KEY", "")),
			AccessSecretTTL:           15 * time.Minute,
			OneTimeTokenTTL:           15 * time.Minute,
			RefreshTokenTTL:           168 * time.Hour,
			AdminAPITokenTTL:          15 * 24 * time.Hour,
			DeviceActiveTTL:           168 * time.Hour,
			AdminSessionTTL:           15 * time.Minute,
			AdminTrustedDeviceTTL:     30 * 24 * time.Hour,
			AdminAllowedCIDRs:         getEnvAsCSV("SECURITY_ADMIN_ALLOWED_CIDRS", []string{"0.0.0.0/0", "::/0"}),
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
			MaxConns:      20,
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
			DialTimeout:   5 * time.Second,
			ReadTimeout:   3 * time.Second,
			WriteTimeout:  3 * time.Second,
			PoolSize:      20,
			MinIdleConns:  5,
			PingTimeout:   5 * time.Second,
			MaxRetries:    5,
			RetryInterval: 3 * time.Second,
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
		Dataplane: DataplaneCfg{
			GRPCTarget:     getEnv("DATAPLANE_GRPC_TARGET", "localhost:50051"),
			RequestTimeout: getEnvAsDuration("DATAPLANE_REQUEST_TIMEOUT", 10*time.Second),
		},
		Prometheus: PrometheusCfg{
			BaseURL:      getEnv("PROMETHEUS_BASE_URL", "http://127.0.0.1:9090"),
			QueryTimeout: 5 * time.Second,
			DefaultStep:  15 * time.Second,
		},
		SchemaSQL: SchemaSQLCfg{
			Core: "core",
			IAM:  "iam",
		},
		Agent: AgentCfg{
			CACertPath: getEnv("AGENT_CA_CERT_PATH", ""),
			CAKeyPath:  getEnv("AGENT_CA_KEY_PATH", ""),
			CertTTL:    8760 * time.Hour,
		},
	}
}
