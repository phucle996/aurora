package deltaengine

import (
	"context"
	"fmt"
	"time"

	coreSvcInterface "controlplane/internal/core/domain/service"
	"controlplane/internal/delta-engine/broker"
	"controlplane/internal/delta-engine/reconcile"
	"controlplane/internal/delta-engine/runtime"
	"controlplane/internal/delta-engine/types"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// DeltaEngine đóng vai trò là nhạc trưởng điều phối toàn bộ hạ tầng Delta Engine (5-Pillar Model).
// Kết nối DB + Outbox + NATS + Local RAM (COW) + Reconciler thành một khối thống nhất.
type DeltaEngine struct {
	holder     *runtime.SnapshotHolder
	bus        broker.ConfigEventBus
	reconciler *reconcile.Reconciler
	outboxSvc  coreSvcInterface.OutboxService
	rdb        *redis.Client
}

// NewDeltaEngine khởi tạo DeltaEngine với các thành phần được decouple hoàn toàn qua interfaces và Redis Client cho HA locking.
func NewDeltaEngine(bus broker.ConfigEventBus, outboxSvc coreSvcInterface.OutboxService, rdb *redis.Client) *DeltaEngine {
	holder := runtime.NewSnapshotHolder()
	reconciler := reconcile.NewReconciler(holder, 30*time.Second)

	return &DeltaEngine{
		holder:     holder,
		bus:        bus,
		reconciler: reconciler,
		outboxSvc:  outboxSvc,
		rdb:        rdb,
	}
}

// Start khởi động toàn bộ động cơ: đăng ký bus event và khởi chạy vòng lặp Reconcile ngầm.
func (e *DeltaEngine) Start(ctx context.Context) error {
	logger.SysInfo("delta-engine", "starting high-performance synchronization engine")

	// Đăng ký nhận các Delta Event từ cụm NATS JetStream để áp dụng vào bộ nhớ
	err := e.bus.Subscribe(ctx, func(event types.DeltaEvent) {
		logger.SysInfoFields("delta-engine", "received cluster delta mutation event", logger.Fields{
			"entity":  event.Entity,
			"op":      string(event.Op),
			"version": event.Version,
		})

		e.holder.Update(func(snap *runtime.RuntimeSnapshot) *runtime.RuntimeSnapshot {
			next := snap.Clone()
			if err := runtime.ApplyMutation(next, event); err != nil {
				logger.SysError("delta-engine", fmt.Sprintf("failed to apply incoming mutation: %v", err))
				return snap // Nếu lỗi, trả về snapshot cũ an toàn (rollback)
			}
			return next
		})
	})
	if err != nil {
		return fmt.Errorf("delta-engine: subscription on cluster bus failed: %w", err)
	}

	// Chạy Reconciler ngầm bảo vệ dữ liệu chống mất mát (Self-Healing)
	go e.reconciler.Start(ctx)

	// Chạy Outbox Worker ngầm định kỳ quét DB và đẩy lên NATS JetStream
	if e.outboxSvc != nil {
		go e.startOutboxWorker(ctx)
	}

	return nil
}

func (e *DeltaEngine) startOutboxWorker(ctx context.Context) {
	logger.SysInfo("delta-engine", "starting background outbox batcher worker with distributed coordinator")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.SysInfo("delta-engine", "stopping background outbox batcher worker gracefully")
			return
		case <-ticker.C:
			if e.rdb != nil {
				// Cố gắng giành quyền xử lý qua khóa phân tán Redis (Distributed Lock)
				// Đặt TTL khóa là 4 giây (ngắn hơn chu kỳ ticker 5 giây) để tự giải phóng nếu node bị crash đột ngột
				lockKey := "lock:delta-engine:outbox-worker"
				nodeID := uuid.New().String() // Định danh duy nhất cho node của luồng này

				acquired, err := e.rdb.SetNX(ctx, lockKey, nodeID, 4*time.Second).Result()
				if err != nil {
					logger.SysWarnFields("delta-engine", "failed to acquire redis lock for outbox worker", err, nil)
					continue
				}

				if !acquired {
					// Node khác đã giành quyền xử lý chu kỳ này -> Bỏ qua mượt mà (Graceful Skip)
					continue
				}

				// Đã giành quyền xử lý thành công -> Thực thi nghiệp vụ outbox
				if err := e.outboxSvc.ProcessPending(ctx, 50); err != nil {
					logger.SysErrorFields("delta-engine", "outbox batcher execution failed", err, nil)
				}

				// Sử dụng tập lệnh Lua để giải phóng khóa nguyên tử (Atomic Lock Release)
				// Đảm bảo node chỉ giải phóng khóa do chính mình nắm giữ
				releaseScript := `
					if redis.call("get", KEYS[1]) == ARGV[1] then
						return redis.call("del", KEYS[1])
					else
						return 0
					end
				`
				_, _ = e.rdb.Eval(ctx, releaseScript, []string{lockKey}, nodeID).Result()
			} else {
				// Dự phòng khi không có Redis (ví dụ: Standalone Testing)
				if err := e.outboxSvc.ProcessPending(ctx, 50); err != nil {
					logger.SysErrorFields("delta-engine", "outbox batcher execution failed", err, nil)
				}
			}
		}
	}
}

// Current trả về Snapshot hiện tại trong bộ nhớ RAM phục vụ cho Hot-Path nghiệp vụ (Lock-Free).
func (e *DeltaEngine) Current() *runtime.RuntimeSnapshot {
	return e.holder.Get()
}
