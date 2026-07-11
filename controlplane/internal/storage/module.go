package storage

import (
	"errors"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageRepoImpl "controlplane/internal/storage/repository"
	storageSvcImpl "controlplane/internal/storage/service"
	storageHandler "controlplane/internal/storage/transport/http/handler"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// [COMMENT]: StorageModule quản lý vòng đời và Dependency Injection cho phân hệ Storage.
type StorageModule struct {
	enabled    bool
	err        error // [COMMENT]: Lưu vết lỗi khởi tạo của phân hệ Storage phục vụ SRE monitoring.
	cfg         *config.Config
	db          *pgxpool.Pool
	rds         *goredis.Client
	L1Registry  *cacheengine.CacheRegistry

	// HTTP Transport Handlers
	PersonalBucketHandler     *storageHandler.PersonalBucketHandler
	TenantBucketHandler       *storageHandler.TenantBucketHandler
	PersonalCredentialHandler *storageHandler.PersonalCredentialHandler
	TenantCredentialHandler   *storageHandler.TenantCredentialHandler

	// Core Services
	TenantBucketService       storageSvcInterface.TenantBucketService
	PersonalBucketService      storageSvcInterface.PersonalBucketService
	TenantCredentialService   storageSvcInterface.TenantCredentialService
	PersonalCredentialService storageSvcInterface.PersonalCredentialService

	// Repositories
	TenantBucketRepo       storageRepoInterface.TenantBucketRepo
	PersonalBucketRepo      storageRepoInterface.PersonalBucketRepo
	TenantCredentialRepo   storageRepoInterface.TenantCredentialRepo
	PersonalCredentialRepo  storageRepoInterface.PersonalCredentialRepo
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

	// ------------------------------------------------------------------------
	// 🧱 GIAI ĐOẠN ĐẤU NỐI (WIRING GRAPH SETUP WITH FAIL-FAST CHECKS)
	// ------------------------------------------------------------------------

	// 1. Khởi tạo repositories riêng biệt theo scope
	tenantBucketRepo := storageRepoImpl.NewTenantBucketRepo(db, cfg.SchemaSQL.Storage)
	if tenantBucketRepo == nil {
		return nil, errors.New("storage module: failed to construct tenant bucket repository")
	}
	personalBucketRepo := storageRepoImpl.NewPersonalBucketRepo(db, cfg.SchemaSQL.Storage)
	if personalBucketRepo == nil {
		return nil, errors.New("storage module: failed to construct personal bucket repository")
	}
	tenantCredentialRepo := storageRepoImpl.NewTenantCredentialRepo(db, cfg.SchemaSQL.Storage)
	if tenantCredentialRepo == nil {
		return nil, errors.New("storage module: failed to construct tenant credential repository")
	}
	personalCredentialRepo := storageRepoImpl.NewPersonalCredentialRepo(db, cfg.SchemaSQL.Storage)
	if personalCredentialRepo == nil {
		return nil, errors.New("storage module: failed to construct personal credential repository")
	}

	// 2. Khởi tạo services tách biệt theo scope
	tenantBucketSvc := storageSvcImpl.NewTenantBucketService(tenantBucketRepo)
	if tenantBucketSvc == nil {
		return nil, errors.New("storage module: failed to construct tenant bucket service")
	}
	personalBucketSvc := storageSvcImpl.NewPersonalBucketService(personalBucketRepo)
	if personalBucketSvc == nil {
		return nil, errors.New("storage module: failed to construct personal bucket service")
	}
	tenantCredentialSvc := storageSvcImpl.NewTenantCredentialService(tenantCredentialRepo, tenantBucketRepo, cfg.Security.RuntimeMasterKey)
	if tenantCredentialSvc == nil {
		return nil, errors.New("storage module: failed to construct tenant credential service")
	}
	personalCredentialSvc := storageSvcImpl.NewPersonalCredentialService(personalCredentialRepo, personalBucketRepo, cfg.Security.RuntimeMasterKey)
	if personalCredentialSvc == nil {
		return nil, errors.New("storage module: failed to construct personal credential service")
	}

	// 3. Khởi tạo HTTP handlers
	personalBucketHandler := storageHandler.NewPersonalBucketHandler(personalBucketSvc)
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
		enabled:                   true,
		cfg:                       cfg,
		db:                        db,
		rds:                       rds,
		L1Registry:                cacheEngine,
		TenantBucketRepo:          tenantBucketRepo,
		PersonalBucketRepo:         personalBucketRepo,
		TenantCredentialRepo:      tenantCredentialRepo,
		PersonalCredentialRepo:     personalCredentialRepo,
		TenantBucketService:       tenantBucketSvc,
		PersonalBucketService:     personalBucketSvc,
		TenantCredentialService:   tenantCredentialSvc,
		PersonalCredentialService: personalCredentialSvc,
		PersonalBucketHandler:     personalBucketHandler,
		TenantBucketHandler:       tenantBucketHandler,
		PersonalCredentialHandler: personalCredentialHandler,
		TenantCredentialHandler:   tenantCredentialHandler,
	}, nil
}
