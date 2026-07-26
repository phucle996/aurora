/*
============================================================================
MAP: COST MANAGER API CONFIGURATION
============================================================================
CONTRACT:
1. Định nghĩa cấu trúc dữ liệu và khởi tạo centralized config từ OS env.
2. Đảm bảo tính Immutable trong suốt process lifecycle.
3. Cung cấp đầy đủ cấu hình kết nối PostgreSQL, Shared/Auth Redis và TLS/mTLS.

SOT: file này là Source of Truth cho toàn bộ cấu hình tĩnh của Cost Manager API.
============================================================================
*/

package config

// [COMMENT]: Config là cấu trúc cấu hình gốc gom nhóm tất cả phân hệ hạ tầng của Cost Manager.
type Config struct {
	App       AppCfg
	Psql      PsqlCfg
	Redis     RedisCfg
	AuthRedis RedisCfg
	GRPC      GRPCCfg
	Payment   PaymentCfg
}

// [COMMENT]: AppCfg lưu trữ thông tin cấu hình dịch vụ web và HTTP REST Server.
type AppCfg struct {
	AppName        string
	Env            string
	TimeZone       string
	HTTPPort       int
	LogLV          string
	PublicDomain   string
	TrustedProxies []string
	AllowedOrigins []string
}

// [COMMENT]: PsqlCfg lưu trữ tham số kết nối PostgreSQL Database và TLS Options.
type PsqlCfg struct {
	Host        string
	Port        int
	User        string
	Password    string
	DBName      string
	SSLMode     string
	TLSEnabled  bool
	CACertPath  string
	CertPath    string
	KeyPath     string
	MaxConns    int
	MinConns    int
	MaxConnLife string
	MaxConnIdle string
}

// [COMMENT]: RedisCfg chứa thông số kết nối Redis Cache và TLS/mTLS Options.
type RedisCfg struct {
	Addr        string
	Password    string
	DB          int
	TLSEnabled  bool
	CACertPath  string
	CertPath    string
	KeyPath     string
	DialTimeout string
	PoolSize    int
}

// [COMMENT]: GRPCCfg lưu thông số kết nối của gRPC Reconciler / Server và cấu hình TLS.
type GRPCCfg struct {
	Port             string
	PublicAddr       string
	TLSCertPath      string
	TLSKeyPath       string
	ClientCACertPath string
}

// PaymentCfg defines the contract with the external checkout gateway. Signing
// secrets and the gateway endpoint are required infrastructure, never defaults.
type PaymentCfg struct {
	Provider               string
	CheckoutBaseURL        string
	ReturnBaseURL          string
	CheckoutSigningSecret  string
	WebhookSigningSecret   string
	MinimumTopUpMicroUnits int64
	IntentTTL              string
	ReferralReservationTTL string
	WebhookTolerance       string
}

