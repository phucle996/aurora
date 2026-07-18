package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	planproto "cost-manager/api/internal/transport/proto/planproto"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/proto"
)

// l1CacheItem bao bọc dữ liệu và thời gian hết hạn cho cache L1 (RAM)
type l1CacheItem struct {
	plans     []entity.Plan // Danh sách các plans dạng value struct
	expiredAt time.Time     // Thời gian hết hạn của cache RAM L1
}

// planService triển khai interface billingSvcInterface.PlanService với bộ nhớ đệm 2 lớp (L1 RAM & L2 Redis)
type planService struct {
	planRepo    billingRepoInterface.PlanRepository // Repository kết nối Postgres
	redisClient *redis.Client                       // Client kết nối Redis Cache L2
	sfGroup     *singleflight.Group                 // Group hỗ trợ phòng tránh Cache Stampede cục bộ

	// L1 RAM Cache
	l1Mutex sync.RWMutex            // Mutex bảo vệ map cache L1 trước truy cập đồng thời
	l1Cache map[string]l1CacheItem // Map lưu trữ dữ liệu cache L1
}

// NewPlanService khởi tạo service quản lý Plan với bộ nhớ đệm 2 lớp
func NewPlanService(planRepo billingRepoInterface.PlanRepository, redisClient *redis.Client) billingSvcInterface.PlanService {
	return &planService{
		planRepo:    planRepo,
		redisClient: redisClient,
		sfGroup:     &singleflight.Group{},
		l1Cache:     make(map[string]l1CacheItem),
	}
}

