package dto

import (
	"time"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

// PriceResponse là DTO trả về cho client
type PriceResponse struct {
	ID            uuid.UUID  `json:"id"`
	ServiceType   string     `json:"service_type"`
	ZoneCode      string     `json:"zone_code"`
	UnitPrice     float64    `json:"unit_price"`
	Currency      string     `json:"currency"`
	Tier          string     `json:"tier"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ZoneResponse là DTO trả về cho client
type ZoneResponse struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ToPriceResponse map entity → DTO
func ToPriceResponse(p entity.Price) PriceResponse {
	return PriceResponse{
		ID:            p.ID,
		ServiceType:   p.ServiceType,
		ZoneCode:      p.ZoneCode,
		UnitPrice:     p.UnitPrice,
		Currency:      p.Currency,
		Tier:          p.Tier,
		EffectiveFrom: p.EffectiveFrom,
		EffectiveTo:   p.EffectiveTo,
		CreatedAt:     p.CreatedAt,
	}
}

// ToPriceListResponse map slice entity → slice DTO
func ToPriceListResponse(prices []entity.Price) []PriceResponse {
	out := make([]PriceResponse, len(prices))
	for i, p := range prices {
		out[i] = ToPriceResponse(p)
	}
	return out
}

// ToZoneResponse map entity → DTO
func ToZoneResponse(z entity.Zone) ZoneResponse {
	return ZoneResponse{
		ID:     z.ID,
		Code:   z.Code,
		Name:   z.Name,
		Status: z.Status,
	}
}

// ToZoneListResponse map slice entity → slice DTO
func ToZoneListResponse(zones []entity.Zone) []ZoneResponse {
	out := make([]ZoneResponse, len(zones))
	for i, z := range zones {
		out[i] = ToZoneResponse(z)
	}
	return out
}
