// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/module.go
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
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"

	coreRepoInterface "controlplane/internal/hierarchy/domain/repo"
	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreRepoImpl "controlplane/internal/hierarchy/repository"
	coreSvcImpl "controlplane/internal/hierarchy/service"
	zoneHandler "controlplane/internal/hierarchy/transport/http/handler"
	zoneRpcHandler "controlplane/internal/hierarchy/transport/rpc/handler"
	zoneProto "controlplane/internal/hierarchy/transport/rpc/proto"

	"controlplane/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

type Module struct {
	cfg                         *config.Config
	rds                         *goredis.Client
	ZoneRepository      coreRepoInterface.ZoneRepository
	ZoneService         coreSvcInterface.ZoneService
	ZoneHandler         *zoneHandler.ZoneHandler
	// [COMMENT]: Chia ranh giới DB access thành 2 repositories Tenant và Personal
	TenantWorkspaceRepository   coreRepoInterface.TenantWorkspaceRepository
	PersonalWorkspaceRepository coreRepoInterface.PersonalWorkspaceRepository
	// [COMMENT]: Chia ranh giới Service Layer thành 2 services Tenant và Personal
	TenantWorkspaceService      coreSvcInterface.TenantWorkspaceService
	PersonalWorkspaceService    coreSvcInterface.PersonalWorkspaceService
	WorkspaceHandler    *zoneHandler.WorkspaceHandler
	TenantRepository    coreRepoInterface.TenantRepository
	TenantService       coreSvcInterface.TenantService
	TenantHandler       *zoneHandler.TenantHandler
	listenCancel        context.CancelFunc
	L1Registry          *cacheengine.CacheRegistry
}

// NewModule dựng dependency graph của Core và trả về Module hoàn chỉnh.
// Ở đây chúng ta chỉ nhận duy nhất thực thể cacheEngine để truyền vào các service nội bộ.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	cacheEngine *cacheengine.CacheRegistry,
) (*Module, error) {
	if cfg == nil {
		return nil, fmt.Errorf("zone module: config is required")
	}
	if db == nil {
		return nil, fmt.Errorf("zone module: database pool is required")
	}

	// 1) SoT data access for secret lifecycle.
	// 3) Rotation orchestration - Chỉ truyền một đối tượng cacheEngine duy nhất
	zoneRepo := coreRepoImpl.NewZoneRepoImpl(cfg, db)
	if zoneRepo == nil {
		return nil, fmt.Errorf("zone module: zone service unavailable: zone repository is nil")
	}

	// 5) Zone management service - Chỉ truyền một đối tượng cacheEngine duy nhất
	zoneService := coreSvcImpl.NewZoneService(zoneRepo, rds)
	if zoneService == nil {
		return nil, fmt.Errorf("zone module: zone service unavailable: zone service is nil")
	}
	zHandler := zoneHandler.NewZoneHandler(zoneService)
	if zHandler == nil {
		return nil, fmt.Errorf("zone module: zone handler is nil")
	}

	// 6) Workspace management — repo, service, handler (Chia 2 dòng chảy Tenant và Personal)
	tenantWorkspaceRepo := coreRepoImpl.NewTenantWorkspaceRepoImpl(cfg, db)
	if tenantWorkspaceRepo == nil {
		return nil, fmt.Errorf("hierarchy module: tenant workspace repository is nil")
	}
	personalWorkspaceRepo := coreRepoImpl.NewPersonalWorkspaceRepoImpl(cfg, db)
	if personalWorkspaceRepo == nil {
		return nil, fmt.Errorf("hierarchy module: personal workspace repository is nil")
	}

	tenantWorkspaceService := coreSvcImpl.NewTenantWorkspaceService(tenantWorkspaceRepo, cacheEngine)
	if tenantWorkspaceService == nil {
		return nil, fmt.Errorf("hierarchy module: tenant workspace service is nil")
	}
	personalWorkspaceService := coreSvcImpl.NewPersonalWorkspaceService(personalWorkspaceRepo)
	if personalWorkspaceService == nil {
		return nil, fmt.Errorf("hierarchy module: personal workspace service is nil")
	}
	wHandler := zoneHandler.NewWorkspaceHandler(tenantWorkspaceService, personalWorkspaceService)
	if wHandler == nil {
		return nil, fmt.Errorf("hierarchy module: workspace handler is nil")
	}

	// 7) Tenant management — repo, service, handler
	tenantRepo := coreRepoImpl.NewTenantRepoImpl(cfg, db)
	if tenantRepo == nil {
		return nil, fmt.Errorf("hierarchy module: tenant repository is nil")
	}
	tenantService := coreSvcImpl.NewTenantService(tenantRepo)
	if tenantService == nil {
		return nil, fmt.Errorf("hierarchy module: tenant service is nil")
	}
	tHandler := zoneHandler.NewTenantHandler(tenantService)
	if tHandler == nil {
		return nil, fmt.Errorf("hierarchy module: tenant handler is nil")
	}

	// [COMMENT]: Lược bỏ việc khởi tạo BackpressureService do đã chuyển đổi hoàn toàn sang mô hình event-driven ở job-orchestrator.

	return &Module{
		cfg:                         cfg,
		rds:                         rds,
		ZoneRepository:              zoneRepo,
		ZoneService:                 zoneService,
		ZoneHandler:                 zHandler,
		TenantWorkspaceRepository:   tenantWorkspaceRepo,
		PersonalWorkspaceRepository: personalWorkspaceRepo,
		TenantWorkspaceService:      tenantWorkspaceService,
		PersonalWorkspaceService:    personalWorkspaceService,
		WorkspaceHandler:            wHandler,
		TenantRepository:            tenantRepo,
		TenantService:               tenantService,
		TenantHandler:               tHandler,
		L1Registry:                  cacheEngine,
	}, nil
}

