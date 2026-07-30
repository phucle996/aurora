package repository

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	"controlplane/internal/managedservice/taxonomy"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type categoryRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewCategoryRepository(db *pgxpool.Pool, schema string) managedrepo.CategoryRepository {
	return &categoryRepository{db: db, schema: schema}
}

func (r *categoryRepository) CreateCategory(ctx context.Context, in *entity.CreateCategory) (*entity.CategoryView, error) {
	query := fmt.Sprintf(`WITH inserted AS (
		INSERT INTO %s.service_categories (id,code,name,description,name_i18n,description_i18n,icon_key,created_by,updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
		RETURNING id,code,name,description,name_i18n,description_i18n,icon_key,state::text,row_version,created_at,updated_at
	), audited AS (
		INSERT INTO %s.catalog_audit_events (id,actor_subject,action,record_kind,record_id,record_version,outcome,after_hash)
		SELECT $9,$8,'category.create','category',id,row_version,'succeeded',$10 FROM inserted
	) SELECT id,code,name,description,name_i18n,description_i18n,icon_key,state,row_version,created_at,updated_at FROM inserted`, r.schema, r.schema)
	out := &entity.CategoryView{}
	err := r.db.QueryRow(ctx, query, in.ID, in.Code, in.Name, in.Description, in.NameI18n, in.DescriptionI18n,
		in.IconKey, in.Actor, in.AuditID, in.AfterHash).Scan(&out.ID, &out.Code, &out.Name, &out.Description,
		&out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, taxonomy.ErrCatalogCodeConflict
		}
		return nil, err
	}
	return out, nil
}

func (r *categoryRepository) ListCategories(ctx context.Context, in *entity.ListCategories) ([]entity.CategoryView, error) {
	query := fmt.Sprintf(`SELECT id,code,name,description,name_i18n,description_i18n,icon_key,state::text,row_version,created_at,updated_at
		FROM %s.service_categories ORDER BY created_at DESC,id DESC LIMIT $1`, r.schema)
	rows, err := r.db.Query(ctx, query, in.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]entity.CategoryView, 0)
	for rows.Next() {
		var item entity.CategoryView
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Description, &item.NameI18n, &item.DescriptionI18n,
			&item.IconKey, &item.State, &item.RowVersion, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *categoryRepository) GetCategory(ctx context.Context, in *entity.GetCategory) (*entity.CategoryView, error) {
	query := fmt.Sprintf(`SELECT id,code,name,description,name_i18n,description_i18n,icon_key,state::text,row_version,created_at,updated_at
		FROM %s.service_categories WHERE id=$1`, r.schema)
	out := &entity.CategoryView{}
	err := r.db.QueryRow(ctx, query, in.CategoryID).Scan(&out.ID, &out.Code, &out.Name, &out.Description, &out.NameI18n,
		&out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *categoryRepository) UpdateCategory(ctx context.Context, in *entity.UpdateCategory) (*entity.CategoryView, error) {
	query := fmt.Sprintf(`WITH target AS (SELECT * FROM %s.service_categories WHERE id=$1 FOR UPDATE), updated AS (
		UPDATE %s.service_categories category SET name=$2,description=$3,name_i18n=$4,description_i18n=$5,icon_key=$6,
			updated_by=$7,row_version=category.row_version+1,updated_at=now()
		WHERE category.id=$1 AND category.row_version=$8 AND category.state='active' RETURNING category.*
	), audited AS (
		INSERT INTO %s.catalog_audit_events (id,actor_subject,action,record_kind,record_id,record_version,outcome,after_hash)
		SELECT $9,$7,'category.update','category',id,row_version,'succeeded',$10 FROM updated
	) SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),
		COALESCE((SELECT id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT code FROM updated),''),
		COALESCE((SELECT name FROM updated),''),COALESCE((SELECT description FROM updated),''),COALESCE((SELECT name_i18n FROM updated),'{}'::jsonb),
		COALESCE((SELECT description_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT icon_key FROM updated),''),COALESCE((SELECT state::text FROM updated),''),
		COALESCE((SELECT row_version FROM updated),0),COALESCE((SELECT created_at FROM updated),now()),COALESCE((SELECT updated_at FROM updated),now())`, r.schema, r.schema, r.schema)
	out := &entity.CategoryView{}
	var exists bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.CategoryID, in.Name, in.Description, in.NameI18n, in.DescriptionI18n, in.IconKey, in.Actor,
		in.ExpectedVersion, in.AuditID, in.AfterHash).Scan(&exists, &state, &current, &out.ID, &out.Code, &out.Name, &out.Description,
		&out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if state != "active" {
		return nil, taxonomy.ErrCatalogInvalidTransition
	}
	if current != in.ExpectedVersion {
		return nil, taxonomy.ErrCatalogConcurrentChange
	}
	return out, nil
}

func (r *categoryRepository) RetireCategory(ctx context.Context, in *entity.RetireCategory) (*entity.CategoryView, error) {
	query := fmt.Sprintf(`WITH target AS (SELECT * FROM %s.service_categories WHERE id=$1 FOR UPDATE), updated AS (
		UPDATE %s.service_categories category SET state='retired',retired_by=$2,updated_by=$2,row_version=category.row_version+1,updated_at=now()
		WHERE category.id=$1 AND category.row_version=$3 AND category.state='active' RETURNING category.*
	), audited AS (
		INSERT INTO %s.catalog_audit_events (id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome)
		SELECT $4,$2,$5,'category.retire','category',id,row_version,'succeeded' FROM updated
	) SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),
		COALESCE((SELECT id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT code FROM updated),''),
		COALESCE((SELECT name FROM updated),''),COALESCE((SELECT description FROM updated),''),COALESCE((SELECT name_i18n FROM updated),'{}'::jsonb),
		COALESCE((SELECT description_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT icon_key FROM updated),''),COALESCE((SELECT state::text FROM updated),''),
		COALESCE((SELECT row_version FROM updated),0),COALESCE((SELECT created_at FROM updated),now()),COALESCE((SELECT updated_at FROM updated),now())`, r.schema, r.schema, r.schema)
	out := &entity.CategoryView{}
	var exists bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.CategoryID, in.Actor, in.ExpectedVersion, in.AuditID, in.ProofID).Scan(&exists, &state, &current,
		&out.ID, &out.Code, &out.Name, &out.Description, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if state != "active" {
		return nil, taxonomy.ErrCatalogInvalidTransition
	}
	if current != in.ExpectedVersion {
		return nil, taxonomy.ErrCatalogConcurrentChange
	}
	return out, nil
}
