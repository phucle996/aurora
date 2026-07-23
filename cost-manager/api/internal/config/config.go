/*
============================================================================
MAP: COST MANAGER API CONFIGURATION
============================================================================
CONTRACT:
1. Định nghĩa cấu trúc dữ liệu và khởi tạo centralized config từ OS env.
2. Đảm bảo tính Immutable trong suốt process lifecycle.
3. Cung cấp đầy đủ cấu hình kết nối PostgreSQL, NATS JetStream, Redis và TLS/mTLS.

SOT: file này là Source of Truth cho toàn bộ cấu hình tĩnh của Cost Manager API.
============================================================================
*/

package config

// [COMMENT]: Config là cấu trúc cấu hình gốc gom nhóm tất cả phân hệ hạ tầng của Cost Manager.
type Config struct {
	App   AppCfg
	Psql  PsqlCfg
	Redis RedisCfg
	NATS  NATSCfg
	GRPC  GRPCCfg
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

// [COMMENT]: NATSCfg chứa thông số kết nối NATS Core / JetStream hỗ trợ TLS/mTLS.
type NATSCfg struct {
	Addr          string
	TLSEnabled    bool
	CACertPath    string
	CertPath      string
	KeyPath       string
	MaxRetries    int
	RetryInterval string
}

// [COMMENT]: GRPCCfg lưu thông số kết nối của gRPC Reconciler / Server và cấu hình TLS.
type GRPCCfg struct {
	Port             string
	PublicAddr       string
	TLSCertPath      string
	TLSKeyPath       string
	ClientCACertPath string
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
			// [COMMENT]: Cost chỉ dùng shared rebuildable cache; Security-State Redis của ACR không được cấp credential.
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
		NATS: NATSCfg{
			Addr:          getEnv("NATS_URL", "nats://controlplane-nats:4222"),
			TLSEnabled:    getEnvAsBool("NATS_TLS_ENABLED", false),
			CACertPath:    getEnv("NATS_CA_CERT_PATH", ""),
			CertPath:      getEnv("NATS_CERT_PATH", ""),
			KeyPath:       getEnv("NATS_KEY_PATH", ""),
			MaxRetries:    getEnvAsInt("NATS_MAX_RETRIES", 5),
			RetryInterval: getEnv("NATS_RETRY_INTERVAL", "2s"),
		},
		GRPC: GRPCCfg{
			Port:             getEnv("GRPC_PORT", "9094"),
			PublicAddr:       getEnv("GRPC_PUBLIC_ADDR", "localhost:9094"),
			TLSCertPath:      getEnv("GRPC_TLS_CERT_PATH", ""),
			TLSKeyPath:       getEnv("GRPC_TLS_KEY_PATH", ""),
			ClientCACertPath: getEnv("GRPC_CLIENT_CA_CERT_PATH", ""),
		},
	}
}
