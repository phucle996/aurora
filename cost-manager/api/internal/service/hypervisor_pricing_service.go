package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	hypervisorpricingv1 "cost-manager/api/internal/genproto/billing/pricing/hypervisor/v1"
	pricingv1 "cost-manager/api/internal/genproto/billing/pricing/v1"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// HẰNG SỐ & CẤU HÌNH NGHIỆP VỤ HYPERVISOR PRICING
// ============================================================================

const (
	// Định dạng mốc thời gian chuẩn ISO-8601 UTC với độ chính xác Microsecond (dùng để băm mã bảo mật SHA-256)
	hypervisorPricingChecksumTimeLayout = "2006-01-02T15:04:05.000000Z07:00"

	// Kênh Redis Pub/Sub phát tín hiệu khi có phiên bản bảng giá máy ảo mới được ban hành (Publish)
	hypervisorPricingCacheChannel   = "billing.pricing.hypervisor.version.published.v1"
	hypervisorPricingCacheKeyPrefix = "cost-manager:hypervisor:pricing:snapshot:v1"

	// Thời gian sống (TTL) của bộ nhớ đệm:
	hypervisorPricingCacheL1TTL    = time.Minute     // L1 Cache (RAM trong tiến trình): 1 phút
	hypervisorPricingCacheL2TTL    = 5 * time.Minute // L2 Cache (Redis Cluster): 5 phút
	hypervisorPricingEngineChannel = "billing.pricing.schedule.version.published"
)

// hypervisorPricingCacheItem đại diện cho một phần tử bảng giá được lưu tạm trong bộ nhớ RAM L1.
type hypervisorPricingCacheItem struct {
	snapshot  *entity.HypervisorPricingSnapshot
	expiresAt time.Time
}

// hypervisorPricingService quản lý toàn bộ vòng đời của nghiệp vụ tính giá tài nguyên máy ảo (Hypervisor):
// - Dự toán chi phí máy ảo theo giờ và theo tháng 730h (vCPU, RAM, Disk)
// - Ban hành phiên bản bảng giá gốc (Base Price Publishing)
// - Quản lý hệ số điều chỉnh giá theo từng trung tâm dữ liệu / khu vực (Zone Multipliers)
// - Tự động đồng bộ và xóa Cache khi có thay đổi (Cache Invalidation)
type hypervisorPricingService struct {
	repo        billingRepoInterface.HypervisorPricingRepository
	redisClient *goredis.Client

	cacheLoad       singleflight.Group
	cacheMu         sync.RWMutex
	cache           map[string]hypervisorPricingCacheItem
	cacheGeneration uint64
	wake            chan struct{}
}

// NewHypervisorPricingService khởi tạo đối tượng Hypervisor Pricing Service duy nhất và đầy đủ.
func NewHypervisorPricingService(
	repo billingRepoInterface.HypervisorPricingRepository,
	redisClient *goredis.Client,
) billingSvcInterface.HypervisorPricingService {
	return &hypervisorPricingService{
		repo:        repo,
		redisClient: redisClient,
		cache:       make(map[string]hypervisorPricingCacheItem),
		wake:        make(chan struct{}, 1),
	}
}

// ============================================================================
// 1. WORKFLOW: DỰ TOÁN CƯỚC MÁY ẢO HYPERVISOR (ESTIMATE HYPERVISOR)
// ============================================================================

