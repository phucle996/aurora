// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/cache/zone_cache.go
//            Zone L2 RAM Cache với COW Atomic Swap và TTL
// ======================================================================================================
//
// 📜 THIẾT KẾ:
//   - ZoneFanoutCache đóng gói toàn bộ logic hạ tầng cache:
//       * Đọc lock-free O(1) từ local RAM snapshot
//       * Tự quản lý và sinh version tuần tự qua Redis (IncrVersion) để chống Out-of-Order
//       * Tự động publish mutation event qua Redis Pub/Sub (fanout)
//
//   - Interface sạch dành cho Service layer:
//       * GetByID / GetByCode / GetCatalog → đọc RAM
//       * PatchZone(ctx, zone) → tự sinh version → apply local RAM → publish event
//       * EvictZone(ctx, id, code) → tự sinh version → apply local RAM → publish event
//
//   - HandleFanout(op, payload, version) là subscriber callback:
//       * Chỉ gọi ApplyUpsert/ApplyDelete nội bộ (local mutation) để tránh tạo vòng lặp phát event.
//
// ======================================================================================================

package coreCache

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	"controlplane/pkg/logger"
)

// DefaultZoneCacheTTL là TTL mặc định cho Zone L2 cache.
const DefaultZoneCacheTTL = 5 * time.Minute

// ZoneVersionKey là key lưu trữ cache version của zone trong Redis.
const ZoneVersionKey = "core:zone:version"

// zoneSnapshot là bản sao bất biến của toàn bộ trạng thái zone trong RAM.
type zoneSnapshot struct {
	byID      map[string]coreEntity.Zone  // "zone:id:<id>"    → Zone
	byCode    map[string]coreEntity.Zone  // "zone:code:<code>" → Zone
	catalog   []coreEntity.ZoneCatalog   // computed từ byID, sorted by code
	expiresAt time.Time                  // TTL sentinel
	version   int64                      // Phiên bản tăng tuần tự của RAM cache
}

func newZoneSnapshot(ttl time.Duration) *zoneSnapshot {
	return &zoneSnapshot{
		byID:      make(map[string]coreEntity.Zone),
		byCode:    make(map[string]coreEntity.Zone),
		catalog:   []coreEntity.ZoneCatalog{},
		expiresAt: time.Now().Add(ttl),
		version:   0,
	}
}

// clone shallow-copies snapshot. TTL kế thừa để không reset khi mid-write clone.
func (s *zoneSnapshot) clone() *zoneSnapshot {
	next := &zoneSnapshot{
		byID:      make(map[string]coreEntity.Zone, len(s.byID)),
		byCode:    make(map[string]coreEntity.Zone, len(s.byCode)),
		expiresAt: s.expiresAt,
		version:   s.version,
	}
	for k, v := range s.byID {
		next.byID[k] = v
	}
	for k, v := range s.byCode {
		next.byCode[k] = v
	}
	return next
}

func (s *zoneSnapshot) rebuildCatalog() {
	var catalog []coreEntity.ZoneCatalog
	for _, z := range s.byID {
		if z.Status != coreEntity.ZoneStatusDisabled && z.Status != coreEntity.ZoneStatusPlanned {
			catalog = append(catalog, coreEntity.ZoneCatalog{ID: z.ID, Code: z.Code, Name: z.Name})
		}
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Code < catalog[j].Code })
	s.catalog = catalog
}

// ZoneFanoutCache quản lý RAM L2 cache, versioning, và giao tiếp Redis fanout.
type ZoneFanoutCache struct {
	ptr    atomic.Pointer[zoneSnapshot]
	mu     sync.Mutex
	ttl    time.Duration
	fanout *RedisFanoutBus
}

func NewZoneFanoutCache(fanout *RedisFanoutBus) *ZoneFanoutCache {
	c := &ZoneFanoutCache{ttl: DefaultZoneCacheTTL, fanout: fanout}
	c.ptr.Store(newZoneSnapshot(c.ttl))
	return c
}

func NewZoneFanoutCacheWithTTL(fanout *RedisFanoutBus, ttl time.Duration) *ZoneFanoutCache {
	c := &ZoneFanoutCache{ttl: ttl, fanout: fanout}
	c.ptr.Store(newZoneSnapshot(c.ttl))
	return c
}

// ── READ PATH (lock-free) ──────────────────────────────────────────────────

func (c *ZoneFanoutCache) isExpired() bool {
	return time.Now().After(c.ptr.Load().expiresAt)
}

func (c *ZoneFanoutCache) GetByID(id string) (coreEntity.Zone, bool) {
	if c.isExpired() {
		return coreEntity.Zone{}, false
	}
	z, ok := c.ptr.Load().byID["zone:id:"+id]
	return z, ok
}

func (c *ZoneFanoutCache) GetByCode(code string) (coreEntity.Zone, bool) {
	if c.isExpired() {
		return coreEntity.Zone{}, false
	}
	z, ok := c.ptr.Load().byCode["zone:code:"+code]
	return z, ok
}

func (c *ZoneFanoutCache) GetCatalog() ([]coreEntity.ZoneCatalog, bool) {
	if c.isExpired() {
		return nil, false
	}
	snap := c.ptr.Load()
	if len(snap.byID) == 0 {
		return nil, false // chưa warm
	}
	return snap.catalog, true
}

