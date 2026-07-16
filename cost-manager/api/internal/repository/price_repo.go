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

func (r *PriceRepositoryImpl) ListPrices(ctx context.Context) ([]entity.Price, error) {
	query := `
		SELECT id, service_type, zone_code, unit_price, currency, tier, effective_from, effective_to, created_at
		FROM billing.prices
		ORDER BY created_at DESC
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDatabaseFailed, err, "query_prices_failed")
	}
	defer rows.Close()

	var list []entity.Price
	for rows.Next() {
		var p entity.Price
		err := rows.Scan(&p.ID, &p.ServiceType, &p.ZoneCode, &p.UnitPrice, &p.Currency, &p.Tier, &p.EffectiveFrom, &p.EffectiveTo, &p.CreatedAt)
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDatabaseFailed, err, "scan_price_failed")
		}
		list = append(list, p)
	}

	return list, nil
}

func (r *PriceRepositoryImpl) CreateOrUpdatePrice(ctx context.Context, p *entity.Price) error {
	if p.ID == uuid.Nil {
		var err error
		p.ID, err = uuid.NewV7()
		if err != nil {
			return apperr.Wrap(apperr.ErrInternalServer, err, "uuid_generation_failed")
		}
	}

	query := `
		INSERT INTO billing.prices (id, service_type, zone_code, unit_price, currency, tier, effective_from, effective_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (service_type, zone_code, tier) 
		DO UPDATE SET unit_price = EXCLUDED.unit_price, effective_from = EXCLUDED.effective_from, effective_to = EXCLUDED.effective_to
		RETURNING id, created_at
	`
	err := r.pool.QueryRow(ctx, query, p.ID, p.ServiceType, p.ZoneCode, p.UnitPrice, p.Currency, p.Tier, p.EffectiveFrom, p.EffectiveTo).Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		return apperr.Wrap(apperr.ErrDatabaseFailed, err, "upsert_price_failed")
	}

	return nil
}
