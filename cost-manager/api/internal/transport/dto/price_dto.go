package dto

import "time"

// SavePriceRequest đại diện cho DTO đầu vào cấu hình đơn giá
type SavePriceRequest struct {
	ID            string     `json:"id"`
	ServiceType   string     `json:"service_type" binding:"required"`
	MetricType    string     `json:"metric_type" binding:"required"`
	ZoneCode      string     `json:"zone_code" binding:"required"`
	Unit          string     `json:"unit" binding:"required"`
	UnitPrice     float64    `json:"unit_price" binding:"required,gte=0"`
	Currency      string     `json:"currency" binding:"required"`
	Tier          string     `json:"tier" binding:"required"`
	FreeQuota     float64    `json:"free_quota"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`
}
