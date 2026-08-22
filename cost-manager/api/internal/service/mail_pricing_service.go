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
	mailpricingv1 "cost-manager/api/internal/genproto/billing/pricing/mail/v1"
	pricingv1 "cost-manager/api/internal/genproto/billing/pricing/v1"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/proto"
)

const (
	// Định dạng thời gian chuẩn ISO-8601 Microsecond dùng để băm Checksum toàn vẹn
	mailPricingChecksumTimeLayout = "2006-01-02T15:04:05.000000Z07:00"

	// Kênh Redis Pub/Sub phát tín hiệu khi có bảng giá Mail mới được Publish
	mailPricingCacheChannel   = "billing.pricing.mail.version.published.v1"
	mailPricingCacheKeyPrefix = "cost-manager:mail:pricing:snapshot:v1"

	// Thời gian sống (TTL) của bộ nhớ đệm
	mailPricingCacheL1TTL    = time.Minute     // L1 Cache (RAM trong tiến trình): 1 phút
	mailPricingCacheL2TTL    = 5 * time.Minute // L2 Cache (Redis Cluster): 5 phút
	mailPricingEngineChannel = "billing.pricing.schedule.version.published"
)

// mailPricingCacheItem đại diện cho một phần tử bảng giá được lưu tạm trong bộ nhớ RAM L1.
type mailPricingCacheItem struct {
	snapshot  *entity.MailPricingSnapshot
	expiresAt time.Time
}

// mailPricingService quản lý toàn bộ vòng đời của nghiệp vụ tính giá Mail Service:
// - Dự toán chi phí gửi mail (Estimate)
// - Ban hành phiên bản bảng giá gốc (Base Price Publishing)
// - Quản lý hệ số điều chỉnh giá theo khu vực (Zone Multipliers)
// - Tự động đồng bộ và xóa Cache khi có thay đổi (Cache Invalidation)
type mailPricingService struct {
	repo        billingRepoInterface.MailPricingRepository
	redisClient *goredis.Client

	cacheLoad       singleflight.Group
	cacheMu         sync.RWMutex
	cache           map[string]mailPricingCacheItem
	cacheGeneration uint64
	wake            chan struct{}
}

// NewMailPricingService khởi tạo đối tượng Mail Pricing Service duy nhất và đầy đủ.
func NewMailPricingService(
	repo billingRepoInterface.MailPricingRepository,
	redisClient *goredis.Client,
) billingSvcInterface.MailPricingService {
	return &mailPricingService{
		repo:        repo,
		redisClient: redisClient,
		cache:       make(map[string]mailPricingCacheItem),
		wake:        make(chan struct{}, 1),
	}
}

// ============================================================================
// 1. WORKFLOW: DỰ TOÁN CƯỚC MAIL (ESTIMATE MAIL)
// ============================================================================

