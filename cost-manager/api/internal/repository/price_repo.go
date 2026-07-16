package repository

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/domain/repo"
	"cost-manager/api/pkg/apperr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PriceRepositoryImpl struct {
	pool *pgxpool.Pool
}

func NewPriceRepository(pool *pgxpool.Pool) repo.PriceRepository {
	return &PriceRepositoryImpl{pool: pool}
}

// ListPrices trả về tất cả đơn giá, bao gồm metric_type, unit, free_quota
func (r *PriceRepositoryImpl) ListPrices(ctx context.Context) ([]entity.Price, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, service_type, metric_type, zone_code, unit, unit_price,
		       currency, tier, free_quota, effective_from, effective_to, created_at
		FROM billing.prices
		ORDER BY service_type, metric_type, zone_code
	`)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDatabaseFailed, err, "query_prices_failed")
	}
	defer rows.Close()

	var list []entity.Price
	for rows.Next() {
		var p entity.Price
		err := rows.Scan(
			&p.ID, &p.ServiceType, &p.MetricType, &p.ZoneCode, &p.Unit,
			&p.UnitPrice, &p.Currency, &p.Tier, &p.FreeQuota,
			&p.EffectiveFrom, &p.EffectiveTo, &p.CreatedAt,
		)
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDatabaseFailed, err, "scan_price_failed")
		}
		list = append(list, p)
	}
	return list, nil
}

// GetPriceByMetric lấy đơn giá theo (service_type, metric_type, zone_code, tier) — dùng khi tính overage
func (r *PriceRepositoryImpl) GetPriceByMetric(ctx context.Context, serviceType, metricType, zoneCode, tier string) (*entity.Price, error) {
	var p entity.Price
	err := r.pool.QueryRow(ctx, `
		SELECT id, service_type, metric_type, zone_code, unit, unit_price,
		       currency, tier, free_quota, effective_from, effective_to, created_at
		FROM billing.prices
		WHERE service_type = $1 AND metric_type = $2 AND zone_code = $3 AND tier = $4
		  AND effective_from <= NOW()
		  AND (effective_to IS NULL OR effective_to > NOW())
		LIMIT 1
	`, serviceType, metricType, zoneCode, tier).Scan(
		&p.ID, &p.ServiceType, &p.MetricType, &p.ZoneCode, &p.Unit,
		&p.UnitPrice, &p.Currency, &p.Tier, &p.FreeQuota,
		&p.EffectiveFrom, &p.EffectiveTo, &p.CreatedAt,
	)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrPriceNotFound, err, "price_not_found")
	}
	return &p, nil
}

// CreateOrUpdatePrice upsert đơn giá theo unique constraint (service_type, metric_type, zone_code, tier)
func (r *PriceRepositoryImpl) CreateOrUpdatePrice(ctx context.Context, p *entity.Price) error {
	if p.ID == uuid.Nil {
		var err error
		p.ID, err = uuid.NewV7()
		if err != nil {
			return apperr.Wrap(apperr.ErrInternalServer, err, "uuid_generation_failed")
		}
	}

	err := r.pool.QueryRow(ctx, `
		INSERT INTO billing.prices (id, service_type, metric_type, zone_code, unit, unit_price, currency, tier, free_quota, effective_from, effective_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (service_type, metric_type, zone_code, tier)
		DO UPDATE SET
		    unit_price     = EXCLUDED.unit_price,
		    free_quota     = EXCLUDED.free_quota,
		    effective_from = EXCLUDED.effective_from,
		    effective_to   = EXCLUDED.effective_to
		RETURNING id, created_at
	`, p.ID, p.ServiceType, p.MetricType, p.ZoneCode, p.Unit,
		p.UnitPrice, p.Currency, p.Tier, p.FreeQuota, p.EffectiveFrom, p.EffectiveTo,
	).Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		return apperr.Wrap(apperr.ErrDatabaseFailed, err, "upsert_price_failed")
	}
	return nil
}
