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
	storagepricingv1 "cost-manager/api/internal/genproto/billing/pricing/storage/v1"
	pricingv1 "cost-manager/api/internal/genproto/billing/pricing/v1"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/proto"
)

const (
	// storageAdjustmentChecksumTimeLayout là định dạng thời gian chuẩn ISO-8601 đến microsecond để sinh mã băm SHA-256.
	// Chuẩn này đảm bảo tính nhất quán tuyệt đối giữa Go Service, PostgreSQL và Engine Rust.
	storageAdjustmentChecksumTimeLayout = "2006-01-02T15:04:05.000000Z07:00"

	// storagePricingCacheChannel là kênh Redis Pub/Sub phát tín hiệu khi có bảng giá Storage mới được publish.
	storagePricingCacheChannel = "billing.pricing.storage.version.published.v1"

	// storagePricingCacheKeyPrefix là tiền tố khóa lưu trữ bảng giá Storage trên Redis (L2 Cache).
	storagePricingCacheKeyPrefix = "cost-manager:storage:pricing:snapshot:v1"

	// storagePricingCacheL1TTL là thời gian tồn tại của bản ghi trong bộ nhớ RAM cục bộ (L1 Cache: 1 phút).
	storagePricingCacheL1TTL = time.Minute

	// storagePricingCacheL2TTL là thời gian tồn tại của bản ghi trên Redis (L2 Cache: 1 giờ / 3600s).
	storagePricingCacheL2TTL    = 1 * time.Hour
	storagePricingEngineChannel = "billing.pricing.schedule.version.published"
)

// storagePricingCacheItem đại diện cho một mục dữ liệu lưu trong L1 RAM Cache kèm thời hạn hết hạn.
type storagePricingCacheItem struct {
	snapshot  *entity.StoragePricingSnapshot
	expiresAt time.Time
}

// storagePricingService cung cấp toàn bộ nghiệp vụ tính cước, phát hành và xem lịch sử điều chỉnh giá Storage.
// Source of Truth (Tài liệu đặc tả kiến trúc):
// - god_view/billing/billing_storage_base_price_version_publish_god_view.md
// - god_view/billing/billing_personal_storage_estimate_god_view.md
// - god_view/billing/billing_storage_zone_price_adjustment_publish_god_view.md
type storagePricingService struct {
	repo        billingRepoInterface.StoragePricingRepository
	redisClient *goredis.Client
	wake        chan struct{}

	// cacheLoad sử dụng cơ chế SingleFlight để gom các request trùng nhau tại cùng một thời điểm,
	// ngăn chặn hiện tượng Cache Stampede (hàng nghìn request đồng thời lao xuống DB khi cache hết hạn).
	cacheLoad singleflight.Group

	// cacheMu bảo vệ truy cập đồng thời (concurrency safe) vào bộ nhớ RAM L1 cache.
	cacheMu sync.RWMutex

	// cache là bộ nhớ RAM L1 cục bộ của instance service (nhanh nhất, truy xuất microsecond).
	cache map[string]storagePricingCacheItem

	// cacheGeneration là bộ đếm thế hệ cache, tăng lên mỗi khi xóa cache để vô hiệu hóa dữ liệu cũ đang tải dở.
	cacheGeneration uint64
}

// NewStoragePricingService khởi tạo dịch vụ Storage Pricing duy nhất quản lý toàn bộ nghiệp vụ giá cước Storage.
func NewStoragePricingService(
	repo billingRepoInterface.StoragePricingRepository,
	redisClient *goredis.Client,
) billingSvcInterface.StoragePricingService {
	return &storagePricingService{
		repo:        repo,
		redisClient: redisClient,
		wake:        make(chan struct{}, 1),
		cache:       make(map[string]storagePricingCacheItem),
	}
}

// ============================================================================
// 1. WORKFLOW: DỰ TOÁN CƯỚC LƯU TRỮ (ESTIMATE STORAGE)
// ============================================================================

