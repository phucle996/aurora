package repository

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type authRepository struct {
	dbPool *pgxpool.Pool
}

// NewAuthRepository creates a new UserRepository instance
func NewAuthRepository(dbPool *pgxpool.Pool) billingRepoInterface.UserRepository {
	return &authRepository{
		dbPool: dbPool,
	}
}

// GetByEmployeeCode queries user details from the database by employee code
func (r *authRepository) GetByEmployeeCode(ctx context.Context, employeeCode string) (*entity.User, error) {
	var u entity.User
	err := r.dbPool.QueryRow(ctx,
		`SELECT id, employee_code, public_key, fullname, email, role_id, level, status, created_at, updated_at 
		 FROM billing.users 
		 WHERE employee_code = $1`,
		employeeCode,
	).Scan(
		&u.ID,
		&u.EmployeeCode,
		&u.PublicKey,
		&u.Fullname,
		&u.Email,
		&u.RoleID,
		&u.Level,
		&u.Status,
		&u.CreatedAt,
		&u.UpdatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, billingTaxonomy.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return &u, nil
}
