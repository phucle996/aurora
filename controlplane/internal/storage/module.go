package storage

import (
	"errors"
	"strings"

	kafkainfra "controlplane/infra/kafka"
	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	"controlplane/internal/observability"
	jobpayload "controlplane/internal/security"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageRepoImpl "controlplane/internal/storage/repository"
	storageSvcImpl "controlplane/internal/storage/service"
	storageHandler "controlplane/internal/storage/transport/http/handler"
	storageProto "controlplane/internal/storage/transport/proto"
	storageStream "controlplane/internal/storage/transport/stream"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// [COMMENT]: StorageModule quản lý vòng đời và Dependency Injection cho phân hệ Storage.
type StorageModule struct {
	enabled    bool
	err        error // [COMMENT]: Lưu vết lỗi khởi tạo của phân hệ Storage phục vụ SRE monitoring.
	cfg        *config.Config
	db         *pgxpool.Pool
	rds        *goredis.Client
	L1Registry *cacheengine.CacheRegistry

	// HTTP Transport Handlers
	PersonalBucketHandler     *storageHandler.PersonalBucketHandler
	TenantBucketHandler       *storageHandler.TenantBucketHandler
	PersonalCredentialHandler *storageHandler.PersonalCredentialHandler
	TenantCredentialHandler   *storageHandler.TenantCredentialHandler

	// Core Services
	TenantBucketService                 storageSvcInterface.TenantBucketService
	PersonalBucketService               storageSvcInterface.PersonalBucketService
	TenantCredentialService             storageSvcInterface.TenantCredentialService
	PersonalCredentialService           storageSvcInterface.PersonalCredentialService
	PersonalStorageAccessSessionService storageSvcInterface.PersonalStorageAccessSessionService

	// Repositories
	TenantBucketRepo                 storageRepoInterface.TenantBucketRepo
	PersonalBucketRepo               storageRepoInterface.PersonalBucketRepo
	TenantCredentialRepo             storageRepoInterface.TenantCredentialRepo
	PersonalCredentialRepo           storageRepoInterface.PersonalCredentialRepo
	PersonalStorageAccessSessionRepo storageRepoInterface.PersonalStorageAccessSessionRepository

	// Background workflow transports
	CommercialAdmissionProjection *storageStream.CommercialAdmissionProjectionConsumer
}

// [COMMENT]: IsEnabled trả về trạng thái hoạt động của Storage module.
func (m *StorageModule) IsEnabled() bool {
	return m != nil && m.enabled
}

// [COMMENT]: GetError trả về lỗi khởi tạo nếu có của Storage module.
func (m *StorageModule) GetError() error {
	if m == nil {
		return errors.New("storage module is nil")
	}
	return m.err
}

// [COMMENT]: NewDegradedModule khởi tạo một dummy StorageModule lỗi phục vụ chạy chế độ suy giảm (Degraded Mode).
func NewDegradedModule(err error) *StorageModule {
	return &StorageModule{
		enabled: false,
		err:     err,
	}
}

// [COMMENT]: NewModule khởi tạo phân hệ Storage và cấu hình Dependency Injection thủ công (Wiring Graph).
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	cacheEngine *cacheengine.CacheRegistry,
	otel *observability.OTel,
	protector jobpayload.Protector,
	kafkaProducer *kafkainfra.Producer,
) (*StorageModule, error) {

	// ------------------------------------------------------------------------
	// 🛑 KIỂM TRA ĐẦU VÀO (FAIL-FAST POLICY)
	// ------------------------------------------------------------------------
	if cfg == nil {
		return nil, errors.New("storage module: config is nil")
	}
	if db == nil {
		return nil, errors.New("storage module: database pool is nil")
	}
	if rds == nil {
		return nil, errors.New("storage module: redis client is nil")
	}
	if cacheEngine == nil {
		return nil, errors.New("storage module: cache engine registry is nil")
	}
	if otel == nil {
		return nil, errors.New("storage module: observability is nil")
	}
	if protector == nil {
		return nil, errors.New("storage module: job payload protector is nil")
	}
	if kafkaProducer == nil {
		return nil, errors.New("storage module: Kafka producer is nil")
	}
	if strings.TrimSpace(cfg.SchemaSQL.Storage) == "" {
		return nil, errors.New("storage module: SQL schema is empty")
	}
	if strings.TrimSpace(cfg.Kafka.TopicPrefix) == "" {
		return nil, errors.New("storage module: Kafka topic prefix is empty")
	}
	// ------------------------------------------------------------------------
	// 🧱 GIAI ĐOẠN ĐẤU NỐI (WIRING GRAPH SETUP WITH FAIL-FAST CHECKS)
	// ------------------------------------------------------------------------

	// 1. Khởi tạo repositories riêng biệt theo scope
	tenantBucketRepo := storageRepoImpl.NewTenantBucketRepo(db, cfg, protector)
	if tenantBucketRepo == nil {
		return nil, errors.New("storage module: failed to construct tenant bucket repository")
	}
	personalBucketRepo := storageRepoImpl.NewPersonalBucketRepo(db, cfg, protector)
	if personalBucketRepo == nil {
		return nil, errors.New("storage module: failed to construct personal bucket repository")
	}
	tenantCredentialRepo := storageRepoImpl.NewTenantCredentialRepo(db, cfg, protector)
	if tenantCredentialRepo == nil {
		return nil, errors.New("storage module: failed to construct tenant credential repository")
	}
	personalCredentialRepo := storageRepoImpl.NewPersonalCredentialRepo(db, cfg, protector)
	if personalCredentialRepo == nil {
		return nil, errors.New("storage module: failed to construct personal credential repository")
	}
	personalStorageAccessSessionRepo := storageRepoImpl.NewPersonalStorageAccessSessionRepository(db, cfg, protector)
	if personalStorageAccessSessionRepo == nil {
		return nil, errors.New("storage module: failed to construct personal storage access-session repository")
	}
	commercialAdmissionZonePayloadEncoder := storageProto.NewCommercialAdmissionZonePayloadEncoder()
	commercialAdmissionProjectionRepo := storageRepoImpl.NewStorageCommercialAdmissionProjectionRepo(
		db, cfg.SchemaSQL.Storage, commercialAdmissionZonePayloadEncoder, protector,
	)
	commercialAdmissionProjectionSvc := storageSvcImpl.NewStorageCommercialAdmissionProjectionService(commercialAdmissionProjectionRepo)
	commercialAdmissionProjection := storageStream.NewCommercialAdmissionProjectionConsumer(rds, commercialAdmissionProjectionSvc)
	// 2. Khởi tạo services tách biệt theo scope
	workflowMetrics := otel.WorkflowRecorder("storage")
	tenantCredentialSvc := storageSvcImpl.NewTenantCredentialService(tenantCredentialRepo, tenantBucketRepo, workflowMetrics)
	if tenantCredentialSvc == nil {
		return nil, errors.New("storage module: failed to construct tenant credential service")
	}
	personalCredentialSvc := storageSvcImpl.NewPersonalCredentialService(personalCredentialRepo, personalBucketRepo, workflowMetrics)
	if personalCredentialSvc == nil {
		return nil, errors.New("storage module: failed to construct personal credential service")
	}
	tenantBucketSvc := storageSvcImpl.NewTenantBucketService(tenantBucketRepo, tenantCredentialSvc, workflowMetrics)
	if tenantBucketSvc == nil {
		return nil, errors.New("storage module: failed to construct tenant bucket service")
	}
	personalBucketSvc := storageSvcImpl.NewPersonalBucketService(personalBucketRepo, personalCredentialSvc, workflowMetrics)
	if personalBucketSvc == nil {
		return nil, errors.New("storage module: failed to construct personal bucket service")
	}
	personalStorageAccessSessionSvc := storageSvcImpl.NewPersonalStorageAccessSessionService(personalStorageAccessSessionRepo, workflowMetrics)
	if personalStorageAccessSessionSvc == nil {
		return nil, errors.New("storage module: failed to construct personal storage access-session service")
	}

	// 3. Khởi tạo HTTP handlers
	personalBucketHandler := storageHandler.NewPersonalBucketHandler(personalBucketSvc, personalStorageAccessSessionSvc)
	if personalBucketHandler == nil {
		return nil, errors.New("storage module: failed to construct personal bucket handler")
	}
	tenantBucketHandler := storageHandler.NewTenantBucketHandler(tenantBucketSvc)
	if tenantBucketHandler == nil {
		return nil, errors.New("storage module: failed to construct tenant bucket handler")
	}
	personalCredentialHandler := storageHandler.NewPersonalCredentialHandler(personalCredentialSvc)
	if personalCredentialHandler == nil {
		return nil, errors.New("storage module: failed to construct personal credential handler")
	}
	tenantCredentialHandler := storageHandler.NewTenantCredentialHandler(tenantCredentialSvc)
	if tenantCredentialHandler == nil {
		return nil, errors.New("storage module: failed to construct tenant credential handler")
	}

	return &StorageModule{
		enabled:                             true,
		cfg:                                 cfg,
		db:                                  db,
		rds:                                 rds,
		L1Registry:                          cacheEngine,
		TenantBucketRepo:                    tenantBucketRepo,
		PersonalBucketRepo:                  personalBucketRepo,
		TenantCredentialRepo:                tenantCredentialRepo,
		PersonalCredentialRepo:              personalCredentialRepo,
		PersonalStorageAccessSessionRepo:    personalStorageAccessSessionRepo,
		TenantBucketService:                 tenantBucketSvc,
		PersonalBucketService:               personalBucketSvc,
		TenantCredentialService:             tenantCredentialSvc,
		PersonalCredentialService:           personalCredentialSvc,
		PersonalStorageAccessSessionService: personalStorageAccessSessionSvc,
		PersonalBucketHandler:               personalBucketHandler,
		TenantBucketHandler:                 tenantBucketHandler,
		PersonalCredentialHandler:           personalCredentialHandler,
		TenantCredentialHandler:             tenantCredentialHandler,
		CommercialAdmissionProjection:       commercialAdmissionProjection,
	}, nil
}