// RegisterGRPCServices phơi ra phương thức đăng ký grpc services phục vụ app bootstrap layer.
func (m *Module) RegisterGRPCServices(server *grpc.Server) {
	if m == nil {
		return
	}
	// [COMMENT]: Loại bỏ RegisterBackpressureServiceServer do luồng báo tải gRPC đã được dọn dẹp theo God View.
	if m.ZoneService != nil {
		zoneHandler := zoneRpcHandler.NewZoneGRPCHandler(m.ZoneService)
		zoneProto.RegisterZoneServiceServer(server, zoneHandler)
		logger.SysInfo("grpc", "registered ZoneService onto gRPC server")
	}
}

// Bootstrap khởi tạo các side-effect lâu dài và chạy các background task của module Core.
func (m *Module) Bootstrap(ctx context.Context) error {
	if m == nil || m.rds == nil {
		return nil
	}

	// [COMMENT]: Khởi tạo sub-context riêng biệt để kiểm soát luồng background listener
	subCtx, cancel := context.WithCancel(ctx)
	m.listenCancel = cancel

	go func() {
		pubsub := m.rds.Subscribe(subCtx, "gateway:sync:requests")
		defer pubsub.Close()

		logger.SysInfo("hierarchy.pubsub", "Gateway sync requests listener started.")

		ch := pubsub.Channel()
		for {
			select {
			case <-subCtx.Done():
				logger.SysInfo("hierarchy.pubsub", "Gateway sync requests listener stopped (context done).")
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				logger.SysInfoFields("hierarchy.pubsub", "Received sync request from edge", logger.Fields{
					"payload": msg.Payload,
				})
				m.handleSyncRequest(subCtx, msg.Payload)
			}
		}
	}()

	return nil
}

// handleSyncRequest phân tích yêu cầu từ Edge, đọc database và publish ngược lại gateway:sync
func (m *Module) handleSyncRequest(ctx context.Context, payload string) {
	// Parse thủ công đơn giản: {"type": "zone", "code": "vn"}
	// Lọc code
	var code string
	if idx := strings.Index(payload, `"code"`); idx != -1 {
		sub := payload[idx:]
		if start := strings.Index(sub, `":"`); start != -1 {
			valSub := sub[start+3:]
			if end := strings.Index(valSub, `"`); end != -1 {
				code = valSub[:end]
			}
		}
	}
	if code == "" {
		return
	}

	// Đọc database lấy detail để sync
	if m.ZoneService != nil {
		zones, err := m.ZoneService.RPCListZones(ctx)
		if err == nil {
			for _, z := range zones {
				if z.Code == code {
					// Ghi lại Redis L2
					redisKey := fmt.Sprintf("zone:code:%s", z.Code)
					val := fmt.Sprintf("%s:%s", z.ID, z.Status)
					_ = m.rds.Set(ctx, redisKey, val, 24*time.Hour).Err()

					// Broadcast invalidation qua gateway:sync để Gateway reload
					_ = m.rds.Publish(ctx, "gateway:sync", fmt.Sprintf(`{"type": "zone", "code": "%s"}`, z.Code)).Err()
					logger.SysInfoFields("hierarchy.pubsub", "Successfully responded and warmed up cache for zone", logger.Fields{
						"code": code,
					})
					return
				}
			}
		}
	}
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