// SetCatalog warm-up sau khi DB query hoàn tất — nạp danh sách catalog vào RAM.
func (c *ZoneFanoutCache) SetCatalog(catalog []coreEntity.ZoneCatalog, version int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	current := c.ptr.Load()
	if version < current.version {
		return // Dữ liệu cũ -> bỏ qua
	}

	next := current.clone()
	next.catalog = catalog
	next.expiresAt = time.Now().Add(c.ttl)
	next.version = version
	c.ptr.Store(next)
}

// GetVersion lấy phiên bản hiện tại từ Redis phục vụ quá trình Warm cache.
func (c *ZoneFanoutCache) GetVersion(ctx context.Context) int64 {
	if c.fanout == nil {
		return 0
	}
	v, _ := c.fanout.GetVersion(ctx, ZoneVersionKey)
	return v
}

// ── WRITE PATH (COW + Redis Fanout) ────────────────────────────────────────

// PatchZone cập nhật local cache và phát tín hiệu update (upsert) sang các replica.
func (c *ZoneFanoutCache) PatchZone(ctx context.Context, zone coreEntity.Zone) {
	version := c.nextVersion(ctx)
	
	// 1. Cập nhật local RAM cache lập tức
	c.ApplyUpsert(zone, version)
	
	// 2. Publish ra các replica khác
	if c.fanout != nil {
		if err := c.fanout.Publish(ctx, FanoutOpUpsert, zone, version); err != nil {
			logger.SysWarnFields("zone_cache", "failed to publish zone upsert fanout", err, logger.Fields{"zone_id": zone.ID, "version": version})
		}
	}
}

// EvictZone loại bỏ zone khỏi local cache và phát tín hiệu delete sang các replica.
func (c *ZoneFanoutCache) EvictZone(ctx context.Context, id, code string) {
	version := c.nextVersion(ctx)
	
	// 1. Xóa local RAM cache lập tức
	c.ApplyDelete(id, code, version)
	
	// 2. Publish ra các replica khác
	if c.fanout != nil {
		payload := struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		}{ID: id, Code: code}
		if err := c.fanout.Publish(ctx, FanoutOpDelete, payload, version); err != nil {
			logger.SysWarnFields("zone_cache", "failed to publish zone delete fanout", err, logger.Fields{"zone_id": id, "version": version})
		}
	}
}

// ── MUTATIONS NỘI BỘ (Chỉ tác động local RAM) ──────────────────────────────

// ApplyUpsert patch zone vào local snapshot RAM (không tự trigger Publish).
func (c *ZoneFanoutCache) ApplyUpsert(zone coreEntity.Zone, version int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.ptr.Load()
	if version <= current.version {
		return
	}

	next := current.clone()
	next.byID["zone:id:"+zone.ID] = zone
	next.byCode["zone:code:"+zone.Code] = zone
	next.expiresAt = time.Now().Add(c.ttl)
	next.version = version
	next.rebuildCatalog()
	c.ptr.Store(next)
}

// ApplyDelete evict zone khỏi local snapshot RAM (không tự trigger Publish).
func (c *ZoneFanoutCache) ApplyDelete(id, code string, version int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.ptr.Load()
	if version <= current.version {
		return
	}

	next := current.clone()
	delete(next.byID, "zone:id:"+id)
	delete(next.byCode, "zone:code:"+code)
	next.expiresAt = time.Now().Add(c.ttl)
	next.version = version
	next.rebuildCatalog()
	c.ptr.Store(next)
}

// nextVersion lấy version kế tiếp từ Redis (hoặc fallback UnixNano).
func (c *ZoneFanoutCache) nextVersion(ctx context.Context) int64 {
	if c.fanout == nil {
		return time.Now().UnixNano()
	}
	v, err := c.fanout.IncrVersion(ctx, ZoneVersionKey)
	if err != nil {
		logger.SysWarnFields("zone_cache", "failed to increment version, fallback to UnixNano", err, nil)
		return time.Now().UnixNano()
	}
	return v
}

// ── FANOUT RECEIVE PATH ────────────────────────────────────────────────────

// HandleFanout được gọi bởi subscriber của module khi nhận được tin nhắn từ Redis Pub/Sub.
// Chỉ gọi mutation local RAM để không tạo loop feedback.
func (c *ZoneFanoutCache) HandleFanout(op FanoutOp, payload json.RawMessage, version int64) {
	switch op {
	case FanoutOpUpsert:
		var zone coreEntity.Zone
		if err := json.Unmarshal(payload, &zone); err != nil {
			logger.SysWarnFields("zone_cache", "fanout: failed to unmarshal upsert payload", err, nil)
			return
		}
		c.ApplyUpsert(zone, version)
		logger.SysInfoFields("zone_cache", "fanout: applied upsert", logger.Fields{"zone_id": zone.ID, "version": version})

	case FanoutOpDelete:
		var ref struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		}
		if err := json.Unmarshal(payload, &ref); err != nil {
			logger.SysWarnFields("zone_cache", "fanout: failed to unmarshal delete payload", err, nil)
			return
		}
		c.ApplyDelete(ref.ID, ref.Code, version)
		logger.SysInfoFields("zone_cache", "fanout: applied delete", logger.Fields{"zone_id": ref.ID, "version": version})
	}
}