// EstimateMail tính toán số tiền cước Mail dự trù dựa trên số lượng người nhận (recipientQuantity) và Zone ID.
// Luồng xử lý End-to-End được viết hoàn toàn Inline:
// 1. Kiểm tra tính hợp lệ của tham số đầu vào.
// 2. Tra cứu Cache 3 tầng (L1 RAM -> SingleFlight Stampede Guard -> L2 Redis Protobuf -> DB PostgreSQL).
// 3. Tra cứu hệ số điều chỉnh giá khu vực (Zone Price Adjustment) từ DB.
// 4. Xác thực chữ ký Checksum SHA-256 của hệ số Zone.
// 5. Tính toán cước phí lũy tiến bậc thang bằng số học phân số chính xác tuyệt đối (big.Rat).
// 6. Đóng gói kết quả chi tiết kèm mã Checksum niêm phong gửi về cho Client.
func (s *mailPricingService) EstimateMail(
	ctx context.Context,
	recipientQuantity int64,
	zoneID uuid.UUID,
) (*entity.MailEstimate, error) {
	// ------------------------------------------------------------------------
	// BƯỚC 1: KIỂM TRA TÍNH HỢP LỆ CỦA ĐẦU VÀO (INPUT VALIDATION)
	// ------------------------------------------------------------------------
	// - Số lượng người nhận phải từ 1 đến 1 tỷ recipients.
	// - ZoneID không được là UUID rỗng (uuid.Nil).
	if recipientQuantity < 1 || recipientQuantity > 1_000_000_000 || zoneID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	now := time.Now().UTC()

	// ------------------------------------------------------------------------
	// BƯỚC 2: TRA CỨU BẢNG GIÁ MAIL ĐANG HIỆU LỰC (CACHE 3 TẦNG)
	// ------------------------------------------------------------------------
	lookupKey := string(entity.ChargeKindMailAcceptedRecipient)

	// 2.A. Kiểm tra nhanh tại Tầng L1 Cache (RAM trong tiến trình hiện tại)
	s.cacheMu.RLock()
	item, ok := s.cache[lookupKey]
	s.cacheMu.RUnlock()

	var snapshot *entity.MailPricingSnapshot
	if ok && now.Before(item.expiresAt) && item.snapshot != nil &&
		!item.snapshot.EffectiveFrom.After(now) &&
		(item.snapshot.EffectiveTo == nil || now.Before(*item.snapshot.EffectiveTo)) {
		// Trúng L1 Cache -> Lấy trực tiếp từ RAM trong microsecond (Fast Path)
		snapshot = item.snapshot
	} else {
		// 2.B. Nếu L1 Cache bị Miss: Dùng SingleFlight để chặn nghẽn tải (Cache Stampede Protection)
		// Đảm bảo chỉ đúng 1 Goroutine đi tải dữ liệu từ Redis/DB, các Goroutine khác cùng chờ nhận kết quả.
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

			cacheKey := fmt.Sprintf("%s:%s", mailPricingCacheKeyPrefix, entity.ChargeKindMailAcceptedRecipient)

			// ================================================================
			// 2.1. ĐỌC TỪ L2 CACHE (REDIS) DƯỚI DẠNG PROTOBUF BINARY (FAST PATH)
			// ================================================================
			// Đọc dữ liệu nhị phân đã được mã hóa bằng Protobuf từ Redis cluster.
			// Dùng Protobuf giúp giải mã trực tiếp vào bộ nhớ, triệt tiêu 100% Reflection overhead của JSON.
			if s.redisClient != nil {
				if raw, redisErr := s.redisClient.Get(ctx, cacheKey).Bytes(); redisErr == nil {
					var payload mailpricingv1.MailPricingSnapshotCacheEntryV1

					// Giải mã Protobuf Binary và kiểm tra tính toàn vẹn của hợp đồng dữ liệu (Contract Invariants):
					// - PricingScheduleId và VersionId phải đủ đúng chuẩn 16 bytes UUID nhị phân.
					// - ChecksumSha256 phải đủ đúng 32 bytes của mã băm SHA-256.
					// - Đúng loại định giá Mail (mail_accepted_recipient) và đơn vị RECIPIENT.
					if decodeErr := proto.Unmarshal(raw, &payload); decodeErr == nil &&
						len(payload.PricingScheduleId) == 16 && len(payload.VersionId) == 16 &&
						len(payload.ChecksumSha256) == sha256.Size && payload.Code != "" &&
						payload.Currency != "" && payload.VersionNumber > 0 &&
						payload.ChargeKindCode == string(entity.ChargeKindMailAcceptedRecipient) &&
						payload.RawInputUnit == "RECIPIENT" {

						// Chuyển đổi UUID từ 16 bytes nhị phân sang kiểu uuid.UUID chuẩn
						scheduleID, scheduleErr := uuid.FromBytes(payload.PricingScheduleId)
						versionID, versionErr := uuid.FromBytes(payload.VersionId)
						if scheduleErr == nil && versionErr == nil && len(payload.Brackets) > 0 {
							brackets := make([]entity.MailPricingSnapshotBracket, len(payload.Brackets))
							validBrackets := true

							// Chuyển đổi từng bậc thang giá từ Protobuf message sang domain entity MailPricingSnapshotBracket
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
								brackets[index] = entity.MailPricingSnapshotBracket{
									RangeStartQuantity:       bracket.RangeStartQuantity,
									RangeEndQuantity:         rangeEnd,
									PriceNumeratorMicroUnits: bracket.PriceNumeratorMicroUnits,
									PriceDenominatorQuantity: bracket.PriceDenominatorQuantity,
								}
							}

							// Kiểm tra tính liên tục của các bậc thang giá (Progressive Continuity Invariant):
							// 1. Bậc đầu tiên phải bắt đầu từ đúng 0 recipient.
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

								snap := &entity.MailPricingSnapshot{
									PricingScheduleID: scheduleID,
									VersionID:         versionID,
									Code:              payload.Code,
									ChargeKindCode:    entity.ChargeKindMailAcceptedRecipient,
									ModuleCode:        "mail",
									PricingModel:      entity.PricingModelProgressiveUnit,
									RawInputUnit:      "RECIPIENT",
									VersionNumber:     int(payload.VersionNumber),
									EffectiveFrom:     effectiveFrom,
									EffectiveTo:       effectiveTo,
									Checksum:          hex.EncodeToString(payload.ChecksumSha256),
									Currency:          payload.Currency,
									Brackets:          brackets,
								}

								// Tính lại SHA-256 Checksum độc lập từ các trường dữ liệu và so sánh với Checksum niêm phong
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
								write(snap.EffectiveFrom.UTC().Format(mailPricingChecksumTimeLayout))
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
										s.cache[lookupKey] = mailPricingCacheItem{
											snapshot:  snap,
											expiresAt: current.Add(mailPricingCacheL1TTL),
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
			dbSnap, repoErr := s.repo.GetActiveMailPricingSnapshot(ctx, entity.ChargeKindMailAcceptedRecipient, now)
			if repoErr != nil {
				return nil, repoErr
			}

			// Kiểm tra cấu trúc bản ghi DB có đầy đủ thông tin định danh và mô hình định giá bắt buộc
			if dbSnap == nil || dbSnap.PricingScheduleID == uuid.Nil || dbSnap.VersionID == uuid.Nil ||
				dbSnap.Code == "" || dbSnap.ModuleCode != "mail" || dbSnap.Currency == "" ||
				dbSnap.VersionNumber < 1 || dbSnap.PricingModel != entity.PricingModelProgressiveUnit ||
				dbSnap.ChargeKindCode != entity.ChargeKindMailAcceptedRecipient {
				return nil, fmt.Errorf("Mail pricing snapshot is incomplete")
			}

			// Mail bắt buộc phải tính theo đơn vị người nhận: RECIPIENT
			if dbSnap.RawInputUnit != "RECIPIENT" {
				return nil, fmt.Errorf("Mail pricing snapshot unit mismatch")
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
			write(dbSnap.EffectiveFrom.UTC().Format(mailPricingChecksumTimeLayout))
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
				return nil, fmt.Errorf("Mail pricing snapshot checksum mismatch")
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
					brackets := make([]*mailpricingv1.MailPricingScalarBracketV1, len(dbSnap.Brackets))
					for index, bracket := range dbSnap.Brackets {
						entry := &mailpricingv1.MailPricingScalarBracketV1{
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
					entry := &mailpricingv1.MailPricingSnapshotCacheEntryV1{
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
						_ = s.redisClient.Set(ctx, cacheKey, payload, mailPricingCacheL2TTL).Err()
					}
				}
			}

			// ================================================================
			// 2.5. LƯU BẢNG GIÁ VÀO L1 RAM CACHE CỦA TIẾN TRÌNH HIỆN TẠI
			// ================================================================
			// Đảm bảo không bị ghi đè dữ liệu cũ nếu cacheGeneration đã bị thay đổi bởi worker invalidation.
			s.cacheMu.Lock()
			if s.cacheGeneration == generation {
				s.cache[lookupKey] = mailPricingCacheItem{
					snapshot:  dbSnap,
					expiresAt: time.Now().UTC().Add(mailPricingCacheL1TTL),
				}
			}
			s.cacheMu.Unlock()

			return dbSnap, nil
		})
		if err != nil {
			return nil, err
		}
		snapVal, ok := value.(*entity.MailPricingSnapshot)
		if !ok || snapVal == nil {
			return nil, fmt.Errorf("Mail pricing cache returned unexpected value %T", value)
		}
		snapshot = snapVal
	}

	// ------------------------------------------------------------------------
	// BƯỚC 3: LẤY HỆ SỐ ĐIỀU CHỈNH GIÁ RIÊNG THEO ZONE (ZONE MULTIPLIER)
	// ------------------------------------------------------------------------
	// Tra cứu hệ số multiplier đang active của Zone (Mặc định 1/1 nếu không có cấu hình riêng).
	adjustment, err := s.repo.GetActiveMailZonePriceAdjustment(ctx, zoneID, now)
	if err != nil {
		return nil, err
	}

	// ------------------------------------------------------------------------
	// BƯỚC 4: KIỂM TRA TÍNH TOÀN VẸN CHECKSUM SHA-256 CỦA HỆ SỐ ZONE
	// ------------------------------------------------------------------------
	numerator, denominator := int64(1), int64(1)
	if adjustment != nil {
		hash := sha256.New()
		for _, value := range []string{
			adjustment.ZoneID.String(),
			fmt.Sprintf("%d", adjustment.VersionNumber),
			adjustment.EffectiveFrom.UTC().Format(mailPricingChecksumTimeLayout),
			fmt.Sprintf("%d", adjustment.MultiplierNumerator),
			fmt.Sprintf("%d", adjustment.MultiplierDenominator),
		} {
			var length [8]byte
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
		if fmt.Sprintf("%x", hash.Sum(nil)) != adjustment.Checksum {
			return nil, fmt.Errorf("Mail Zone price adjustment checksum mismatch")
		}
		numerator, denominator = adjustment.MultiplierNumerator, adjustment.MultiplierDenominator
	}

	// ------------------------------------------------------------------------
	// BƯỚC 5: TÍNH TOÁN TIỀN CƯỚC MAIL BẰNG SỐ HỌC PHÂN SỐ BIG.RAT
	// ------------------------------------------------------------------------
	// Áp dụng công thức biểu phí lũy tiến từng phần (Progressive Brackets Calculation):
	// - Duyệt qua từng bậc thang giá từ thấp đến cao.
	// - Phần sản lượng rơi vào bậc nào thì nhân với đơn giá của bậc đó.
	// - Nhân thêm hệ số khu vực (Zone Multiplier) và làm tròn lên (Ceil) để bảo đảm doanh thu tối thiểu.
	if numerator < 0 || denominator <= 0 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	qty := uint64(recipientQuantity)
	total := new(big.Rat)
	for _, bracket := range snapshot.Brackets {
		if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 {
			return nil, billingTaxonomy.ErrInvalidPricingBrackets
		}
		start := uint64(bracket.RangeStartQuantity)
		if qty <= start {
			break
		}
		upper := qty
		if bracket.RangeEndQuantity != nil {
			if *bracket.RangeEndQuantity <= bracket.RangeStartQuantity {
				return nil, billingTaxonomy.ErrInvalidPricingBrackets
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
		return nil, fmt.Errorf("Mail pricing charge exceeds BIGINT")
	}
	estimate := ceil.Int64()

	// ------------------------------------------------------------------------
	// BƯỚC 6: ĐÓNG GÓI KẾT QUẢ GỬI VỀ CLIENT (RESULT PROJECTION)
	// ------------------------------------------------------------------------
	var adjustmentID *uuid.UUID
	var adjustmentVersion *int
	var adjustmentChecksum *string
	if adjustment != nil {
		adjustmentID = &adjustment.ID
		adjustmentVersion = &adjustment.VersionNumber
		adjustmentChecksum = &adjustment.Checksum
	}

	return &entity.MailEstimate{
		RecipientQuantity:         recipientQuantity,
		EstimateMicroUnits:        estimate,
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
		RateAdjustmentNumerator:   numerator,
		RateAdjustmentDenominator: denominator,
		EstimatedAt:               now,
	}, nil
}

// ============================================================================
// 2. WORKFLOW: BAN HÀNH BẢNG GIÁ GỐC MAIL (PUBLISH BASE PRICE VERSION)
// ============================================================================

// GetMailBasePricePublishTarget lấy thông tin cấu hình bảng giá gốc hiện tại trước khi ban hành bản mới.
func (s *mailPricingService) GetMailBasePricePublishTarget(ctx context.Context, code string) (*entity.MailBasePricePublishTarget, error) {
	return s.repo.GetMailBasePricePublishTarget(ctx, code)
}

// CreateMailBasePriceVersion tạo và công bố một phiên bản bảng giá gốc mới cho Mail Service.
// Các bước thực hiện:
// 1. Kiểm tra tính hợp lệ của thông tin đầu vào và thời gian hiệu lực.
// 2. Xác thực bảng giá mục tiêu (Schedule Target).
// 3. Sắp xếp và kiểm tra tính liên tục của các bậc thang giá [0, vô cực).
// 4. Tạo chữ ký bảo mật SHA-256 Checksum niêm phong toàn bộ dữ liệu bảng giá.
// 5. Lưu phiên bản mới vào cơ sở dữ liệu PostgreSQL.
// 6. Phát sự kiện Protobuf lên Redis Pub/Sub để các worker khác xóa cache đồng bộ.
func (s *mailPricingService) CreateMailBasePriceVersion(
	ctx context.Context,
	create entity.MailBasePricePublishCommand,
	brackets []entity.MailBasePriceBracketCommand,
) (*entity.MailBasePricePublished, error) {
	create.ScheduleCode = strings.TrimSpace(create.ScheduleCode)
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	create.EffectiveFrom = create.EffectiveFrom.UTC().Truncate(time.Microsecond)

	if create.ScheduleCode == "" || create.ChangeReason == "" || len(create.ChangeReason) > 2000 ||
		create.ExpectedLatestVersion < 0 || create.CreatedBy == uuid.Nil || create.EffectiveFrom.IsZero() ||
		len(brackets) == 0 || len(brackets) > 1000 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	// Bảng giá không được có mốc hiệu lực trong quá khứ quá 1 phút
	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, billingTaxonomy.ErrPricingScheduleEffectiveConflict
	}

	// Lấy thông tin bảng giá mục tiêu để kiểm tra tính tương thích
	target, err := s.repo.GetMailBasePricePublishTarget(ctx, create.ScheduleCode)
	if err != nil {
		return nil, err
	}

	if target.ChargeKindCode != entity.ChargeKindMailAcceptedRecipient || target.PricingModel != entity.PricingModelProgressiveUnit {
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
	write(create.EffectiveFrom.Format(mailPricingChecksumTimeLayout))
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
	published, err := s.repo.CreateMailBasePriceVersion(ctx, create, brackets)
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
			_ = s.redisClient.Publish(ctx, mailPricingCacheChannel, payload).Err()
		}
	}
	return published, err
}

// ============================================================================
// 3. WORKFLOW: ĐIỀU CHỈNH GIÁ THEO ZONE (ZONE ADJUSTMENT)
// ============================================================================

// CreateMailZonePriceAdjustment thiết lập và niêm phong hệ số giá mới cho một Zone cụ thể.
func (s *mailPricingService) CreateMailZonePriceAdjustment(
	ctx context.Context,
	create entity.MailZoneAdjustmentPublishCommand,
) (*entity.MailZoneAdjustmentPublished, error) {
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	create.EffectiveFrom = create.EffectiveFrom.UTC().Truncate(time.Microsecond)

	if create.ZoneID == uuid.Nil || create.CreatedBy == uuid.Nil || create.ExpectedLatestVersion < 0 ||
		create.EffectiveFrom.IsZero() || create.ChangeReason == "" || len(create.ChangeReason) > 2000 ||
		create.MultiplierNumerator < 0 || create.MultiplierDenominator <= 0 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}

	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, billingTaxonomy.ErrMailZoneAdjustmentConflict
	}

	// Tạo mã băm SHA-256 Checksum niêm phong hệ số điều chỉnh Zone
	hash := sha256.New()
	for _, value := range []string{
		create.ZoneID.String(),
		fmt.Sprintf("%d", create.ExpectedLatestVersion+1),
		create.EffectiveFrom.Format(mailPricingChecksumTimeLayout),
		fmt.Sprintf("%d", create.MultiplierNumerator),
		fmt.Sprintf("%d", create.MultiplierDenominator),
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	create.Checksum = fmt.Sprintf("%x", hash.Sum(nil))

	return s.repo.CreateMailZonePriceAdjustment(ctx, create)
}

// ListMailZonePriceAdjustments truy vấn danh sách lịch sử các lần điều chỉnh giá của một Zone.
func (s *mailPricingService) ListMailZonePriceAdjustments(
	ctx context.Context,
	query entity.MailZoneAdjustmentListQuery,
) (*entity.MailZoneAdjustmentListResult, error) {
	items, hasMore, err := s.repo.ListMailZonePriceAdjustments(ctx, query)
	if err != nil {
		return nil, err
	}
	return &entity.MailZoneAdjustmentListResult{
		ZoneID:     query.ZoneID,
		Items:      items,
		HasMore:    hasMore,
		ObservedAt: time.Now().UTC(),
	}, nil
}

// ============================================================================
// 4. WORKFLOW: INVALIDATION WORKER
// ============================================================================

// RunPricingSnapshotRefresh keeps the Cost-owned L2 contract warm for
// Controlplane's direct pricing gate. It emits no readiness stream or JSON.
func (s *mailPricingService) RunPricingSnapshotRefresh(ctx context.Context) {
	for {
		_, _ = s.EstimateMail(ctx, 1, uuid.New())
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

// RunPricingCacheInvalidation chạy worker ngầm đăng ký lắng nghe sự kiện bảng giá mới từ Redis Pub/Sub.
// Khi nhận được sự kiện, worker sẽ thực hiện xóa Cache L1 RAM và xóa Cache L2 Redis một cách chính xác (O(1)).
func (s *mailPricingService) RunPricingCacheInvalidation(ctx context.Context) {
	for {
		pubsub := s.redisClient.Subscribe(ctx, mailPricingCacheChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			_ = pubsub.Close()
			if ctx.Err() != nil {
				return
			}
			logger.SysWarn("billing.mail.pricing.cache.subscribe", err.Error())
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
				logger.SysWarn("billing.mail.pricing.cache.event", "invalid pricing cache event")
				continue
			}
			// Chỉ xử lý nếu sự kiện đúng là loại cước Mail
			if entity.ChargeKindCode(event.ChargeKindCode) != entity.ChargeKindMailAcceptedRecipient ||
				event.VersionNumber < 1 || len(event.Checksum) != 64 {
				continue
			}

			// 1. Xóa L1 RAM Cache của tiến trình hiện tại
			s.cacheMu.Lock()
			s.cacheGeneration++
			delete(s.cache, string(entity.ChargeKindMailAcceptedRecipient))
			s.cacheMu.Unlock()

			// 2. Xóa L2 Redis Cache chính xác key O(1)
			cacheKey := fmt.Sprintf("%s:%s", mailPricingCacheKeyPrefix, entity.ChargeKindMailAcceptedRecipient)
			if err := s.redisClient.Del(ctx, cacheKey).Err(); err != nil && ctx.Err() == nil {
				logger.SysWarn("billing.mail.pricing.cache.invalidate", err.Error())
			}
		}
		_ = pubsub.Close()
	}
}

func (s *mailPricingService) NotifyPricingOutbox() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *mailPricingService) RunPricingOutboxRelay(ctx context.Context) {
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
			if err := s.repo.RefreshMailPricingStatuses(ctx); err != nil && ctx.Err() == nil {
				logger.SysError("billing.mail.pricing.outbox.status", err.Error())
			}
			reconcile.Reset(30*time.Second + time.Duration(rand.IntN(10))*time.Second)
		}
		for ctx.Err() == nil {
			claimToken := uuid.New()
			rows, err := s.repo.ClaimMailPricingOutbox(ctx, claimToken, time.Now().UTC().Add(30*time.Second), 100)
			if err != nil {
				logger.SysError("billing.mail.pricing.outbox.claim", err.Error())
				break
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				event := &pricingv1.PricingScheduleVersionPublished{EventId: row.ID.String(), PricingScheduleId: row.PricingScheduleID.String(), PricingScheduleVersionId: row.VersionID.String(), VersionNumber: row.VersionNumber, ChargeKindCode: string(row.ChargeKindCode), EffectiveFromUnixMs: row.EffectiveFrom.UnixMilli(), Checksum: row.Checksum, OccurredAtUnixMs: row.OccurredAt.UnixMilli()}
				payload, publishErr := proto.Marshal(event)
				if publishErr == nil && s.redisClient != nil {
					publishErr = s.redisClient.Publish(ctx, mailPricingEngineChannel, payload).Err()
				}
				if publishErr == nil && s.redisClient != nil {
					publishErr = s.redisClient.Publish(ctx, mailPricingCacheChannel, payload).Err()
				}
				if s.redisClient == nil && publishErr == nil {
					publishErr = fmt.Errorf("Mail pricing outbox Redis is unavailable")
				}
				if publishErr != nil {
					backoffSeconds := 1 << min(row.RetryCount, 6)
					availableAt := time.Now().UTC().Add(time.Duration(backoffSeconds+rand.IntN(backoffSeconds+1)) * time.Second)
					if err := s.repo.RetryMailPricingOutbox(ctx, row.ID, row.ClaimToken, publishErr.Error(), availableAt); err != nil && ctx.Err() == nil {
						logger.SysError("billing.mail.pricing.outbox.retry", err.Error())
					}
					continue
				}
				if err := s.repo.MarkMailPricingOutboxPublished(ctx, row.ID, row.ClaimToken); err != nil && ctx.Err() == nil {
					logger.SysError("billing.mail.pricing.outbox.publish", err.Error())
				}
			}
		}
	}
}
