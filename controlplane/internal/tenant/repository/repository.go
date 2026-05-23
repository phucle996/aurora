package tenantRepoImpl

import (
	"context"
	"fmt"
	"strings"

	"controlplane/internal/config"
	tenantEntity "controlplane/internal/tenant/domain/entity"
	tenantRepo "controlplane/internal/tenant/domain/repo"
	tenantErrorx "controlplane/internal/tenant/errorx"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db     *pgxpool.Pool
	schema string
}

func NewRepository(cfg *config.Config, db *pgxpool.Pool) tenantRepo.Repository {
	return &Repository{db: db, schema: cfg.SchemaSQL.IAM}
}

func (r *Repository) CreateTenantBootstrapTx(ctx context.Context, input tenantEntity.CreateTenantInput) (*tenantEntity.CreateTenantResult, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil { return nil, fmt.Errorf("tenant repo: begin tx: %w", err) }
	defer tx.Rollback(ctx)

	var tenantID string
	if err := tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.tenants(name,status) VALUES($1,'active') RETURNING id`, r.schema), input.Name).Scan(&tenantID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") { return nil, tenantErrorx.ErrConflict }
		return nil, fmt.Errorf("tenant repo: insert tenant: %w", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.tenant_domains(tenant_id,domain,is_primary) VALUES($1,$2,true)`, r.schema), tenantID, strings.ToLower(strings.TrimSpace(input.Domain))); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") { return nil, tenantErrorx.ErrConflict }
		return nil, fmt.Errorf("tenant repo: insert domain: %w", err)
	}
	var membershipID string
	if err := tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.tenant_memberships(tenant_id,user_id,status) VALUES($1,$2,'active') RETURNING id`, r.schema), tenantID, input.CreatorID).Scan(&membershipID); err != nil {
		return nil, fmt.Errorf("tenant repo: insert membership: %w", err)
	}
	var ownerRoleID string
	if err := tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.tenant_roles(tenant_id,code,name) VALUES($1,'owner','Owner') RETURNING id`, r.schema), tenantID).Scan(&ownerRoleID); err != nil {
		return nil, fmt.Errorf("tenant repo: insert owner role: %w", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.tenant_membership_roles(membership_id,role_id) VALUES($1,$2)`, r.schema), membershipID, ownerRoleID); err != nil {
		return nil, fmt.Errorf("tenant repo: link membership role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil { return nil, fmt.Errorf("tenant repo: commit: %w", err) }
	return &tenantEntity.CreateTenantResult{TenantID: tenantID, Domain: strings.ToLower(strings.TrimSpace(input.Domain))}, nil
}

func (r *Repository) ResolveTenantByDomain(ctx context.Context, domain string) (*tenantEntity.TenantDomain, error) {
	item := tenantEntity.TenantDomain{}
	if err := r.db.QueryRow(ctx, fmt.Sprintf(`SELECT id,tenant_id,domain,is_primary,created_at FROM %s.tenant_domains WHERE domain=$1 LIMIT 1`, r.schema), strings.ToLower(strings.TrimSpace(domain))).Scan(&item.ID,&item.TenantID,&item.Domain,&item.IsPrimary,&item.CreatedAt); err != nil {
		if err == pgx.ErrNoRows { return nil, tenantErrorx.ErrNotFound }
		return nil, fmt.Errorf("tenant repo: resolve domain: %w", err)
	}
	return &item, nil
}

func (r *Repository) GetMembershipAndRoles(ctx context.Context, tenantID string, userID string) (*tenantEntity.LoginTenantContext, error) {
	query := fmt.Sprintf(`
SELECT tr.code
FROM %s.tenant_memberships tm
JOIN %s.tenant_membership_roles tmr ON tmr.membership_id = tm.id
JOIN %s.tenant_roles tr ON tr.id = tmr.role_id
WHERE tm.tenant_id = $1 AND tm.user_id = $2 AND tm.status='active'`, r.schema, r.schema, r.schema)
	rows, err := r.db.Query(ctx, query, tenantID, userID)
	if err != nil { return nil, fmt.Errorf("tenant repo: membership roles: %w", err) }
	defer rows.Close()
	roles := make([]string,0,4)
	for rows.Next() { var c string; if scanErr := rows.Scan(&c); scanErr != nil { return nil, fmt.Errorf("tenant repo: scan role: %w", scanErr) }; roles = append(roles,c) }
	if len(roles) == 0 { return nil, tenantErrorx.ErrForbidden }
	return &tenantEntity.LoginTenantContext{TenantID: tenantID, UserID: userID, Roles: roles}, nil
}
