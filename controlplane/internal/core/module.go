// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/module.go
//            Đặc Tả Quản Lý Vòng Đời & Inject Dependency Core Module
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & BOOTSTRAP AN TOÀN (DESIGN CONTRACT & LIFECYCLE):
//   - Đóng vai trò là trung tâm lắp ghép đồ thị phụ thuộc (Dependency Graph Builder) của Core Module.
//   - Quản lý vòng đời chạy nền (Start/Stop) của các tiến trình background an toàn:
//
//     1) GRACEFUL LIFECYCLE MANAGEMENT:
//        * Đảm bảo mọi background workers (Invalidation Bus, Redis Subscriber)
//          đều được kiểm soát bởi các context cancellation biệt lập.
//        * Tắt gracefully sạch sẽ toàn bộ tài nguyên khi hệ thống shutdown, ngăn chặn rò rỉ RAM/Socket.
//
//     2) PORTABLE gRPC REGISTRATION PORT:
//        * Cung cấp phương thức `RegisterGRPCServices` cho tầng app bootstrap đăng ký các API RPC của Core.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Định hình toàn bộ sơ đồ wiring của Core Module.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Ranh giới thiết kế DI (Dependency Injection) khép kín. Không phơi bày cấu trúc persistance thô ra ngoài.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Bất kỳ lỗi khởi tạo dependency bắt buộc (DB/Redis) sẽ chặn đứng tiến trình chạy và ném lỗi sớm (Fail-Fast).
//
// ======================================================================================================

package core

import (
	"context"
	"fmt"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreRepoImpl "controlplane/internal/core/repository"
	coreSvcImpl "controlplane/internal/core/service"
	coreHandler "controlplane/internal/core/transport/http/handler"
	coreRpcHandler "controlplane/internal/core/transport/rpc/handler"
	coreProto "controlplane/internal/core/transport/rpc/proto"
	"controlplane/internal/security/ratelimit"
	"controlplane/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

type Module struct {
	cfg                   *config.Config
	SecretRepository      coreRepoInterface.SecretRepository
	SecretRotationService coreSvcInterface.SecretRotationService
	ZoneRepository        coreRepoInterface.ZoneRepository
	ZoneService           coreSvcInterface.ZoneService
	ZoneHandler           *coreHandler.ZoneHandler
	BackpressureService   coreSvcInterface.BackpressureService
	listenCancel          context.CancelFunc
	rateLimiter           *ratelimit.Bucket
	L1Registry            *cacheengine.CacheRegistry
}

// NewModule dựng dependency graph của Core và trả về Module hoàn chỉnh.
// Ở đây chúng ta chỉ nhận duy nhất thực thể cacheEngine để truyền vào các service nội bộ.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	rateLimiter *ratelimit.Bucket,
	cacheEngine *cacheengine.CacheRegistry,
) (*Module, error) {
	if cfg == nil {
		return nil, fmt.Errorf("core module: config is required")
	}
	if db == nil {
		return nil, fmt.Errorf("core module: database pool is required")
	}

	// 1) SoT data access for secret lifecycle.
	repo := coreRepoImpl.NewSecretRepository(cfg, db)
	// 3) Rotation orchestration - Chỉ truyền một đối tượng cacheEngine duy nhất
	rotationService := coreSvcImpl.NewSecretRotationService(repo, cacheEngine)
	zoneRepo := coreRepoImpl.NewZoneRepoImpl(cfg, db)
	if zoneRepo == nil {
		return nil, fmt.Errorf("core module: zone service unavailable: zone repository is nil")
	}
	// 5) Zone management service - Chỉ truyền một đối tượng cacheEngine duy nhất
	zoneService := coreSvcImpl.NewZoneService(zoneRepo, cacheEngine)
	zoneHandler := coreHandler.NewZoneHandler(zoneService)
	if zoneHandler == nil {
		return nil, fmt.Errorf("core module: zone handler is nil")
	}

	// 6) Backpressure service (zone-scoped, no node-level tracking)
	backpressureSvc := coreSvcImpl.NewBackpressureService(cacheEngine)
	if backpressureSvc == nil {
		return nil, fmt.Errorf("core module: backpressure service is nil")
	}

	m := &Module{
		cfg:                   cfg,
		SecretRepository:      repo,
		SecretRotationService: rotationService,
		ZoneRepository:        zoneRepo,
		ZoneService:           zoneService,
		ZoneHandler:           zoneHandler,
		BackpressureService:   backpressureSvc,
		rateLimiter:           rateLimiter,
		L1Registry:            cacheEngine,
	}

	return m, nil
}

// RegisterGRPCServices phơi ra phương thức đăng ký grpc services phục vụ app bootstrap layer.
func (m *Module) RegisterGRPCServices(server *grpc.Server) {
	if m == nil {
		return
	}
	if m.BackpressureService != nil {
		handler := coreRpcHandler.NewBackpressureGRPCHandler(m.BackpressureService)
		coreProto.RegisterBackpressureServiceServer(server, handler)
		logger.SysInfo("grpc", "registered BackpressureService onto gRPC server")
	}
	if m.ZoneService != nil {
		zoneHandler := coreRpcHandler.NewZoneGRPCHandler(m.ZoneService)
		coreProto.RegisterZoneServiceServer(server, zoneHandler)
		logger.SysInfo("grpc", "registered ZoneService onto gRPC server")
	}
}

// Bootstrap khởi tạo các side-effect lâu dài và chạy các background task của module Core.
func (m *Module) Bootstrap(ctx context.Context) error {
	return nil
}

// Stop hủy các background goroutine của module Core an toàn.
func (m *Module) Stop() {
	if m == nil {
		return
	}
	if m.listenCancel != nil {
		m.listenCancel()
		m.listenCancel = nil
	}
}