// [COMMENT]: LoadConfig đọc và parse toàn bộ biến môi trường hệ thống.
func LoadConfig() *Config {
	return &Config{
		App: AppCfg{
			AppName:        getEnv("APP_NAME", "cost-manager-api"),
			Env:            getEnv("APP_ENV", "development"),
			TimeZone:       getEnv("APP_TIMEZONE", "UTC"),
			HTTPPort:       getEnvAsInt("PORT", 8084),
			LogLV:          getEnv("LOG_LEVEL", "info"),
			PublicDomain:   getEnv("APP_PUBLIC_DOMAIN", "http://localhost:8084"),
			TrustedProxies: getEnvAsCSV("APP_TRUSTED_PROXIES", nil),
			AllowedOrigins: getEnvAsCSV("APP_ALLOWED_ORIGINS", []string{"*"}),
		},
		Psql: PsqlCfg{
			Host:        getEnv("POSTGRES_HOST", "billing-psql"),
			Port:        getEnvAsInt("POSTGRES_PORT", 5432),
			User:        getEnv("POSTGRES_USER", "billing_admin"),
			Password:    getEnv("POSTGRES_PASSWORD", "billing_secure_password"),
			DBName:      getEnv("POSTGRES_DB", "billing"),
			SSLMode:     getEnv("POSTGRES_SSLMODE", "disable"),
			TLSEnabled:  getEnvAsBool("POSTGRES_TLS_ENABLED", false),
			CACertPath:  getEnv("POSTGRES_CA_CERT_PATH", ""),
			CertPath:    getEnv("POSTGRES_CERT_PATH", ""),
			KeyPath:     getEnv("POSTGRES_KEY_PATH", ""),
			MaxConns:    getEnvAsInt("POSTGRES_MAX_CONNS", 25),
			MinConns:    getEnvAsInt("POSTGRES_MIN_CONNS", 5),
			MaxConnLife: getEnv("POSTGRES_MAX_CONN_LIFE", "30m"),
			MaxConnIdle: getEnv("POSTGRES_MAX_CONN_IDLE", "10m"),
		},
		Redis: RedisCfg{
			// [COMMENT]: Shared Redis giữ cache/pubsub/lock và bounded AOF-backed Streams;
			// wallet command không được chạy trên deployment có allkeys eviction.
			Addr:        getEnv("CACHE_REDIS_URL", getEnv("REDIS_URL", "redis://controlplane-cp-redis:6379")),
			Password:    getEnv("REDIS_PASSWORD", ""),
			DB:          getEnvAsInt("REDIS_DB", 0),
			TLSEnabled:  getEnvAsBool("REDIS_TLS_ENABLED", false),
			CACertPath:  getEnv("REDIS_CA_CERT_PATH", ""),
			CertPath:    getEnv("REDIS_CERT_PATH", ""),
			KeyPath:     getEnv("REDIS_KEY_PATH", ""),
			DialTimeout: getEnv("REDIS_DIAL_TIMEOUT", "5s"),
			PoolSize:    getEnvAsInt("REDIS_POOL_SIZE", 20),
		},
		AuthRedis: RedisCfg{
			// [COMMENT]: Cost credential chỉ đọc authz/proof prefix; không được đọc hoặc sửa session ACR.
			Addr:        getEnv("AUTH_REDIS_URL", "redis://cost@controlplane-acr-redis:6379/0"),
			Password:    getEnv("AUTH_REDIS_PASSWORD", ""),
			DB:          0,
			TLSEnabled:  getEnvAsBool("AUTH_REDIS_TLS_ENABLED", false),
			CACertPath:  getEnv("AUTH_REDIS_CA_CERT_PATH", ""),
			CertPath:    getEnv("AUTH_REDIS_CERT_PATH", ""),
			KeyPath:     getEnv("AUTH_REDIS_KEY_PATH", ""),
			DialTimeout: getEnv("AUTH_REDIS_DIAL_TIMEOUT", "5s"),
			PoolSize:    getEnvAsInt("AUTH_REDIS_POOL_SIZE", 20),
		},
		GRPC: GRPCCfg{
			Port:             getEnv("GRPC_PORT", "9094"),
			PublicAddr:       getEnv("GRPC_PUBLIC_ADDR", "localhost:9094"),
			TLSCertPath:      getEnv("GRPC_TLS_CERT_PATH", ""),
			TLSKeyPath:       getEnv("GRPC_TLS_KEY_PATH", ""),
			ClientCACertPath: getEnv("GRPC_CLIENT_CA_CERT_PATH", ""),
		},
		Payment: PaymentCfg{
			Provider:               getEnv("PAYMENT_PROVIDER", ""),
			CheckoutBaseURL:        getEnv("PAYMENT_CHECKOUT_BASE_URL", ""),
			ReturnBaseURL:          getEnv("PAYMENT_RETURN_BASE_URL", ""),
			CheckoutSigningSecret:  getEnv("PAYMENT_CHECKOUT_SIGNING_SECRET", ""),
			WebhookSigningSecret:   getEnv("PAYMENT_WEBHOOK_SIGNING_SECRET", ""),
			MinimumTopUpMicroUnits: getEnvAsInt64("PAYMENT_MINIMUM_TOP_UP_MICRO_UNITS", 1_000_000),
			IntentTTL:              getEnv("PAYMENT_INTENT_TTL", "30m"),
			ReferralReservationTTL: getEnv("PAYMENT_REFERRAL_RESERVATION_TTL", "24h"),
			WebhookTolerance:       getEnv("PAYMENT_WEBHOOK_TOLERANCE", "5m"),
		},
	}
}
