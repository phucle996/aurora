package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

// PricingScheduleRepository quản lý các truy vấn SQL cho danh mục bảng giá (Catalog)
// và metadata dùng chung. Mỗi module sở hữu workflow outbox của chính mình.
type PricingScheduleRepository interface {
	// Catalog & Metadata
	ListPricingSchedules(ctx context.Context, page, limit int, chargeKind entity.ChargeKindCode, search string) ([]*entity.PricingScheduleListItem, int64, error)
	GetPricingScheduleDetail(ctx context.Context, code string) (*entity.PricingScheduleDetail, []entity.PricingScheduleDetailBracket, error)
	UpdatePricingScheduleMetadata(ctx context.Context, update entity.PricingScheduleMetadataCommand) (*entity.PricingScheduleMetadataUpdated, error)
}