// EstimateStorage tính toán số tiền cước Storage dự trù theo giờ dựa trên dung lượng (bytes) và Zone người dùng chọn.
//
// Quy trình nghiệp vụ:
// 1. Kiểm tra tham số đầu vào (dung lượng byte > 0, Zone ID hợp lệ).
// 2. Tra cứu bảng giá bậc thang hiện hành qua hệ thống Cache 3 tầng (L1 RAM -> L2 Redis -> DB PostgreSQL).
// 3. Tra cứu hệ số điều chỉnh giá của Zone người dùng chọn (Zone Multiplier).
// 4. Kiểm tra mã băm SHA-256 Checksum của hệ số Zone để chống gian lận tài chính.
// 5. Tính tiền theo mô hình bậc thang lũy tiến (Progressive Brackets) bằng số học phân số big.Rat và làm tròn lên (Ceil).
// 6. Đóng gói kết quả gửi về Client.
func (s *storagePricingService) EstimateStorage(
	ctx context.Context,
	capacityBytes int64,
	zoneID uuid.UUID,
) (*entity.StorageEstimate, error) {
	// ------------------------------------------------------------------------
	// BƯỚC 1: Kiểm tra tính hợp lệ của tham số đầu vào (Input Validation)
	// ------------------------------------------------------------------------
	// Dung lượng phải lớn hơn 0 bytes, không vượt quá giới hạn hệ thống (~1 Exabyte), và Zone ID không được để trống.
	if capacityBytes <= 0 || capacityBytes > 1<<60 || zoneID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	now := time.Now().UTC()

	// ------------------------------------------------------------------------
	// BƯỚC 2: Lấy bảng giá Storage đang có hiệu lực tại thời điểm hiện tại
	// ------------------------------------------------------------------------
	// Áp dụng cơ chế Cache 3 tầng: L1 RAM -> SingleFlight gom request -> L2 Redis -> DB PostgreSQL.
	lookupKey := string(entity.ChargeKindStorageCapacity)
	s.cacheMu.RLock()
	item, ok := s.cache[lookupKey]
	s.cacheMu.RUnlock()

	var snapshot *entity.StoragePricingSnapshot
	// Kiểm tra L1 RAM Cache: nếu dữ liệu còn hạn và mốc thời gian áp dụng hợp lệ thì lấy ngay trong RAM
	if ok && now.Before(item.expiresAt) && item.snapshot != nil &&
		!item.snapshot.EffectiveFrom.After(now) &&
		(item.snapshot.EffectiveTo == nil || now.Before(*item.snapshot.EffectiveTo)) {
		snapshot = item.snapshot
	} else {
		// Nếu L1 RAM không có, dùng SingleFlight để chỉ 1 goroutine đi lấy dữ liệu từ L2 Redis/DB, các goroutine khác chờ nhận chung kết quả
		value, err, _ := s.cacheLoad.Do(lookupKey, func() (any, error) {
			s.cacheMu.RLock()
			generation := s.cacheGeneration
			cached, ready := s.cache[lookupKey]
			s.cacheMu.RUnlock()
			current := time.Now().UTC()
			// Double-check L1 cache bên trong SingleFlight
			if ready && current.Before(cached.expiresAt) && cached.snapshot != nil &&
				!cached.snapshot.EffectiveFrom.After(now) &&
				(cached.snapshot.EffectiveTo == nil || now.Before(*cached.snapshot.EffectiveTo)) {
				return cached.snapshot, nil
			}

			cacheKey := fmt.Sprintf("%s:%s", storagePricingCacheKeyPrefix, entity.ChargeKindStorageCapacity)

			// ====================================================================
			// 2.1. ĐỌC TỪ L2 CACHE (REDIS) DƯỚI DẠNG PROTOBUF BINARY (FAST PATH)
			// ====================================================================
			// Đọc dữ liệu nhị phân đã được mã hóa bằng Protobuf từ Redis cluster.
			// Dùng Protobuf giúp giải mã trực tiếp vào bộ nhớ, triệt tiêu 100% Reflection overhead của JSON.
			if raw, redisErr := s.redisClient.Get(ctx, cacheKey).Bytes(); redisErr == nil {
				var payload storagepricingv1.StoragePricingSnapshotCacheEntryV1

				// Giải mã Protobuf Binary và kiểm tra tính toàn vẹn của hợp đồng dữ liệu (Contract Invariants):
				// - PricingScheduleId và VersionId phải đủ đúng chuẩn 16 bytes UUID nhị phân.
				// - ChecksumSha256 phải đủ đúng 32 bytes của mã băm SHA-256.
				// - Đúng loại định giá dung lượng Storage (CAPACITY_GB_HOUR), mô hình bậc thang (PROGRESSIVE_UNIT), đơn vị BYTE_HOUR.
				if decodeErr := proto.Unmarshal(raw, &payload); decodeErr == nil &&
					len(payload.PricingScheduleId) == 16 && len(payload.VersionId) == 16 &&
					len(payload.ChecksumSha256) == sha256.Size && payload.ScheduleCode != "" &&
					payload.Currency != "" && payload.VersionNumber > 0 &&
					payload.ChargeKind == storagepricingv1.StorageChargeKindV1_STORAGE_CHARGE_KIND_V1_CAPACITY_GB_HOUR &&
					payload.PricingModel == storagepricingv1.StoragePricingModelV1_STORAGE_PRICING_MODEL_V1_PROGRESSIVE_UNIT &&
					payload.RawInputUnit == storagepricingv1.StorageRawInputUnitV1_STORAGE_RAW_INPUT_UNIT_V1_BYTE_HOUR {

					// Chuyển đổi UUID từ dạng 16 bytes nhị phân sang kiểu uuid.UUID chuẩn
					scheduleID, scheduleErr := uuid.FromBytes(payload.PricingScheduleId)
					versionID, versionErr := uuid.FromBytes(payload.VersionId)
					if scheduleErr == nil && versionErr == nil && len(payload.Brackets) > 0 {
						brackets := make([]entity.StoragePricingSnapshotBracket, len(payload.Brackets))
						validBrackets := true

						// Chuyển đổi từng bậc thang giá từ Protobuf message sang domain entity StoragePricingSnapshotBracket
						for index, bracket := range payload.Brackets {
							if bracket == nil || len(bracket.Id) != 16 {
								validBrackets = false
								break
							}
							bracketID, bracketErr := uuid.FromBytes(bracket.Id)
							if bracketErr != nil {
								validBrackets = false
								break
							}
							var rangeEnd *int64
							if bracket.RangeEndQuantity != nil {
								value := *bracket.RangeEndQuantity
								rangeEnd = &value
							}
							brackets[index] = entity.StoragePricingSnapshotBracket{
								ID:                       bracketID,
								RangeStartQuantity:       bracket.RangeStartQuantity,
								RangeEndQuantity:         rangeEnd,
								PriceNumeratorMicroUnits: bracket.PriceNumeratorMicroUnits,
								PriceDenominatorQuantity: bracket.PriceDenominatorQuantity,
							}
						}

						// Kiểm tra tính liên tục của các bậc thang giá (Progressive Continuity Invariant):
						// 1. Bậc đầu tiên phải bắt đầu từ đúng 0 byte.
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
							effectiveFrom := time.UnixMicro(payload.EffectiveFromUnixMicros).UTC()
							var effectiveTo *time.Time
							if payload.EffectiveToUnixMicros != nil {
								value := time.UnixMicro(*payload.EffectiveToUnixMicros).UTC()
								effectiveTo = &value
							}
							if effectiveTo != nil && !effectiveTo.After(effectiveFrom) {
								validBrackets = false
							}

							snap := &entity.StoragePricingSnapshot{
								PricingScheduleID: scheduleID,
								VersionID:         versionID,
								Code:              payload.ScheduleCode,
								ChargeKindCode:    entity.ChargeKindStorageCapacity,
								ModuleCode:        "storage",
								PricingModel:      entity.PricingModelProgressiveUnit,
								RawInputUnit:      "BYTE_HOUR",
								VersionNumber:     int(payload.VersionNumber),
								EffectiveFrom:     effectiveFrom,
								EffectiveTo:       effectiveTo,
								Checksum:          hex.EncodeToString(payload.ChecksumSha256),
								Currency:          payload.Currency,
								Brackets:          brackets,
							}

							// Tính lại SHA-256 Checksum độc lập từ các trường dữ liệu và so sánh với Checksum niêm phong
							// Điều này đảm bảo dữ liệu lưu trong Redis chưa từng bị ai sửa đổi hoặc bị lỗi byte khi truyền tải.
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
							write(snap.EffectiveFrom.UTC().Format(storageAdjustmentChecksumTimeLayout))
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

							// Nếu snapshot từ Redis hoàn toàn hợp lệ và đang trong khung giờ hiệu lực:
							// Ghi ngay vào L1 RAM Cache của tiến trình hiện tại để phục vụ các request tiếp theo trong microsecond!
							if validBrackets && !snap.EffectiveFrom.After(now) && (snap.EffectiveTo == nil || now.Before(*snap.EffectiveTo)) {
								s.cacheMu.Lock()
								if s.cacheGeneration == generation {
									s.cache[lookupKey] = storagePricingCacheItem{
										snapshot:  snap,
										expiresAt: current.Add(storagePricingCacheL1TTL),
									}
								}
								s.cacheMu.Unlock()
								return snap, nil
							}
						}
					}
				}
			}

			// ====================================================================
			// 2.2. TRUY VẤN CƠ SỞ DỮ LIỆU GỐC (POSTGRESQL - FALLBACK PATH)
			// ====================================================================
			// Chạy khi L1 RAM và L2 Redis đều bị miss (hoặc dữ liệu trên Redis không đạt chuẩn kiểm tra).
			// Truy vấn lấy bản ghi mới nhất đang active bằng câu lệnh SQL CTE tối ưu.
			dbSnap, repoErr := s.repo.GetActiveStoragePricingSnapshot(ctx, entity.ChargeKindStorageCapacity, now)
			if repoErr != nil {
				return nil, repoErr
			}

			// Kiểm tra cấu trúc bản ghi DB có đầy đủ thông tin định danh và mô hình định giá bắt buộc
			if dbSnap == nil || dbSnap.PricingScheduleID == uuid.Nil || dbSnap.VersionID == uuid.Nil ||
				dbSnap.Code == "" || dbSnap.ModuleCode != "storage" || dbSnap.Currency == "" ||
				dbSnap.VersionNumber < 1 || dbSnap.PricingModel != entity.PricingModelProgressiveUnit ||
				dbSnap.ChargeKindCode != entity.ChargeKindStorageCapacity {
				return nil, fmt.Errorf("Storage pricing snapshot is incomplete")
			}

			// Storage bắt buộc phải tính theo đơn vị dung lượng thời gian: Byte-Giờ (BYTE_HOUR)
			if dbSnap.RawInputUnit != "BYTE_HOUR" {
				return nil, fmt.Errorf("Storage pricing snapshot unit mismatch")
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

			// ====================================================================
			// 2.3. KIỂM TRA CHỮ KÝ BẢO MẬT (SHA-256 CHECKSUM) CỦA BẢN GHI DB
			// ====================================================================
			// Đảm bảo không ai có thể can thiệp trực tiếp vào DB để sửa đổi đơn giá bất hợp pháp.
			// Checksum được tính toán từ toàn bộ các trường nghiệp vụ và đối chiếu với giá trị niêm phong lúc Publish.
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
			write(dbSnap.EffectiveFrom.UTC().Format(storageAdjustmentChecksumTimeLayout))
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
				return nil, fmt.Errorf("Storage pricing snapshot checksum mismatch")
			}

			// ====================================================================
			// 2.4. LƯU BẢNG GIÁ HỢP LỆ VÀO L2 REDIS CACHE DƯỚI DẠNG PROTOBUF BINARY
			// ====================================================================
			// Tuần tự hóa đối tượng sang Protobuf byte array siêu nhẹ và ghi vào Redis cluster (TTL 5 phút).
			s.cacheMu.RLock()
			generationCurrent := s.cacheGeneration == generation
			s.cacheMu.RUnlock()
			if generationCurrent {
				checksumBytes, checksumErr := hex.DecodeString(dbSnap.Checksum)
				if checksumErr == nil && len(checksumBytes) == sha256.Size {
					brackets := make([]*storagepricingv1.StoragePricingScalarBracketV1, len(dbSnap.Brackets))
					for index, bracket := range dbSnap.Brackets {
						entry := &storagepricingv1.StoragePricingScalarBracketV1{
							Id:                       bracket.ID[:],
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
					entry := &storagepricingv1.StoragePricingSnapshotCacheEntryV1{
						PricingScheduleId:       dbSnap.PricingScheduleID[:],
						VersionId:               dbSnap.VersionID[:],
						ScheduleCode:            dbSnap.Code,
						ChargeKind:              storagepricingv1.StorageChargeKindV1_STORAGE_CHARGE_KIND_V1_CAPACITY_GB_HOUR,
						PricingModel:            storagepricingv1.StoragePricingModelV1_STORAGE_PRICING_MODEL_V1_PROGRESSIVE_UNIT,
						RawInputUnit:            storagepricingv1.StorageRawInputUnitV1_STORAGE_RAW_INPUT_UNIT_V1_BYTE_HOUR,
						VersionNumber:           uint32(dbSnap.VersionNumber),
						EffectiveFromUnixMicros: dbSnap.EffectiveFrom.UTC().UnixMicro(),
						ChecksumSha256:          checksumBytes,
						Currency:                dbSnap.Currency,
						Brackets:                brackets,
					}
					if dbSnap.EffectiveTo != nil {
						value := dbSnap.EffectiveTo.UTC().UnixMicro()
						entry.EffectiveToUnixMicros = &value
					}
					// Mã hóa Protobuf binary (0 reflection, kích thước chỉ ~200 bytes)
					if payload, marshalErr := proto.Marshal(entry); marshalErr == nil {
						_ = s.redisClient.Set(ctx, cacheKey, payload, storagePricingCacheL2TTL).Err()
					}
				}
			}

			// ====================================================================
			// 2.5. LƯU BẢNG GIÁ VÀO L1 RAM CACHE CỦA TIẾN TRÌNH HIỆN TẠI
			// ====================================================================
			// Đảm bảo không bị ghi đè dữ liệu cũ nếu cacheGeneration đã bị thay đổi bởi worker invalidation.
			s.cacheMu.Lock()
			if s.cacheGeneration == generation {
				s.cache[lookupKey] = storagePricingCacheItem{
					snapshot:  dbSnap,
					expiresAt: time.Now().UTC().Add(storagePricingCacheL1TTL),
				}
			}
			s.cacheMu.Unlock()

			return dbSnap, nil
		})
		if err != nil {
			return nil, err
		}
		snapVal, ok := value.(*entity.StoragePricingSnapshot)
		if !ok || snapVal == nil {
			return nil, fmt.Errorf("Storage pricing cache returned unexpected value %T", value)
		}
		snapshot = snapVal
	}

	// ------------------------------------------------------------------------
	// BƯỚC 3: Lấy hệ số điều chỉnh giá riêng của khu vực / Zone (Zone Multiplier)
	// ------------------------------------------------------------------------
	// Ví dụ: Zone Singapore có chi phí vận hành cao hơn thì hệ số có thể là 105/100 (+5%).
	// Nếu Zone chưa có cấu hình riêng thì mặc định hệ số là 1/1 (không tăng giảm).
	adjustment, err := s.repo.GetActiveStorageZonePriceAdjustment(ctx, zoneID, now)
	if err != nil {
		return nil, err
	}

	// ------------------------------------------------------------------------
	// BƯỚC 4: Kiểm tra tính toàn vẹn dữ liệu (Checksum SHA-256) của hệ số Zone
	// ------------------------------------------------------------------------
	// Đảm bảo hệ số Zone lấy từ DB chưa bị sửa đổi trái phép.
	if adjustment != nil {
		hash := sha256.New()
		for _, value := range []string{
			adjustment.ZoneID.String(),
			fmt.Sprintf("%d", adjustment.VersionNumber),
			adjustment.EffectiveFrom.UTC().Format(storageAdjustmentChecksumTimeLayout),
			fmt.Sprintf("%d", adjustment.MultiplierNumerator),
			fmt.Sprintf("%d", adjustment.MultiplierDenominator),
		} {
			var length [8]byte
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
		if fmt.Sprintf("%x", hash.Sum(nil)) != adjustment.Checksum {
			return nil, fmt.Errorf("Storage Zone price adjustment checksum mismatch")
		}
	}

	// ------------------------------------------------------------------------
	// BƯỚC 5: Tính toán tiền cước theo mô hình bậc thang Progressive Unit Brackets
	// ------------------------------------------------------------------------
	// Nguyên lý:
	// - Dung lượng rơi vào dải nào thì nhân đơn giá dải đó (ví dụ 50GB đầu giá X, phần vượt mức giá Y).
	// - Dùng phân số chính xác tuyệt đối (big.Rat) để triệt tiêu hoàn toàn sai số dấu phẩy động (Floating Point Error).
	// - Nhân hệ số khu vực (Zone Multiplier).
	// - Làm tròn lên (Ceil) ở ranh giới micro-units (1/1.000.000 USD).
	capacityByteHours := uint64(capacityBytes)
	adjustmentNumerator, adjustmentDenominator := int64(1), int64(1)
	if adjustment != nil {
		adjustmentNumerator = adjustment.MultiplierNumerator
		adjustmentDenominator = adjustment.MultiplierDenominator
	}

	total := new(big.Rat)
	for _, bracket := range snapshot.Brackets {
		start := uint64(bracket.RangeStartQuantity)
		if capacityByteHours <= start {
			break
		}

		upper := capacityByteHours
		if bracket.RangeEndQuantity != nil && uint64(*bracket.RangeEndQuantity) < upper {
			upper = uint64(*bracket.RangeEndQuantity)
		}
		if upper <= start {
			continue
		}

		// Số lượng đơn vị trong bậc này = min(capacity, end) - start
		units := new(big.Int).SetUint64(upper - start)
		numerator := new(big.Int).Mul(units, big.NewInt(bracket.PriceNumeratorMicroUnits))
		total.Add(total, new(big.Rat).SetFrac(numerator, big.NewInt(bracket.PriceDenominatorQuantity)))
	}

	// Áp dụng hệ số khu vực (Zone Multiplier) nếu có
	if adjustmentNumerator != 1 || adjustmentDenominator != 1 {
		if adjustmentNumerator < 0 || adjustmentDenominator <= 0 {
			return nil, billingTaxonomy.ErrInvalidArgument
		}
		total.Mul(total, new(big.Rat).SetFrac(big.NewInt(adjustmentNumerator), big.NewInt(adjustmentDenominator)))
	}

	// Làm tròn lên (Ceiling): nếu có phần dư nhỏ hơn 1 micro-unit thì làm tròn lên +1 micro-unit
	ceil := new(big.Int).Quo(total.Num(), total.Denom())
	if new(big.Int).Mod(total.Num(), total.Denom()).Sign() != 0 {
		ceil.Add(ceil, big.NewInt(1))
	}
	if !ceil.IsInt64() {
		return nil, fmt.Errorf("pricing charge exceeds BIGINT capacity")
	}
	hourly := ceil.Int64()

	// ------------------------------------------------------------------------
	// BƯỚC 6: Đóng gói dữ liệu kết quả dự toán và gửi về Client
	// ------------------------------------------------------------------------
	var adjustmentID *uuid.UUID
	var adjustmentVersion *int
	var adjustmentChecksum *string
	if adjustment != nil {
		adjustmentID = &adjustment.ID
		adjustmentVersion = &adjustment.VersionNumber
		adjustmentChecksum = &adjustment.Checksum
		adjustmentNumerator = adjustment.MultiplierNumerator
		adjustmentDenominator = adjustment.MultiplierDenominator
	}

	return &entity.StorageEstimate{
		CapacityBytes:             capacityBytes,
		HourlyMicroUnits:          hourly,
		Currency:                  snapshot.Currency,
		PricingScheduleCode:       snapshot.Code,
		PricingScheduleID:         snapshot.PricingScheduleID,
		PricingScheduleVersionID:  snapshot.VersionID,
		PricingVersion:            snapshot.VersionNumber,
		PricingChecksum:           snapshot.Checksum,
		PricingEffectiveFrom:      snapshot.EffectiveFrom,
		RateAdjustmentID:          adjustmentID,
		RateAdjustmentVersion:     adjustmentVersion,
		RateAdjustmentChecksum:    adjustmentChecksum,
		RateAdjustmentNumerator:   adjustmentNumerator,
		RateAdjustmentDenominator: adjustmentDenominator,
		EstimatedAt:               time.Now().UTC(),
	}, nil
}

// ============================================================================
// 2. WORKFLOW: BAN HÀNH BẢNG GIÁ GỐC STORAGE (PUBLISH BASE PRICE VERSION)
// ============================================================================

// CreateStorageBasePriceVersion xử lý validate, sắp xếp bậc thang, tạo checksum và publish phiên bản bảng giá Storage gốc mới.
//
// Quy trình nghiệp vụ:
// 1. Chuẩn hóa chuỗi văn bản và cắt thời gian về chuẩn microsecond.
// 2. Kiểm tra tính hợp lệ của tham số và ràng buộc không được ban hành giá lùi về quá khứ.
// 3. Lấy thông tin Pricing Schedule mục tiêu và kiểm tra đúng loại bảng giá Storage.
// 4. Sắp xếp và kiểm tra tính liên tục của các dải bậc thang giá [0 -> vô cực).
// 5. Sinh mã băm SHA-256 Checksum niêm phong toàn bộ nội dung phiên bản giá.
// 6. Lưu phiên bản mới vào cơ sở dữ liệu PostgreSQL (phiên bản cũ chuyển trạng thái SUPERSEDED).
// 7. Bắn sự kiện Protobuf lên Redis Pub/Sub để các node khác xóa cache ngay lập tức.
func (s *storagePricingService) CreateStorageBasePriceVersion(
	ctx context.Context,
	create entity.StorageBasePricePublishCommand,
	brackets []entity.StorageBasePricePublishBracket,
) (*entity.StorageBasePricePublished, []entity.StorageBasePricePublishBracket, error) {
	// Bước 1: Chuẩn hóa dữ liệu đầu vào
	create.ScheduleCode = strings.TrimSpace(create.ScheduleCode)
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	create.EffectiveFrom = create.EffectiveFrom.UTC().Truncate(time.Microsecond)

	if create.ScheduleCode == "" || create.ChangeReason == "" || len(create.ChangeReason) > 2_000 ||
		create.ExpectedLatestVersion < 0 || create.CreatedBy == uuid.Nil ||
		create.EffectiveFrom.IsZero() || len(brackets) > 1_000 {
		return nil, nil, billingTaxonomy.ErrInvalidArgument
	}

	// Không cho phép ban hành giá có thời gian hiệu lực lùi về quá khứ quá 1 phút
	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, nil, billingTaxonomy.ErrPricingScheduleEffectiveConflict
	}

	// Bước 2: Kiểm tra đối tượng bảng giá có tồn tại trong hệ thống không
	target, err := s.repo.GetStorageBasePricePublishTarget(ctx, create.ScheduleCode)
	if err != nil {
		return nil, nil, err
	}

	// Đảm bảo chỉ áp dụng cho nhóm dịch vụ Storage và mô hình bậc thang Progressive Unit
	if (target.ChargeKindCode != entity.ChargeKindStorageCapacity &&
		target.ChargeKindCode != entity.ChargeKindStorageNetworkIn &&
		target.ChargeKindCode != entity.ChargeKindStorageNetworkOut) ||
		target.PricingModel != entity.PricingModelProgressiveUnit {
		return nil, nil, billingTaxonomy.ErrInvalidArgument
	}

	// Bước 3: Sắp xếp các bậc giá tăng dần theo mức dung lượng bắt đầu (RangeStartQuantity)
	brackets = append([]entity.StorageBasePricePublishBracket(nil), brackets...)
	sort.Slice(brackets, func(i, j int) bool {
		return brackets[i].RangeStartQuantity < brackets[j].RangeStartQuantity
	})

	// Bước 4: Validate tính liên tục của các bậc thang giá
	// Bậc đầu tiên bắt buộc phải bắt đầu từ 0, bậc cuối cùng phải mở đến vô cực (RangeEndQuantity == nil),
	// và không được có khoảng trống (gap) hoặc trùng lặp (overlap) giữa các bậc.
	if len(brackets) == 0 || brackets[0].RangeStartQuantity != 0 {
		return nil, nil, billingTaxonomy.ErrInvalidPricingBrackets
	}
	for index, bracket := range brackets {
		if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 {
			return nil, nil, billingTaxonomy.ErrInvalidPricingBrackets
		}
		if index == len(brackets)-1 {
			if bracket.RangeEndQuantity != nil {
				return nil, nil, billingTaxonomy.ErrInvalidPricingBrackets
			}
			continue
		}
		if bracket.RangeEndQuantity == nil || *bracket.RangeEndQuantity != brackets[index+1].RangeStartQuantity {
			return nil, nil, billingTaxonomy.ErrInvalidPricingBrackets
		}
	}

	// Bước 5: Sinh mã băm SHA-256 Checksum bất biến để niêm phong dữ liệu phiên bản mới
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
	write(create.EffectiveFrom.UTC().Format(storageAdjustmentChecksumTimeLayout))
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

	// Bước 6: Lưu phiên bản mới vào cơ sở dữ liệu PostgreSQL
	published, err := s.repo.CreateStorageBasePriceVersion(ctx, create, brackets)
	if err != nil {
		return nil, nil, err
	}

	s.NotifyPricingOutbox()

	// Bước 7: Bắn sự kiện Protobuf lên Redis Pub/Sub để các node khác nhận biết và xóa cache L1/L2
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
	if s.redisClient != nil {
		if payload, marshalErr := proto.Marshal(event); marshalErr != nil {
			logger.SysWarn("billing.storage.pricing.cache.publish", marshalErr.Error())
		} else if publishErr := s.redisClient.Publish(ctx, storagePricingCacheChannel, payload).Err(); publishErr != nil && ctx.Err() == nil {
			logger.SysWarn("billing.storage.pricing.cache.publish", publishErr.Error())
		}
	}

	return published, brackets, nil
}

// ============================================================================
// 3. WORKFLOW: ĐIỀU CHỈNH GIÁ THEO ZONE (ZONE ADJUSTMENT PUBLISH & LIST)
// ============================================================================

// CreateStorageZonePriceAdjustment thực thi quy trình phát hành phiên bản hệ số giá mới cho một Zone.
//
// Quy trình nghiệp vụ:
// 1. Validate tham số (Zone ID, người tạo, hệ số nhân dương, thời gian hiệu lực).
// 2. Không cho phép ban hành hệ số lùi về quá khứ quá 1 phút.
// 3. Sinh chữ ký bảo mật SHA-256 niêm phong dữ liệu hệ số Zone.
// 4. Lưu phiên bản mới vào cơ sở dữ liệu PostgreSQL.
func (s *storagePricingService) CreateStorageZonePriceAdjustment(
	ctx context.Context,
	create entity.StorageZoneAdjustmentPublishCommand,
) (*entity.StorageZoneAdjustmentPublished, error) {
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	create.EffectiveFrom = create.EffectiveFrom.UTC().Truncate(time.Microsecond)

	if create.ZoneID == uuid.Nil || create.CreatedBy == uuid.Nil || create.ExpectedLatestVersion < 0 ||
		create.EffectiveFrom.IsZero() || create.ChangeReason == "" || len(create.ChangeReason) > 2_000 ||
		create.MultiplierNumerator < 0 || create.MultiplierDenominator <= 0 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, billingTaxonomy.ErrStorageZoneAdjustmentConflict
	}

	// Sinh chữ ký bảo mật SHA-256 bất biến cho phiên bản điều chỉnh giá Zone
	hash := sha256.New()
	for _, value := range []string{
		create.ZoneID.String(),
		fmt.Sprintf("%d", create.ExpectedLatestVersion+1),
		create.EffectiveFrom.UTC().Format(storageAdjustmentChecksumTimeLayout),
		fmt.Sprintf("%d", create.MultiplierNumerator),
		fmt.Sprintf("%d", create.MultiplierDenominator),
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	create.Checksum = fmt.Sprintf("%x", hash.Sum(nil))

	return s.repo.CreateStorageZonePriceAdjustment(ctx, create)
}

// ListStorageZonePriceAdjustments lấy danh sách phân trang lịch sử các phiên bản giá của một Zone.
func (s *storagePricingService) ListStorageZonePriceAdjustments(
	ctx context.Context,
	query entity.StorageZoneAdjustmentListQuery,
) (*entity.StorageZoneAdjustmentListResult, error) {
	items, hasMore, err := s.repo.ListStorageZonePriceAdjustments(ctx, query)
	if err != nil {
		return nil, err
	}

	return &entity.StorageZoneAdjustmentListResult{
		ZoneID:     query.ZoneID,
		Items:      items,
		HasMore:    hasMore,
		ObservedAt: time.Now().UTC(),
	}, nil
}

// ============================================================================
// 4. WORKFLOW: INVALIDATION WORKER
// ============================================================================

// RunPricingCacheInvalidation chạy worker lắng nghe tín hiệu Redis Pub/Sub để xóa Cache ngay lập tức khi bảng giá thay đổi.
//
// Quy trình nghiệp vụ:
// 1. Subscribe vào Redis channel "billing.pricing.storage.version.published.v1".
// 2. Khi nhận được tin nhắn, giải mã Protobuf payload thành PricingScheduleVersionPublished.
// 3. Kiểm tra tính hợp lệ của sự kiện (đúng loại ChargeKind của Storage, version >= 1, checksum đủ 64 ký tự).
// 4. Xóa chính xác mục cache trong L1 RAM và xóa key trên L2 Redis với độ phức tạp O(1).
// 5. Tự động kết nối lại (Reconnect) nếu kết nối Redis bị gián đoạn.
func (s *storagePricingService) RunPricingCacheInvalidation(ctx context.Context) {
	for {
		pubsub := s.redisClient.Subscribe(ctx, storagePricingCacheChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			_ = pubsub.Close()
			if ctx.Err() != nil {
				return
			}
			logger.SysWarn("billing.storage.pricing.cache.subscribe", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}

		messages := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				_ = pubsub.Close()
				return
			case message, ok := <-messages:
				if !ok {
					_ = pubsub.Close()
					goto reconnect
				}
				if len(message.Payload) == 0 || len(message.Payload) > 64*1024 {
					logger.SysWarn("billing.storage.pricing.cache.event", "Storage pricing cache event size is invalid")
					continue
				}
				var event pricingv1.PricingScheduleVersionPublished
				if err := proto.Unmarshal([]byte(message.Payload), &event); err != nil {
					logger.SysWarn("billing.storage.pricing.cache.event", "Storage pricing cache event is not valid protobuf")
					continue
				}
				chargeKind := entity.ChargeKindCode(event.ChargeKindCode)
				if (chargeKind != entity.ChargeKindStorageCapacity &&
					chargeKind != entity.ChargeKindStorageNetworkIn &&
					chargeKind != entity.ChargeKindStorageNetworkOut) ||
					event.VersionNumber < 1 ||
					event.PricingScheduleId == "" || event.PricingScheduleVersionId == "" ||
					len(event.Checksum) != 64 {
					logger.SysWarn("billing.storage.pricing.cache.event", "Storage pricing cache event contract is invalid")
					continue
				}

				// Xóa L1 RAM cache cục bộ
				s.cacheMu.Lock()
				s.cacheGeneration++
				delete(s.cache, string(chargeKind))
				s.cacheMu.Unlock()

				// Xóa L2 Redis cache chính xác theo key O(1)
				cacheKey := fmt.Sprintf("%s:%s", storagePricingCacheKeyPrefix, chargeKind)
				if err := s.redisClient.Del(ctx, cacheKey).Err(); err != nil && ctx.Err() == nil {
					logger.SysWarn("billing.storage.pricing.cache.invalidate", err.Error())
				}
			}
		}
	reconnect:
	}
}

