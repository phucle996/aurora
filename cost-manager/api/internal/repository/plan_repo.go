package repository

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/domain/repo"
	"cost-manager/api/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type planRepo struct {
	db *pgxpool.Pool
}

// NewPlanRepository khởi tạo PlanRepository với pgx pool
func NewPlanRepository(db *pgxpool.Pool) repo.PlanRepository {
	return &planRepo{db: db}
}

// ListPlans trả về tất cả plans ACTIVE, JOIN với plan_metrics
func (r *planRepo) ListPlans(ctx context.Context) ([]entity.Plan, error) {
	const op = "repo.plan.list_plans"

	// Lấy plans trước
	rows, err := r.db.Query(ctx, `
		SELECT id, name, code, service_type, zone_code, monthly_price, currency, status, description, created_at, updated_at
		FROM billing.plans
		WHERE status = 'ACTIVE'
		ORDER BY monthly_price ASC
	`)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: query plans: %w", op, err), "db_error")
	}
	defer rows.Close()

	var plans []entity.Plan
	var planIDs []uuid.UUID

	for rows.Next() {
		var p entity.Plan
		if err := rows.Scan(&p.ID, &p.Name, &p.Code, &p.ServiceType, &p.ZoneCode,
			&p.MonthlyPrice, &p.Currency, &p.Status, &p.Description,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: scan plan: %w", op, err), "db_error")
		}
		plans = append(plans, p)
		planIDs = append(planIDs, p.ID)
	}

	if len(plans) == 0 {
		return plans, nil
	}

	// Lấy metrics cho tất cả plans trong 1 query
	metrics, err := r.fetchMetricsByPlanIDs(ctx, op, planIDs)
	if err != nil {
		return nil, err
	}

	// Gán metrics vào plan tương ứng
	metricMap := make(map[uuid.UUID][]entity.PlanMetric)
	for _, m := range metrics {
		metricMap[m.PlanID] = append(metricMap[m.PlanID], m)
	}
	for i := range plans {
		plans[i].Metrics = metricMap[plans[i].ID]
	}

	return plans, nil
}

// GetPlanByID lấy Plan kèm Metrics theo ID
func (r *planRepo) GetPlanByID(ctx context.Context, id uuid.UUID) (*entity.Plan, error) {
	const op = "repo.plan.get_plan_by_id"

	var p entity.Plan
	err := r.db.QueryRow(ctx, `
		SELECT id, name, code, service_type, zone_code, monthly_price, currency, status, description, created_at, updated_at
		FROM billing.plans
		WHERE id = $1
	`, id).Scan(&p.ID, &p.Name, &p.Code, &p.ServiceType, &p.ZoneCode,
		&p.MonthlyPrice, &p.Currency, &p.Status, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrPriceNotFound, fmt.Errorf("%s: %w", op, err), "plan_not_found")
	}

	metrics, err := r.fetchMetricsByPlanIDs(ctx, op, []uuid.UUID{p.ID})
	if err != nil {
		return nil, err
	}
	p.Metrics = metrics
	return &p, nil
}

// CreatePlan tạo plan mới kèm plan_metrics trong 1 transaction — atomic
func (r *planRepo) CreatePlan(ctx context.Context, p *entity.Plan) error {
	const op = "repo.plan.create_plan"

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: begin tx: %w", op, err), "db_error")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Insert plan
	_, err = tx.Exec(ctx, `
		INSERT INTO billing.plans (id, name, code, service_type, zone_code, monthly_price, currency, status, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, p.ID, p.Name, p.Code, p.ServiceType, p.ZoneCode, p.MonthlyPrice, p.Currency, p.Status, p.Description)
	if err != nil {
		return apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: insert plan: %w", op, err), "db_error")
	}

	// Insert plan_metrics
	for _, m := range p.Metrics {
		if m.ID == uuid.Nil {
			m.ID = uuid.New()
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO billing.plan_metrics (id, plan_id, metric_type, quota, unit)
			VALUES ($1, $2, $3, $4, $5)
		`, m.ID, p.ID, m.MetricType, m.Quota, m.Unit)
		if err != nil {
			return apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: insert metric: %w", op, err), "db_error")
		}
	}

	return tx.Commit(ctx)
}

// UpdatePlanStatus cập nhật status của plan
func (r *planRepo) UpdatePlanStatus(ctx context.Context, id uuid.UUID, status string) error {
	const op = "repo.plan.update_plan_status"

	_, err := r.db.Exec(ctx, `
		UPDATE billing.plans SET status = $1, updated_at = NOW() WHERE id = $2
	`, status, id)
	if err != nil {
		return apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: %w", op, err), "db_error")
	}
	return nil
}

// fetchMetricsByPlanIDs là helper lấy plan_metrics theo danh sách plan IDs
func (r *planRepo) fetchMetricsByPlanIDs(ctx context.Context, op string, ids []uuid.UUID) ([]entity.PlanMetric, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, plan_id, metric_type, quota, unit
		FROM billing.plan_metrics
		WHERE plan_id = ANY($1)
		ORDER BY plan_id, metric_type
	`, ids)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: query metrics: %w", op, err), "db_error")
	}
	defer rows.Close()

	var metrics []entity.PlanMetric
	for rows.Next() {
		var m entity.PlanMetric
		if err := rows.Scan(&m.ID, &m.PlanID, &m.MetricType, &m.Quota, &m.Unit); err != nil {
			return nil, apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: scan metric: %w", op, err), "db_error")
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}
