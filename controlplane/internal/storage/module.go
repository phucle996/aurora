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
	BucketHandler     *storageHandler.BucketHandler
	CredentialHandler *storageHandler.CredentialHandler

	// Core Services
	BucketService     storageSvcInterface.BucketService
	CredentialService storageSvcInterface.CredentialService

	// Repositories
	BucketRepo     storageRepoInterface.BucketRepo
	CredentialRepo storageRepoInterface.CredentialRepo
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
	// 🧱 GIAI ĐOẠN ĐẤU NỐI (WIRING GRAPH SETUP)
	// ------------------------------------------------------------------------

	// 1. Khởi tạo repositories
	bucketRepo := storageRepoImpl.NewBucketRepo(db)
	credentialRepo := storageRepoImpl.NewCredentialRepo(db)

	// 2. Khởi tạo services
	bucketSvc := storageSvcImpl.NewBucketService(bucketRepo)
	credentialSvc := storageSvcImpl.NewCredentialService(credentialRepo)

	// 3. Khởi tạo HTTP handlers
	bucketHandler := storageHandler.NewBucketHandler(bucketSvc)
	credentialHandler := storageHandler.NewCredentialHandler(credentialSvc)

	return &StorageModule{
		enabled:           true,
		cfg:               cfg,
		db:                db,
		rds:               rds,
		L1Registry:        cacheEngine,
		BucketRepo:        bucketRepo,
		CredentialRepo:    credentialRepo,
		BucketService:     bucketSvc,
		CredentialService: credentialSvc,
		BucketHandler:     bucketHandler,
		CredentialHandler: credentialHandler,
	}, nil
}