// EstimateHypervisor tính toán chi phí ước tính (theo giờ và theo tháng chuẩn 730h) cho cấu hình VM (CPU, RAM, Disk).
//
// 📌 GIẢI THÍCH DÀNH CHO NON-TECH / PRODUCT TEAM:
// - Đơn vị tính cơ sở:
//   - vCPU tính theo: Nhân CPU - Giây (CORE_SECOND)
//   - RAM tính theo: Megabyte - Giây (MIB_SECOND)
//   - Disk tính theo: Gigabyte - Giây (GIB_SECOND)
//
// - Quy đổi 1 giờ = 3,600 giây: Lấy số lượng cấu hình nhân 3,600 giây.
// - Quy đổi 1 tháng = 730 giờ: Đây là tiêu chuẩn điện toán đám mây quốc tế (365 ngày * 24h / 12 tháng = 730 giờ/tháng).
// - Đơn vị tiền tệ: Toàn bộ số tiền được lưu ở đơn vị Micro-Units (1 Đơn vị = 1,000,000 Micro-Units) để triệt tiêu sai số dấu phẩy động (Floating Point).
//
// 🚀 LUỒNG XỬ LÝ 8 BƯỚC END-TO-END (VIẾT HOÀN TOÀN INLINE):
// 1. Kiểm tra giới hạn biên phần cứng (CPU 1-1024, RAM 1-4TB, Disk 1-1PB).
// 2. Tra cứu Cache 3 tầng cho 3 bảng giá (L1 RAM -> SingleFlight -> L2 Redis Protobuf -> DB PostgreSQL).
// 3. Xác thực tính nhất quán của bảng giá (Cùng tiền tệ, đúng đơn vị đo lường).
// 4. Lấy hệ số điều chỉnh khu vực (Zone Multiplier) và xác thực chữ ký SHA-256 chống gian lận.
// 5. Quy đổi sang đơn vị giây/giờ và kiểm tra chống tràn số nguyên 64-bit.
// 6. Tính toán biểu phí bậc thang từng phần bằng số học phân số chính xác tuyệt đối (big.Rat).
// 7. Tổng hợp chi phí theo giờ và tháng (730 giờ) với kiểm tra tràn số.
// 8. Đóng gói kết quả chi tiết kèm mã Checksum niêm phong gửi về cho Client.
func (s *hypervisorPricingService) EstimateHypervisor(
	ctx context.Context,
	cpuCores, memoryMIB, diskGIB int64,
	zoneID uuid.UUID,
) (*entity.HypervisorEstimate, error) {
	// ------------------------------------------------------------------------
	// BƯỚC 1: KIỂM TRA GIỚI HẠN BIÊN CỦA PHẦN CỨNG (HARDWARE BOUNDS VALIDATION)
	// ------------------------------------------------------------------------
	// - vCPU: tối thiểu 1 core, tối đa 1024 cores.
	// - Memory: tối thiểu 1 MiB, tối đa 4,194,304 MiB (tương đương 4 TB RAM).
	// - Disk: tối thiểu 1 GiB, tối đa 1,048,576 GiB (tương đương 1 PB Disk).
	// - ZoneID: Bắt buộc phải là UUID hợp lệ (không được là uuid.Nil).
	if cpuCores < 1 || cpuCores > 1024 || memoryMIB < 1 || memoryMIB > 4_194_304 || diskGIB < 1 || diskGIB > 1_048_576 || zoneID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	now := time.Now().UTC()

	// ------------------------------------------------------------------------
	// BƯỚC 2: TRA CỨU BẢNG GIÁ ĐANG HIỆU LỰC CHO 3 THÀNH PHẦN (CACHE 3 TẦNG)
	// ------------------------------------------------------------------------
	// Hàm nội bộ tra cứu bảng giá theo quy tắc Cache 3 tầng:
	// Tầng 1: L1 RAM Cache (Truy xuất trong Microsecond)
	// Tầng 2: SingleFlight Guard (Chống nghẽn tải - Stampede Protection)
	// Tầng 3: L2 Redis Cache (Lưu dưới dạng Protobuf Binary - Zero Reflection)
	// Tầng 4: Database PostgreSQL (Fallback khi Cache Miss, truy vấn bằng SQL CTE)
	loadSnapshot := func(chargeKind entity.ChargeKindCode, expectedUnit string) (*entity.HypervisorPricingSnapshot, error) {
		lookupKey := string(chargeKind)

		// 2.A. Kiểm tra nhanh tại Tầng L1 Cache (RAM trong tiến trình hiện tại)
		s.cacheMu.RLock()
		item, ok := s.cache[lookupKey]
		s.cacheMu.RUnlock()

		if ok && now.Before(item.expiresAt) && item.snapshot != nil &&
			!item.snapshot.EffectiveFrom.After(now) &&
			(item.snapshot.EffectiveTo == nil || now.Before(*item.snapshot.EffectiveTo)) {
			// Trúng L1 Cache -> Lấy trực tiếp từ RAM trong microsecond (Fast Path)
			return item.snapshot, nil
		}

		// 2.B. Nếu L1 Cache bị Miss: Dùng SingleFlight để chỉ 1 Goroutine đi tải từ Redis/DB
		value, err, _ := s.cacheLoad.Do(lookupKey, func() (any, error) {
			// Double-check lại L1 RAM Cache bên trong SingleFlight
			s.cacheMu.RLock()
			generation := s.cacheGeneration
			cached, ready := s.cache[lookupKey]
			s.cacheMu.RUnlock()
			current := time.Now().UTC()
			if ready && current.Before(cached.expiresAt) && cached.snapshot != nil &&
				!cached.snapshot.EffectiveFrom.After(now) &&
				(cached.snapshot.EffectiveTo == nil || now.Before(*cached.snapshot.EffectiveTo)) {
				return cached.snapshot, nil
			}

			cacheKey := fmt.Sprintf("%s:%s", hypervisorPricingCacheKeyPrefix, chargeKind)

			// ================================================================
			// 2.1. ĐỌC TỪ L2 CACHE (REDIS) DƯỚI DẠNG PROTOBUF BINARY (FAST PATH)
			// ================================================================
			// Đọc dữ liệu nhị phân đã được mã hóa bằng Protobuf từ Redis cluster.
			// Dùng Protobuf giúp giải mã trực tiếp vào bộ nhớ, triệt tiêu 100% Reflection overhead của JSON.
			if s.redisClient != nil {
				if raw, redisErr := s.redisClient.Get(ctx, cacheKey).Bytes(); redisErr == nil {
					var payload hypervisorpricingv1.HypervisorPricingSnapshotCacheEntryV1

					// Giải mã Protobuf Binary và kiểm tra tính toàn vẹn của hợp đồng dữ liệu (Contract Invariants):
					// - PricingScheduleId và VersionId phải đủ đúng chuẩn 16 bytes UUID nhị phân.
					// - ChecksumSha256 phải đủ đúng 32 bytes của mã băm SHA-256.
					// - Đúng loại tài nguyên và đơn vị đo lường tương ứng.
					if decodeErr := proto.Unmarshal(raw, &payload); decodeErr == nil &&
						len(payload.PricingScheduleId) == 16 && len(payload.VersionId) == 16 &&
						len(payload.ChecksumSha256) == sha256.Size && payload.Code != "" &&
						payload.Currency != "" && payload.VersionNumber > 0 &&
						payload.ChargeKindCode == string(chargeKind) &&
						payload.RawInputUnit == expectedUnit {

						// Chuyển đổi UUID từ 16 bytes nhị phân sang kiểu uuid.UUID chuẩn
						scheduleID, scheduleErr := uuid.FromBytes(payload.PricingScheduleId)
						versionID, versionErr := uuid.FromBytes(payload.VersionId)
						if scheduleErr == nil && versionErr == nil && len(payload.Brackets) > 0 {
							brackets := make([]entity.HypervisorPricingSnapshotBracket, len(payload.Brackets))
							validBrackets := true

							// Chuyển đổi từng bậc thang giá từ Protobuf message sang domain entity HypervisorPricingSnapshotBracket
							for index, bracket := range payload.Brackets {
								if bracket == nil {
									validBrackets = false
									break
								}
								var rangeEnd *int64
								if bracket.RangeEndQuantity != nil {
									value := *bracket.RangeEndQuantity
									rangeEnd = &value
								}
								brackets[index] = entity.HypervisorPricingSnapshotBracket{
									RangeStartQuantity:       bracket.RangeStartQuantity,
									RangeEndQuantity:         rangeEnd,
									PriceNumeratorMicroUnits: bracket.PriceNumeratorMicroUnits,
									PriceDenominatorQuantity: bracket.PriceDenominatorQuantity,
								}
							}

							// Kiểm tra tính liên tục của các bậc thang giá (Progressive Continuity Invariant):
							// 1. Bậc đầu tiên phải bắt đầu từ đúng 0.
							// 2. Không được có đơn giá âm, mẫu số phải dương (> 0).
							// 3. Bậc cuối cùng phải mở đến vô cực (RangeEndQuantity == nil).
							// 4. Các bậc liền kề phải khớp nhau chính xác: Bậc N kết thúc ở đâu thì bậc N+1 phải bắt đầu ngay tại đó.
							if validBrackets && brackets[0].RangeStartQuantity == 0 {
								for index, bracket := range brackets {
									if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 ||
										(index == len(brackets)-1 && bracket.RangeEndQuantity != nil) ||
										(index < len(brackets)-1 && (bracket.RangeEndQuantity == nil || *bracket.RangeEndQuantity != brackets[index+1].RangeStartQuantity)) {
										validBrackets = false
										break
									}
								}
							}

							// Kiểm tra thời gian hiệu lực và mã băm Checksum chống giả mạo
							if validBrackets && brackets[0].RangeStartQuantity == 0 {
								effectiveFrom := time.UnixMicro(payload.EffectiveFromUnixMicro).UTC()
								var effectiveTo *time.Time
								if payload.EffectiveToUnixMicro != nil {
									value := time.UnixMicro(*payload.EffectiveToUnixMicro).UTC()
									effectiveTo = &value
								}
								if effectiveTo != nil && !effectiveTo.After(effectiveFrom) {
									validBrackets = false
								}

								snap := &entity.HypervisorPricingSnapshot{
									PricingScheduleID: scheduleID,
									VersionID:         versionID,
									Code:              payload.Code,
									ChargeKindCode:    chargeKind,
									ModuleCode:        "hypervisor",
									PricingModel:      entity.PricingModelProgressiveUnit,
									RawInputUnit:      expectedUnit,
									VersionNumber:     int(payload.VersionNumber),
									EffectiveFrom:     effectiveFrom,
									EffectiveTo:       effectiveTo,
									Checksum:          hex.EncodeToString(payload.ChecksumSha256),
									Currency:          payload.Currency,
									Brackets:          brackets,
								}

								// Tính lại SHA-256 Checksum độc lập để đối chiếu với chữ ký niêm phong
								hash := sha256.New()
								write := func(value string) {
									var length [8]byte
									binary.BigEndian.PutUint64(length[:], uint64(len(value)))
									_, _ = hash.Write(length[:])
									_, _ = hash.Write([]byte(value))
								}
								write(snap.Code)
								write(string(snap.ChargeKindCode))
								write(string(snap.PricingModel))
								write(snap.Currency)
								write(snap.EffectiveFrom.UTC().Format(hypervisorPricingChecksumTimeLayout))
								write(fmt.Sprintf("%d", snap.VersionNumber))
								for _, bracket := range snap.Brackets {
									write(fmt.Sprintf("%d", bracket.RangeStartQuantity))
									if bracket.RangeEndQuantity == nil {
										write("infinity")
									} else {
										write(fmt.Sprintf("%d", *bracket.RangeEndQuantity))
									}
									write(fmt.Sprintf("%d", bracket.PriceNumeratorMicroUnits))
									write(fmt.Sprintf("%d", bracket.PriceDenominatorQuantity))
								}
								if fmt.Sprintf("%x", hash.Sum(nil)) != snap.Checksum {
									validBrackets = false
								}

								// Nếu dữ liệu từ Redis hoàn toàn hợp lệ và đang trong khung giờ hiệu lực:
								// Lưu vào L1 RAM Cache để phục vụ các yêu cầu kế tiếp trong microsecond!
								if validBrackets && !snap.EffectiveFrom.After(now) && (snap.EffectiveTo == nil || now.Before(*snap.EffectiveTo)) {
									s.cacheMu.Lock()
									if s.cacheGeneration == generation {
										s.cache[lookupKey] = hypervisorPricingCacheItem{
											snapshot:  snap,
											expiresAt: current.Add(hypervisorPricingCacheL1TTL),
										}
									}
									s.cacheMu.Unlock()
									return snap, nil
								}
							}
						}
					}
				}
			}

			// ================================================================
			// 2.2. TRUY VẤN CƠ SỞ DỮ LIỆU GỐC (POSTGRESQL - FALLBACK PATH)
			// ================================================================
			// Chạy khi L1 RAM và L2 Redis đều bị miss (hoặc dữ liệu trên Redis không đạt chuẩn kiểm tra).
			dbSnap, repoErr := s.repo.GetActiveHypervisorPricingSnapshot(ctx, chargeKind, now)
			if repoErr != nil {
				return nil, repoErr
			}

			// Kiểm tra cấu trúc bản ghi DB có đầy đủ thông tin định danh và mô hình định giá bắt buộc
			if dbSnap == nil || dbSnap.PricingScheduleID == uuid.Nil || dbSnap.VersionID == uuid.Nil ||
				dbSnap.Code == "" || dbSnap.ModuleCode != "hypervisor" || dbSnap.Currency == "" ||
				dbSnap.VersionNumber < 1 || dbSnap.PricingModel != entity.PricingModelProgressiveUnit ||
				dbSnap.ChargeKindCode != chargeKind {
				return nil, fmt.Errorf("Hypervisor pricing snapshot is incomplete for %s", chargeKind)
			}

			// Kiểm tra đơn vị đo lường cơ sở
			if dbSnap.RawInputUnit != expectedUnit {
				return nil, fmt.Errorf("Hypervisor pricing snapshot unit mismatch for %s", chargeKind)
			}

			// Kiểm tra tính liên tục của các bậc thang giá từ DB (không bị thủng dải định giá)
			if len(dbSnap.Brackets) == 0 || dbSnap.Brackets[0].RangeStartQuantity != 0 {
				return nil, billingTaxonomy.ErrInvalidPricingBrackets
			}
			for index, bracket := range dbSnap.Brackets {
				if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 {
					return nil, billingTaxonomy.ErrInvalidPricingBrackets
				}
				if index == len(dbSnap.Brackets)-1 {
					if bracket.RangeEndQuantity != nil {
						return nil, billingTaxonomy.ErrInvalidPricingBrackets
					}
					continue
				}
				if bracket.RangeEndQuantity == nil || *bracket.RangeEndQuantity != dbSnap.Brackets[index+1].RangeStartQuantity {
					return nil, billingTaxonomy.ErrInvalidPricingBrackets
				}
			}

			// ================================================================
			// 2.3. KIỂM TRA CHỮ KÝ BẢO MẬT (SHA-256 CHECKSUM) CỦA BẢN GHI DB
			// ================================================================
			// Đảm bảo không ai có thể can thiệp trực tiếp vào DB để sửa đổi đơn giá bất hợp pháp.
			hash := sha256.New()
			write := func(value string) {
				var length [8]byte
				binary.BigEndian.PutUint64(length[:], uint64(len(value)))
				_, _ = hash.Write(length[:])
				_, _ = hash.Write([]byte(value))
			}
			write(dbSnap.Code)
			write(string(dbSnap.ChargeKindCode))
			write(string(dbSnap.PricingModel))
			write(dbSnap.Currency)
			write(dbSnap.EffectiveFrom.UTC().Format(hypervisorPricingChecksumTimeLayout))
			write(fmt.Sprintf("%d", dbSnap.VersionNumber))
			for _, bracket := range dbSnap.Brackets {
				write(fmt.Sprintf("%d", bracket.RangeStartQuantity))
				if bracket.RangeEndQuantity == nil {
					write("infinity")
				} else {
					write(fmt.Sprintf("%d", *bracket.RangeEndQuantity))
				}
				write(fmt.Sprintf("%d", bracket.PriceNumeratorMicroUnits))
				write(fmt.Sprintf("%d", bracket.PriceDenominatorQuantity))
			}
			if fmt.Sprintf("%x", hash.Sum(nil)) != dbSnap.Checksum {
				return nil, fmt.Errorf("Hypervisor pricing snapshot checksum mismatch for %s", chargeKind)
			}

			// ================================================================
			// 2.4. LƯU BẢNG GIÁ HỢP LỆ VÀO L2 REDIS CACHE DƯỚI DẠNG PROTOBUF BINARY
			// ================================================================
			// Tuần tự hóa đối tượng sang Protobuf byte array siêu nhẹ và ghi vào Redis cluster (TTL 5 phút).
			s.cacheMu.RLock()
			generationCurrent := s.cacheGeneration == generation
			s.cacheMu.RUnlock()
			if s.redisClient != nil && generationCurrent {
				checksumBytes, checksumErr := hex.DecodeString(dbSnap.Checksum)
				if checksumErr == nil && len(checksumBytes) == sha256.Size {
					brackets := make([]*hypervisorpricingv1.HypervisorPricingScalarBracketV1, len(dbSnap.Brackets))
					for index, bracket := range dbSnap.Brackets {
						entry := &hypervisorpricingv1.HypervisorPricingScalarBracketV1{
							RangeStartQuantity:       bracket.RangeStartQuantity,
							PriceNumeratorMicroUnits: bracket.PriceNumeratorMicroUnits,
							PriceDenominatorQuantity: bracket.PriceDenominatorQuantity,
						}
						if bracket.RangeEndQuantity != nil {
							value := *bracket.RangeEndQuantity
							entry.RangeEndQuantity = &value
						}
						brackets[index] = entry
					}
					entry := &hypervisorpricingv1.HypervisorPricingSnapshotCacheEntryV1{
						PricingScheduleId:      dbSnap.PricingScheduleID[:],
						VersionId:              dbSnap.VersionID[:],
						Code:                   dbSnap.Code,
						ChargeKindCode:         string(dbSnap.ChargeKindCode),
						Currency:               dbSnap.Currency,
						VersionNumber:          int32(dbSnap.VersionNumber),
						EffectiveFromUnixMicro: dbSnap.EffectiveFrom.UTC().UnixMicro(),
						ChecksumSha256:         checksumBytes,
						RawInputUnit:           dbSnap.RawInputUnit,
						Brackets:               brackets,
					}
					if dbSnap.EffectiveTo != nil {
						value := dbSnap.EffectiveTo.UTC().UnixMicro()
						entry.EffectiveToUnixMicro = &value
					}
					// Mã hóa Protobuf binary (0 reflection, kích thước chỉ ~200 bytes)
					if payload, marshalErr := proto.Marshal(entry); marshalErr == nil {
						_ = s.redisClient.Set(ctx, cacheKey, payload, hypervisorPricingCacheL2TTL).Err()
					}
				}
			}

			// ================================================================
			// 2.5. LƯU BẢNG GIÁ VÀO L1 RAM CACHE CỦA TIẾN TRÌNH HIỆN TẠI
			// ================================================================
			// Đảm bảo không bị ghi đè dữ liệu cũ nếu cacheGeneration đã bị thay đổi bởi worker invalidation.
			s.cacheMu.Lock()
			if s.cacheGeneration == generation {
				s.cache[lookupKey] = hypervisorPricingCacheItem{
					snapshot:  dbSnap,
					expiresAt: time.Now().UTC().Add(hypervisorPricingCacheL1TTL),
				}
			}
			s.cacheMu.Unlock()

			return dbSnap, nil
		})
		if err != nil {
			return nil, err
		}
		snapVal, ok := value.(*entity.HypervisorPricingSnapshot)
		if !ok || snapVal == nil {
			return nil, fmt.Errorf("Hypervisor pricing cache returned unexpected value for %s", chargeKind)
		}
		return snapVal, nil
	}

	// Tải bảng giá cho vCPU
	vcpu, err := loadSnapshot(entity.ChargeKindHypervisorVCPU, "CORE_SECOND")
	if err != nil {
		return nil, err
	}

	// Tải bảng giá cho Memory (RAM)
	memory, err := loadSnapshot(entity.ChargeKindHypervisorMemoryMIB, "MIB_SECOND")
	if err != nil {
		return nil, err
	}

	// Tải bảng giá cho Disk (Ổ cứng)
	disk, err := loadSnapshot(entity.ChargeKindHypervisorDiskGIB, "GIB_SECOND")
	if err != nil {
		return nil, err
	}

	// ------------------------------------------------------------------------
	// BƯỚC 3: XÁC THỰC TÍNH NHẤT QUÁN CỦA HỢP ĐỒNG BẢNG GIÁ (CONTRACT VALIDATION)
	// ------------------------------------------------------------------------
	// Đảm bảo cả 3 bảng giá (CPU, RAM, Disk) đều được niêm yết trên cùng một loại tiền tệ (ví dụ cùng USD hoặc VND)
	if vcpu.Currency != memory.Currency || vcpu.Currency != disk.Currency {
		return nil, fmt.Errorf("Hypervisor pricing snapshot currency mismatch")
	}

	// ------------------------------------------------------------------------
	// BƯỚC 4: LẤY HỆ SỐ ĐIỀU CHỈNH THEO ZONE VÀ XÁC THỰC CHECKSUM
	// ------------------------------------------------------------------------
	// Tra cứu hệ số multiplier đang active của Zone (Mặc định 1/1 nếu không có cấu hình riêng).
	adjustment, err := s.repo.GetActiveHypervisorZonePriceAdjustment(ctx, zoneID, now)
	if err != nil {
		return nil, err
	}

	numerator, denominator := int64(1), int64(1)
	if adjustment != nil {
		hash := sha256.New()
		for _, value := range []string{
			adjustment.ZoneID.String(),
			fmt.Sprintf("%d", adjustment.VersionNumber),
			adjustment.EffectiveFrom.UTC().Format(hypervisorPricingChecksumTimeLayout),
			fmt.Sprintf("%d", adjustment.MultiplierNumerator),
			fmt.Sprintf("%d", adjustment.MultiplierDenominator),
		} {
			var length [8]byte
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
		if fmt.Sprintf("%x", hash.Sum(nil)) != adjustment.Checksum {
			return nil, fmt.Errorf("Hypervisor Zone price adjustment checksum mismatch")
		}
		numerator, denominator = adjustment.MultiplierNumerator, adjustment.MultiplierDenominator
	}

	if numerator < 0 || denominator <= 0 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	// ------------------------------------------------------------------------
	// BƯỚC 5: QUY ĐỔI TÀI NGUYÊN SANG ĐƠN VỊ GIÂY/GIỜ (LIMIT X 3600 GIÂY)
	// ------------------------------------------------------------------------
	// Đổi cấu hình phần cứng theo giờ (nhân với 3,600 giây trong 1 giờ) có kiểm tra tràn số nguyên 64-bit
	checkedHourlyLimit := func(limit int64) (int64, bool) {
		quantity := new(big.Int).Mul(big.NewInt(limit), big.NewInt(3_600))
		if !quantity.IsInt64() {
			return 0, false
		}
		return quantity.Int64(), true
	}

	vcpuQuantity, ok := checkedHourlyLimit(cpuCores)
	if !ok {
		return nil, fmt.Errorf("Hypervisor vCPU hourly quantity exceeds BIGINT")
	}

	memoryQuantity, ok := checkedHourlyLimit(memoryMIB)
	if !ok {
		return nil, fmt.Errorf("Hypervisor memory hourly quantity exceeds BIGINT")
	}

	diskQuantity, ok := checkedHourlyLimit(diskGIB)
	if !ok {
		return nil, fmt.Errorf("Hypervisor disk hourly quantity exceeds BIGINT")
	}

	// ------------------------------------------------------------------------
	// BƯỚC 6: TÍNH CHI PHÍ TỪNG THÀNH PHẦN THEO PROGRESSIVE BRACKETS
	// ------------------------------------------------------------------------
	// Áp dụng công thức biểu phí bậc thang từng phần (Progressive Unit Brackets):
	// - Duyệt qua từng bậc thang giá từ thấp đến cao.
	// - Phần sản lượng rơi vào bậc nào thì nhân với đơn giá của bậc đó.
	// - Nhân thêm hệ số khu vực (Zone Multiplier) và làm tròn lên (Ceil) để bảo đảm doanh thu tối thiểu.
	calcComponent := func(quantity uint64, brackets []entity.HypervisorPricingSnapshotBracket) (int64, error) {
		if len(brackets) == 0 {
			return 0, billingTaxonomy.ErrInvalidPricingBrackets
		}
		total := new(big.Rat)
		for _, bracket := range brackets {
			if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 {
				return 0, billingTaxonomy.ErrInvalidPricingBrackets
			}
			start := uint64(bracket.RangeStartQuantity)
			if quantity <= start {
				break
			}
			upper := quantity
			if bracket.RangeEndQuantity != nil {
				if *bracket.RangeEndQuantity <= bracket.RangeStartQuantity {
					return 0, billingTaxonomy.ErrInvalidPricingBrackets
				}
				if uint64(*bracket.RangeEndQuantity) < upper {
					upper = uint64(*bracket.RangeEndQuantity)
				}
			}
			if upper > start {
				units := new(big.Int).SetUint64(upper - start)
				price := new(big.Int).Mul(units, big.NewInt(bracket.PriceNumeratorMicroUnits))
				total.Add(total, new(big.Rat).SetFrac(price, big.NewInt(bracket.PriceDenominatorQuantity)))
			}
		}
		total.Mul(total, new(big.Rat).SetFrac(big.NewInt(numerator), big.NewInt(denominator)))

		// Làm tròn lên (Ceiling Division) để tránh mất phần lẻ micro-units
		ceil := new(big.Int).Quo(total.Num(), total.Denom())
		if new(big.Int).Mod(total.Num(), total.Denom()).Sign() != 0 {
			ceil.Add(ceil, big.NewInt(1))
		}
		if !ceil.IsInt64() {
			return 0, fmt.Errorf("Hypervisor pricing charge exceeds BIGINT")
		}
		return ceil.Int64(), nil
	}

	// 6.1. Tính tiền cước vCPU
	vcpuCost, err := calcComponent(uint64(vcpuQuantity), vcpu.Brackets)
	if err != nil {
		return nil, err
	}

	// 6.2. Tính tiền cước RAM (Memory)
	memoryCost, err := calcComponent(uint64(memoryQuantity), memory.Brackets)
	if err != nil {
		return nil, err
	}

	// 6.3. Tính tiền cước Disk (Ổ cứng)
	diskCost, err := calcComponent(uint64(diskQuantity), disk.Brackets)
	if err != nil {
		return nil, err
	}

	// ------------------------------------------------------------------------
	// BƯỚC 7: TỔNG HỢP CHI PHÍ THEO GIỜ VÀ THEO THÁNG (730 GIỜ)
	// ------------------------------------------------------------------------
	// Tổng chi phí 1 giờ = vCPUCost + MemoryCost + DiskCost
	totalAdd := new(big.Int).Add(big.NewInt(vcpuCost), big.NewInt(memoryCost))
	totalAdd.Add(totalAdd, big.NewInt(diskCost))
	if !totalAdd.IsInt64() {
		return nil, fmt.Errorf("Hypervisor hourly estimate exceeds BIGINT")
	}
	hourly := totalAdd.Int64()

	// Tổng chi phí 1 tháng ước tính = Hourly * 730 giờ
	monthlyBig := new(big.Int).Mul(big.NewInt(hourly), big.NewInt(730))
	if !monthlyBig.IsInt64() {
		return nil, fmt.Errorf("Hypervisor monthly estimate exceeds BIGINT")
	}
	monthly := monthlyBig.Int64()

	// ------------------------------------------------------------------------
	// BƯỚC 8: ĐÓNG GÓI KẾT QUẢ GỬI VỀ CLIENT (RESULT PROJECTION)
	// ------------------------------------------------------------------------
	var adjustmentID *uuid.UUID
	var adjustmentVersion *int
	var adjustmentChecksum *string
	if adjustment != nil {
		adjustmentID = &adjustment.ID
		adjustmentVersion = &adjustment.VersionNumber
		adjustmentChecksum = &adjustment.Checksum
	}

	return &entity.HypervisorEstimate{
		CPUCores:                  cpuCores,
		MemoryMIB:                 memoryMIB,
		DiskGIB:                   diskGIB,
		VCPUHourlyMicroUnits:      vcpuCost,
		MemoryHourlyMicroUnits:    memoryCost,
		DiskHourlyMicroUnits:      diskCost,
		HourlyMicroUnits:          hourly,
		Monthly730HourMicroUnits:  monthly,
		Currency:                  vcpu.Currency,
		VCPUScheduleCode:          vcpu.Code,
		VCPUScheduleID:            vcpu.PricingScheduleID,
		VCPUScheduleVersionID:     vcpu.VersionID,
		VCPUVersion:               vcpu.VersionNumber,
		VCPUChecksum:              vcpu.Checksum,
		MemoryScheduleCode:        memory.Code,
		MemoryScheduleID:          memory.PricingScheduleID,
		MemoryScheduleVersionID:   memory.VersionID,
		MemoryVersion:             memory.VersionNumber,
		MemoryChecksum:            memory.Checksum,
		DiskScheduleCode:          disk.Code,
		DiskScheduleID:            disk.PricingScheduleID,
		DiskScheduleVersionID:     disk.VersionID,
		DiskVersion:               disk.VersionNumber,
		DiskChecksum:              disk.Checksum,
		RateAdjustmentID:          adjustmentID,
		RateAdjustmentVersion:     adjustmentVersion,
		RateAdjustmentChecksum:    adjustmentChecksum,
		RateAdjustmentNumerator:   numerator,
		RateAdjustmentDenominator: denominator,
		EstimatedAt:               now,
	}, nil
}

// ============================================================================
// 2. WORKFLOW: BAN HÀNH BẢNG GIÁ GỐC HYPERVISOR (PUBLISH BASE PRICE VERSION)
// ============================================================================

// GetHypervisorBasePricePublishTarget lấy thông tin cấu hình bảng giá gốc hiện tại trước khi ban hành bản mới.
func (s *hypervisorPricingService) GetHypervisorBasePricePublishTarget(ctx context.Context, code string) (*entity.HypervisorBasePricePublishTarget, error) {
	return s.repo.GetHypervisorBasePricePublishTarget(ctx, code)
}

// CreateHypervisorBasePriceVersion tạo và công bố một phiên bản bảng giá gốc mới cho Hypervisor.
// Các bước thực hiện:
// 1. Kiểm tra tính hợp lệ của thông tin đầu vào và thời gian hiệu lực.
// 2. Xác thực bảng giá mục tiêu (Schedule Target).
// 3. Sắp xếp và kiểm tra tính liên tục của các bậc thang giá [0, vô cực).
// 4. Tạo chữ ký bảo mật SHA-256 Checksum niêm phong toàn bộ dữ liệu bảng giá.
// 5. Lưu phiên bản mới vào cơ sở dữ liệu PostgreSQL.
// 6. Phát sự kiện Protobuf lên Redis Pub/Sub để các worker khác xóa cache đồng bộ.
func (s *hypervisorPricingService) CreateHypervisorBasePriceVersion(
	ctx context.Context,
	create entity.HypervisorBasePricePublishCommand,
	brackets []entity.HypervisorBasePriceBracketCommand,
) (*entity.HypervisorBasePricePublished, error) {
	create.ScheduleCode = strings.TrimSpace(create.ScheduleCode)
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	create.EffectiveFrom = create.EffectiveFrom.UTC().Truncate(time.Microsecond)

	if create.ScheduleCode == "" || create.CreatedBy == uuid.Nil || create.ExpectedLatestVersion < 0 ||
		create.EffectiveFrom.IsZero() || create.ChangeReason == "" || len(create.ChangeReason) > 2000 ||
		len(brackets) == 0 || len(brackets) > 1000 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	// Bảng giá không được có mốc hiệu lực trong quá khứ quá 1 phút
	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, billingTaxonomy.ErrPricingScheduleEffectiveConflict
	}

	// Lấy thông tin bảng giá mục tiêu để kiểm tra tính tương thích
	target, err := s.repo.GetHypervisorBasePricePublishTarget(ctx, create.ScheduleCode)
	if err != nil {
		return nil, err
	}

	if target.PricingModel != entity.PricingModelProgressiveUnit {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	// Sắp xếp các bậc thang giá theo chiều tăng dần của RangeStartQuantity
	sort.Slice(brackets, func(i, j int) bool { return brackets[i].RangeStartQuantity < brackets[j].RangeStartQuantity })
	for index, bracket := range brackets {
		if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 ||
			(index == 0 && bracket.RangeStartQuantity != 0) ||
			(index == len(brackets)-1 && bracket.RangeEndQuantity != nil) ||
			(index < len(brackets)-1 && (bracket.RangeEndQuantity == nil || *bracket.RangeEndQuantity != brackets[index+1].RangeStartQuantity)) {
			return nil, billingTaxonomy.ErrInvalidPricingBrackets
		}
	}

	// Tạo mã băm SHA-256 Checksum niêm phong toàn bộ nội dung bảng giá
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	write(target.ScheduleCode)
	write(string(target.ChargeKindCode))
	write(string(target.PricingModel))
	write(target.Currency)
	write(create.EffectiveFrom.Format(hypervisorPricingChecksumTimeLayout))
	write(fmt.Sprintf("%d", create.ExpectedLatestVersion+1))
	for _, bracket := range brackets {
		write(fmt.Sprintf("%d", bracket.RangeStartQuantity))
		if bracket.RangeEndQuantity == nil {
			write("infinity")
		} else {
			write(fmt.Sprintf("%d", *bracket.RangeEndQuantity))
		}
		write(fmt.Sprintf("%d", bracket.PriceNumeratorMicroUnits))
		write(fmt.Sprintf("%d", bracket.PriceDenominatorQuantity))
	}
	create.Checksum = fmt.Sprintf("%x", hash.Sum(nil))

	// Lưu phiên bản bảng giá mới vào DB PostgreSQL
	published, err := s.repo.CreateHypervisorBasePriceVersion(ctx, create, brackets)
	if err == nil {
		s.NotifyPricingOutbox()
	}

	// Phát thông báo sự kiện qua Redis Pub/Sub để các node khác xóa cache tức thì
	if err == nil && s.redisClient != nil {
		event := &pricingv1.PricingScheduleVersionPublished{
			EventId:                  published.ID.String(),
			PricingScheduleId:        published.PricingScheduleID.String(),
			PricingScheduleVersionId: published.ID.String(),
			VersionNumber:            int32(published.VersionNumber),
			ChargeKindCode:           string(published.ChargeKindCode),
			EffectiveFromUnixMs:      published.EffectiveFrom.UnixMilli(),
			Checksum:                 published.Checksum,
			OccurredAtUnixMs:         time.Now().UTC().UnixMilli(),
		}
		if payload, marshalErr := proto.Marshal(event); marshalErr == nil {
			_ = s.redisClient.Publish(ctx, hypervisorPricingCacheChannel, payload).Err()
		}
	}
	return published, err
}

// ============================================================================
// 3. WORKFLOW: ĐIỀU CHỈNH GIÁ THEO ZONE (ZONE ADJUSTMENT)
// ============================================================================

// CreateHypervisorZonePriceAdjustment thiết lập và niêm phong hệ số giá mới cho một Zone cụ thể.
func (s *hypervisorPricingService) CreateHypervisorZonePriceAdjustment(
	ctx context.Context,
	create entity.HypervisorZoneAdjustmentPublishCommand,
) (*entity.HypervisorZoneAdjustmentPublished, error) {
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	create.EffectiveFrom = create.EffectiveFrom.UTC().Truncate(time.Microsecond)

	if create.ZoneID == uuid.Nil || create.CreatedBy == uuid.Nil || create.ExpectedLatestVersion < 0 ||
		create.EffectiveFrom.IsZero() || create.ChangeReason == "" || len(create.ChangeReason) > 2000 ||
		create.MultiplierNumerator < 0 || create.MultiplierDenominator <= 0 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, billingTaxonomy.ErrHypervisorZoneAdjustmentConflict
	}

	// Tạo mã băm SHA-256 Checksum niêm phong hệ số điều chỉnh Zone
	hash := sha256.New()
	for _, value := range []string{
		create.ZoneID.String(),
		fmt.Sprintf("%d", create.ExpectedLatestVersion+1),
		create.EffectiveFrom.Format(hypervisorPricingChecksumTimeLayout),
		fmt.Sprintf("%d", create.MultiplierNumerator),
		fmt.Sprintf("%d", create.MultiplierDenominator),
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	create.Checksum = fmt.Sprintf("%x", hash.Sum(nil))

	return s.repo.CreateHypervisorZonePriceAdjustment(ctx, create)
}

// ============================================================================
// 4. WORKFLOW: INVALIDATION WORKER
// ============================================================================

// RunPricingSnapshotRefresh keeps all five Cost-owned Hypervisor L2 snapshots
// warm for Controlplane's direct gate. It replaces the former JSON readiness
// stream and never writes Controlplane state.
func (s *hypervisorPricingService) RunPricingSnapshotRefresh(ctx context.Context) {
	for {
		_, _ = s.EstimateHypervisor(ctx, 1, 1, 1, uuid.New())
		for _, chargeKind := range []entity.ChargeKindCode{entity.ChargeKindHypervisorNetworkIn, entity.ChargeKindHypervisorNetworkOut} {
			snapshot, err := s.repo.GetActiveHypervisorPricingSnapshot(ctx, chargeKind, time.Now().UTC())
			if err != nil || snapshot == nil || snapshot.ModuleCode != "hypervisor" || snapshot.RawInputUnit != "BYTE" || s.redisClient == nil {
				continue
			}
			checksum, err := hex.DecodeString(snapshot.Checksum)
			if err != nil || len(checksum) != sha256.Size {
				continue
			}
			brackets := make([]*hypervisorpricingv1.HypervisorPricingScalarBracketV1, 0, len(snapshot.Brackets))
			for _, bracket := range snapshot.Brackets {
				entry := &hypervisorpricingv1.HypervisorPricingScalarBracketV1{RangeStartQuantity: bracket.RangeStartQuantity, PriceNumeratorMicroUnits: bracket.PriceNumeratorMicroUnits, PriceDenominatorQuantity: bracket.PriceDenominatorQuantity}
				if bracket.RangeEndQuantity != nil {
					value := *bracket.RangeEndQuantity
					entry.RangeEndQuantity = &value
				}
				brackets = append(brackets, entry)
			}
			entry := &hypervisorpricingv1.HypervisorPricingSnapshotCacheEntryV1{PricingScheduleId: snapshot.PricingScheduleID[:], VersionId: snapshot.VersionID[:], Code: snapshot.Code, ChargeKindCode: string(snapshot.ChargeKindCode), Currency: snapshot.Currency, VersionNumber: int32(snapshot.VersionNumber), EffectiveFromUnixMicro: snapshot.EffectiveFrom.UTC().UnixMicro(), ChecksumSha256: checksum, RawInputUnit: snapshot.RawInputUnit, Brackets: brackets}
			if snapshot.EffectiveTo != nil {
				value := snapshot.EffectiveTo.UTC().UnixMicro()
				entry.EffectiveToUnixMicro = &value
			}
			if payload, err := proto.Marshal(entry); err == nil {
				_ = s.redisClient.Set(ctx, fmt.Sprintf("%s:%s", hypervisorPricingCacheKeyPrefix, chargeKind), payload, hypervisorPricingCacheL2TTL).Err()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

// RunPricingCacheInvalidation chạy worker ngầm đăng ký lắng nghe sự kiện bảng giá mới từ Redis Pub/Sub.
// Khi nhận được sự kiện, worker sẽ thực hiện xóa Cache L1 RAM và xóa Cache L2 Redis một cách chính xác (O(1)).
func (s *hypervisorPricingService) RunPricingCacheInvalidation(ctx context.Context) {
	if s.redisClient == nil {
		return
	}
	for {
		pubsub := s.redisClient.Subscribe(ctx, hypervisorPricingCacheChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			_ = pubsub.Close()
			if ctx.Err() != nil {
				return
			}
			logger.SysWarn("billing.hypervisor.pricing.cache.subscribe", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}
		for message := range pubsub.Channel() {
			if ctx.Err() != nil {
				_ = pubsub.Close()
				return
			}
			var event pricingv1.PricingScheduleVersionPublished
			if len(message.Payload) == 0 || len(message.Payload) > 64*1024 ||
				proto.Unmarshal([]byte(message.Payload), &event) != nil {
				logger.SysWarn("billing.hypervisor.pricing.cache.event", "invalid pricing cache event")
				continue
			}
			kind := entity.ChargeKindCode(event.ChargeKindCode)
			switch kind {
			case entity.ChargeKindHypervisorVCPU, entity.ChargeKindHypervisorMemoryMIB,
				entity.ChargeKindHypervisorDiskGIB, entity.ChargeKindHypervisorNetworkIn,
				entity.ChargeKindHypervisorNetworkOut:
				// Hợp lệ, tiến hành invalidate
			default:
				continue
			}

			if event.VersionNumber < 1 || len(event.Checksum) != 64 {
				continue
			}

			// 1. Xóa L1 RAM Cache của tiến trình hiện tại
			s.cacheMu.Lock()
			s.cacheGeneration++
			delete(s.cache, string(kind))
			s.cacheMu.Unlock()

			// 2. Xóa L2 Redis Cache chính xác key O(1)
			cacheKey := fmt.Sprintf("%s:%s", hypervisorPricingCacheKeyPrefix, kind)
			if err := s.redisClient.Del(ctx, cacheKey).Err(); err != nil && ctx.Err() == nil {
				logger.SysWarn("billing.hypervisor.pricing.cache.invalidate", err.Error())
			}
		}
		_ = pubsub.Close()
	}
}

func (s *hypervisorPricingService) NotifyPricingOutbox() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *hypervisorPricingService) RunPricingOutboxRelay(ctx context.Context) {
	reconcile := time.NewTimer(0)
	defer reconcile.Stop()
	for {
		refreshStatuses := false
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-reconcile.C:
			refreshStatuses = true
		}
		if refreshStatuses {
			if err := s.repo.RefreshHypervisorPricingStatuses(ctx); err != nil && ctx.Err() == nil {
				logger.SysError("billing.hypervisor.pricing.outbox.status", err.Error())
			}
			reconcile.Reset(30*time.Second + time.Duration(rand.IntN(10))*time.Second)
		}
		for ctx.Err() == nil {
			claimToken := uuid.New()
			rows, err := s.repo.ClaimHypervisorPricingOutbox(ctx, claimToken, time.Now().UTC().Add(30*time.Second), 100)
			if err != nil {
				logger.SysError("billing.hypervisor.pricing.outbox.claim", err.Error())
				break
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				event := &pricingv1.PricingScheduleVersionPublished{EventId: row.ID.String(), PricingScheduleId: row.PricingScheduleID.String(), PricingScheduleVersionId: row.VersionID.String(), VersionNumber: row.VersionNumber, ChargeKindCode: string(row.ChargeKindCode), EffectiveFromUnixMs: row.EffectiveFrom.UnixMilli(), Checksum: row.Checksum, OccurredAtUnixMs: row.OccurredAt.UnixMilli()}
				payload, publishErr := proto.Marshal(event)
				if publishErr == nil && s.redisClient != nil {
					publishErr = s.redisClient.Publish(ctx, hypervisorPricingEngineChannel, payload).Err()
				}
				if publishErr == nil && s.redisClient != nil {
					publishErr = s.redisClient.Publish(ctx, hypervisorPricingCacheChannel, payload).Err()
				}
				if s.redisClient == nil && publishErr == nil {
					publishErr = fmt.Errorf("Hypervisor pricing outbox Redis is unavailable")
				}
				if publishErr != nil {
					backoffSeconds := 1 << min(row.RetryCount, 6)
					availableAt := time.Now().UTC().Add(time.Duration(backoffSeconds+rand.IntN(backoffSeconds+1)) * time.Second)
					if err := s.repo.RetryHypervisorPricingOutbox(ctx, row.ID, row.ClaimToken, publishErr.Error(), availableAt); err != nil && ctx.Err() == nil {
						logger.SysError("billing.hypervisor.pricing.outbox.retry", err.Error())
					}
					continue
				}
				if err := s.repo.MarkHypervisorPricingOutboxPublished(ctx, row.ID, row.ClaimToken); err != nil && ctx.Err() == nil {
					logger.SysError("billing.hypervisor.pricing.outbox.publish", err.Error())
				}
			}
		}
	}
}