func (s *storagePricingService) NotifyPricingOutbox() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *storagePricingService) RunPricingOutboxRelay(ctx context.Context) {
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
			if err := s.repo.RefreshStoragePricingStatuses(ctx); err != nil && ctx.Err() == nil {
				logger.SysError("billing.storage.pricing.outbox.status", err.Error())
			}
			reconcile.Reset(30*time.Second + time.Duration(rand.IntN(10))*time.Second)
		}
		for ctx.Err() == nil {
			claimToken := uuid.New()
			rows, err := s.repo.ClaimStoragePricingOutbox(ctx, claimToken, time.Now().UTC().Add(30*time.Second), 100)
			if err != nil {
				logger.SysError("billing.storage.pricing.outbox.claim", err.Error())
				break
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				event := &pricingv1.PricingScheduleVersionPublished{EventId: row.ID.String(), PricingScheduleId: row.PricingScheduleID.String(), PricingScheduleVersionId: row.VersionID.String(), VersionNumber: row.VersionNumber, ChargeKindCode: string(row.ChargeKindCode), EffectiveFromUnixMs: row.EffectiveFrom.UnixMilli(), Checksum: row.Checksum, OccurredAtUnixMs: row.OccurredAt.UnixMilli()}
				payload, publishErr := proto.Marshal(event)
				if publishErr == nil && s.redisClient != nil {
					publishErr = s.redisClient.Publish(ctx, storagePricingEngineChannel, payload).Err()
				}
				if publishErr == nil && s.redisClient != nil {
					publishErr = s.redisClient.Publish(ctx, storagePricingCacheChannel, payload).Err()
				}
				if s.redisClient == nil && publishErr == nil {
					publishErr = fmt.Errorf("Storage pricing outbox Redis is unavailable")
				}
				if publishErr != nil {
					backoffSeconds := 1 << min(row.RetryCount, 6)
					availableAt := time.Now().UTC().Add(time.Duration(backoffSeconds+rand.IntN(backoffSeconds+1)) * time.Second)
					if err := s.repo.RetryStoragePricingOutbox(ctx, row.ID, row.ClaimToken, publishErr.Error(), availableAt); err != nil && ctx.Err() == nil {
						logger.SysError("billing.storage.pricing.outbox.retry", err.Error())
					}
					continue
				}
				if err := s.repo.MarkStoragePricingOutboxPublished(ctx, row.ID, row.ClaimToken); err != nil && ctx.Err() == nil {
					logger.SysError("billing.storage.pricing.outbox.publish", err.Error())
				}
			}
		}
	}
}
