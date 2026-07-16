package dto

import (
	"time"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

// PlanMetricResponse đại diện cho JSON quota của metric
type PlanMetricResponse struct {
	ID         uuid.UUID `json:"id"`
	PlanID     uuid.UUID `json:"plan_id"`
	MetricType string    `json:"metric_type"`
	Quota      float64   `json:"quota"`
	Unit       string    `json:"unit"`
}

// PlanResponse đại diện cho JSON của gói cước
type PlanResponse struct {
	ID           uuid.UUID            `json:"id"`
	Name         string               `json:"name"`
	Code         string               `json:"code"`
	ServiceType  string               `json:"service_type"`
	ZoneCode     string               `json:"zone_code"`
	MonthlyPrice float64              `json:"monthly_price"`
	Currency     string               `json:"currency"`
	Status       string               `json:"status"`
	Description  string               `json:"description"`
	Metrics      []PlanMetricResponse `json:"metrics,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
}

// SubscriptionResponse đại diện cho JSON subscription
type SubscriptionResponse struct {
	ID          uuid.UUID     `json:"id"`
	OwnerID     uuid.UUID     `json:"owner_id"`
	OwnerType   string        `json:"owner_type"`
	PlanID      uuid.UUID     `json:"plan_id"`
	Plan        *PlanResponse `json:"plan,omitempty"`
	Status      string        `json:"status"`
	StartedAt   time.Time     `json:"started_at"`
	ExpiresAt   *time.Time    `json:"expires_at,omitempty"`
	RenewedAt   *time.Time    `json:"renewed_at,omitempty"`
	CancelledAt *time.Time    `json:"cancelled_at,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
}

// ToPlanMetricResponse chuyển đổi domain entity sang DTO
func ToPlanMetricResponse(m entity.PlanMetric) PlanMetricResponse {
	return PlanMetricResponse{
		ID:         m.ID,
		PlanID:     m.PlanID,
		MetricType: string(m.MetricType),
		Quota:      m.Quota,
		Unit:       string(m.Unit),
	}
}

// ToPlanResponse chuyển đổi domain entity sang DTO
func ToPlanResponse(p entity.Plan) PlanResponse {
	metrics := make([]PlanMetricResponse, len(p.Metrics))
	for i, m := range p.Metrics {
		metrics[i] = ToPlanMetricResponse(m)
	}
	return PlanResponse{
		ID:           p.ID,
		Name:         p.Name,
		Code:         p.Code,
		ServiceType:  string(p.ServiceType),
		ZoneCode:     p.ZoneCode,
		MonthlyPrice: p.MonthlyPrice,
		Currency:     p.Currency,
		Status:       string(p.Status),
		Description:  p.Description,
		Metrics:      metrics,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

// ToPlanListResponse chuyển đổi danh sách domain entity sang DTO
func ToPlanListResponse(plans []entity.Plan) []PlanResponse {
	res := make([]PlanResponse, len(plans))
	for i, p := range plans {
		res[i] = ToPlanResponse(p)
	}
	return res
}

// ToSubscriptionResponse chuyển đổi domain entity sang DTO
func ToSubscriptionResponse(s entity.Subscription) SubscriptionResponse {
	var planDTO *PlanResponse
	if s.Plan != nil {
		p := ToPlanResponse(*s.Plan)
		planDTO = &p
	}
	return SubscriptionResponse{
		ID:          s.ID,
		OwnerID:     s.OwnerID,
		OwnerType:   string(s.OwnerType),
		PlanID:      s.PlanID,
		Plan:        planDTO,
		Status:      string(s.Status),
		StartedAt:   s.StartedAt,
		ExpiresAt:   s.ExpiresAt,
		RenewedAt:   s.RenewedAt,
		CancelledAt: s.CancelledAt,
		CreatedAt:   s.CreatedAt,
	}
}
