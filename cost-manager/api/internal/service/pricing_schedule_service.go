package service

import (
	"context"
	"strings"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
)

// pricingScheduleService quản lý danh mục Global: catalog read/detail và metadata.
type pricingScheduleService struct {
	repo billingRepoInterface.PricingScheduleRepository
}

func NewPricingScheduleService(repo billingRepoInterface.PricingScheduleRepository) billingSvcInterface.PricingScheduleService {
	return &pricingScheduleService{repo: repo}
}

// ============================================================================
// 1. WORKFLOW: TRA CỨU DANH MỤC & CHI TIẾT BẢNG GIÁ (CATALOG READ)
// ============================================================================

// GetPricingSchedules lấy danh sách các bảng giá cùng tổng số lượng bản ghi (phục vụ phân trang trên UI Console).
func (s *pricingScheduleService) GetPricingSchedules(
	ctx context.Context,
	page, limit int,
	chargeKind entity.ChargeKindCode,
	search string,
) ([]*entity.PricingScheduleListItem, int64, error) {
	return s.repo.ListPricingSchedules(ctx, page, limit, chargeKind, search)
}

// GetPricingScheduleDetail truy vấn thông tin chi tiết bảng giá theo mã định danh code (kèm danh sách các bậc thang giá).
func (s *pricingScheduleService) GetPricingScheduleDetail(
	ctx context.Context,
	code string,
) (*entity.PricingScheduleDetail, []entity.PricingScheduleDetailBracket, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.GetPricingScheduleDetail(ctx, code)
}

// UpdatePricingScheduleMetadata cập nhật tên hiển thị (display_name) của bảng giá với cơ chế Optimistic Concurrency Control (OCC).
func (s *pricingScheduleService) UpdatePricingScheduleMetadata(
	ctx context.Context,
	update entity.PricingScheduleMetadataCommand,
) (*entity.PricingScheduleMetadataUpdated, error) {
	update.ScheduleCode = strings.TrimSpace(update.ScheduleCode)
	update.DisplayName = strings.TrimSpace(update.DisplayName)
	if update.ScheduleCode == "" || update.DisplayName == "" || len(update.DisplayName) > 128 || update.MetadataVersion < 1 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.UpdatePricingScheduleMetadata(ctx, update)
}