// ListPlans lấy danh sách plans từ L1 RAM Cache, nếu miss sẽ tìm ở L2 Redis Cache, nếu tiếp tục miss sẽ đồng bộ lock L2 và query DB.
// Lớp Service tập trung xử lý bộ nhớ đệm, khóa và gọi DB mà không thực hiện kiểm tra hoặc parse chuỗi cursor đầu vào.
func (s *planService) ListPlans(ctx context.Context, filter entity.Plan, cursorTime time.Time, cursorID uuid.UUID, limit int) ([]entity.Plan, string, error) {
	// 1. Tạo Cache Key duy nhất theo bộ lọc, cursorTime, cursorID, limit
	cacheKey := fmt.Sprintf("cost_manager:plans:list:%s:%s:%s:%d:%s:%d",
		filter.ServiceType,
		filter.ZoneID.String(),
		filter.Status,
		cursorTime.UnixNano(),
		cursorID.String(),
		limit,
	)

	// 2. Kiểm tra bộ nhớ đệm L1 (RAM) - Đọc và clone CoW
	s.l1Mutex.RLock()
	item, exists := s.l1Cache[cacheKey]
	s.l1Mutex.RUnlock()
	if exists && time.Now().Before(item.expiredAt) {
		copied := make([]entity.Plan, len(item.plans))
		copy(copied, item.plans) // Clone danh sách để đảm bảo an toàn CoW

		// Tạo cursor tiếp theo từ phần tử cuối cùng nếu số bản ghi bằng limit
		nextCursor := ""
		if len(copied) == limit && limit > 0 {
			lastItem := copied[len(copied)-1]
			raw := fmt.Sprintf("%s,%s", lastItem.CreatedAt.Format(time.RFC3339Nano), lastItem.ID.String())
			nextCursor = base64.StdEncoding.EncodeToString([]byte(raw))
		}
		return copied, nextCursor, nil
	}

	// 3. Nếu L1 miss, kiểm tra L2 (Redis) dùng Protobuf Binary
	binaryData, err := s.redisClient.Get(ctx, cacheKey).Bytes()
	if err == nil {
		var payload planproto.PlanListCachePayload
		if err := proto.Unmarshal(binaryData, &payload); err == nil {
			// Convert proto list sang entity.Plan list
			plans := make([]entity.Plan, len(payload.Plans))
			for i, p := range payload.Plans {
				parsedID, _ := uuid.Parse(p.Id)
				parsedZoneID, _ := uuid.Parse(p.ZoneId)
				plans[i] = entity.Plan{
					ID:           parsedID,
					Name:         p.Name,
					Code:         p.Code,
					ServiceType:  entity.ServiceType(p.ServiceType),
					ZoneID:       parsedZoneID,
					MonthlyPrice: p.MonthlyPrice,
					Currency:     p.Currency,
					Status:       p.Status,
					Description:  p.Description,
					CreatedAt:    time.Unix(p.CreatedAt, 0),
					UpdatedAt:    time.Unix(p.UpdatedAt, 0),
				}
			}

			// Nạp dữ liệu vào L1 RAM (áp dụng CoW clone)
			s.l1Mutex.Lock()
			copiedForL1 := make([]entity.Plan, len(plans))
			copy(copiedForL1, plans)
			s.l1Cache[cacheKey] = l1CacheItem{
				plans:     copiedForL1,
				expiredAt: time.Now().Add(1 * time.Minute),
			}
			s.l1Mutex.Unlock()

			// Tạo nextCursor cho client
			nextCursor := ""
			if len(plans) == limit && limit > 0 {
				lastItem := plans[len(plans)-1]
				raw := fmt.Sprintf("%s,%s", lastItem.CreatedAt.Format(time.RFC3339Nano), lastItem.ID.String())
				nextCursor = base64.StdEncoding.EncodeToString([]byte(raw))
			}

			copiedForReturn := make([]entity.Plan, len(plans))
			copy(copiedForReturn, plans)
			return copiedForReturn, nextCursor, nil
		}
	}

	// 4. Nếu L2 miss: Khóa L2 (Redis Lock) và dùng singleflight để đồng bộ hóa
	lockKey := cacheKey + ":lock"
	val, err, _ := s.sfGroup.Do(cacheKey, func() (any, error) {
		// Thử acquire lock trên Redis để chỉ cho phép 1 node/instance query DB tại 1 thời điểm
		acquired, _ := s.redisClient.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
		if !acquired {
			// Nếu không lấy được lock, dừng 100ms chờ node khác nạp dữ liệu xong rồi đọc lại L2
			time.Sleep(100 * time.Millisecond)
			binaryData, err := s.redisClient.Get(ctx, cacheKey).Bytes()
			if err == nil {
				var payload planproto.PlanListCachePayload
				if err := proto.Unmarshal(binaryData, &payload); err == nil {
					plans := make([]entity.Plan, len(payload.Plans))
					for i, p := range payload.Plans {
						parsedID, _ := uuid.Parse(p.Id)
						parsedZoneID, _ := uuid.Parse(p.ZoneId)
						plans[i] = entity.Plan{
							ID:           parsedID,
							Name:         p.Name,
							Code:         p.Code,
							ServiceType:  entity.ServiceType(p.ServiceType),
							ZoneID:       parsedZoneID,
							MonthlyPrice: p.MonthlyPrice,
							Currency:     p.Currency,
							Status:       p.Status,
							Description:  p.Description,
							CreatedAt:    time.Unix(p.CreatedAt, 0),
							UpdatedAt:    time.Unix(p.UpdatedAt, 0),
						}
					}
					return plans, nil
				}
			}
			// Fallback trong trường hợp chờ nhưng L2 vẫn rỗng: đi tiếp xuống DB để tránh lỗi hệ thống (HA)
		} else {
			// Giải phóng lock khi đã xử lý xong
			defer s.redisClient.Del(ctx, lockKey)
		}

		// Truy vấn dữ liệu thực tế từ PostgreSQL Database theo Cursor và Limit
		dbPlans, err := s.planRepo.List(ctx, filter, cursorTime, cursorID, limit)
		if err != nil {
			return nil, err
		}

		// Nạp dữ liệu vào L2 Redis bằng định dạng binary protobuf để tối ưu hóa truyền tải qua mạng
		protoPlans := make([]*planproto.Plan, len(dbPlans))
		for i, p := range dbPlans {
			protoPlans[i] = &planproto.Plan{
				Id:           p.ID.String(),
				Name:         p.Name,
				Code:         p.Code,
				ServiceType:  string(p.ServiceType),
				ZoneId:       p.ZoneID.String(),
				MonthlyPrice: p.MonthlyPrice,
				Currency:     p.Currency,
				Status:       p.Status,
				Description:  p.Description,
				CreatedAt:    p.CreatedAt.Unix(),
				UpdatedAt:    p.UpdatedAt.Unix(),
			}
		}
		payload := &planproto.PlanListCachePayload{Plans: protoPlans}
		binaryData, err := proto.Marshal(payload)
		if err == nil {
			_ = s.redisClient.Set(ctx, cacheKey, binaryData, 15*time.Minute).Err()
		}

		return dbPlans, nil
	})

	if err != nil {
		return nil, "", err
	}

	plans, ok := val.([]entity.Plan)
	if !ok {
		return nil, "", fmt.Errorf("unexpected type returned from singleflight: %T", val)
	}

	// Nạp dữ liệu vào L1 RAM sau khi lấy thành công từ DB (áp dụng CoW clone)
	s.l1Mutex.Lock()
	copiedForL1 := make([]entity.Plan, len(plans))
	copy(copiedForL1, plans)
	s.l1Cache[cacheKey] = l1CacheItem{
		plans:     copiedForL1,
		expiredAt: time.Now().Add(1 * time.Minute),
	}
	s.l1Mutex.Unlock()

	// Tạo nextCursor cho client
	nextCursor := ""
	if len(plans) == limit && limit > 0 {
		lastItem := plans[len(plans)-1]
		raw := fmt.Sprintf("%s,%s", lastItem.CreatedAt.Format(time.RFC3339Nano), lastItem.ID.String())
		nextCursor = base64.StdEncoding.EncodeToString([]byte(raw))
	}

	copiedForReturn := make([]entity.Plan, len(plans))
	copy(copiedForReturn, plans)
	return copiedForReturn, nextCursor, nil
}
