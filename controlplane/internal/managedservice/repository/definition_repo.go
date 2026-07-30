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

type definitionRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewDefinitionRepository(db *pgxpool.Pool, schema string) managedrepo.DefinitionRepository {
	return &definitionRepository{db: db, schema: schema}
}

func (r *definitionRepository) CreateDefinition(ctx context.Context, in *entity.CreateDefinition) (*entity.DefinitionView, error) {
	query := fmt.Sprintf(`WITH parent AS (SELECT id,state FROM %s.service_categories WHERE id=$1 FOR KEY SHARE), inserted AS (
		INSERT INTO %s.service_definitions (id,category_id,code,name,description,name_i18n,description_i18n,icon_key,created_by,updated_by)
		SELECT $2,$1,$3,$4,$5,$6,$7,$8,$9,$9 FROM parent WHERE state='active' RETURNING *
	), audited AS (
		INSERT INTO %s.catalog_audit_events (id,actor_subject,action,record_kind,record_id,record_version,outcome,after_hash)
		SELECT $10,$9,'definition.create','definition',id,row_version,'succeeded',$11 FROM inserted
	) SELECT EXISTS(SELECT 1 FROM parent),COALESCE((SELECT state::text FROM parent),''),
		COALESCE((SELECT id FROM inserted),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT category_id FROM inserted),'00000000-0000-0000-0000-000000000000'::uuid),
		COALESCE((SELECT code FROM inserted),''),COALESCE((SELECT name FROM inserted),''),COALESCE((SELECT description FROM inserted),''),
		COALESCE((SELECT name_i18n FROM inserted),'{}'::jsonb),COALESCE((SELECT description_i18n FROM inserted),'{}'::jsonb),COALESCE((SELECT icon_key FROM inserted),''),
		COALESCE((SELECT state::text FROM inserted),''),COALESCE((SELECT row_version FROM inserted),0),COALESCE((SELECT created_at FROM inserted),now()),COALESCE((SELECT updated_at FROM inserted),now())`, r.schema, r.schema, r.schema)
	out := &entity.DefinitionView{}
	var parentExists bool
	var parentState string
	err := r.db.QueryRow(ctx, query, in.CategoryID, in.ID, in.Code, in.Name, in.Description, in.NameI18n, in.DescriptionI18n, in.IconKey, in.Actor, in.AuditID, in.AfterHash).
		Scan(&parentExists, &parentState, &out.ID, &out.CategoryID, &out.Code, &out.Name, &out.Description, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, taxonomy.ErrCatalogCodeConflict
		}
		return nil, err
	}
	if !parentExists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if parentState != "active" {
		return nil, taxonomy.ErrCatalogParentRetired
	}
	return out, nil
}

func (r *definitionRepository) ListDefinitions(ctx context.Context, in *entity.ListDefinitions) ([]entity.DefinitionView, error) {
	query := fmt.Sprintf(`SELECT id,category_id,code,name,description,name_i18n,description_i18n,icon_key,state::text,row_version,created_at,updated_at
		FROM %s.service_definitions WHERE ($1::uuid='00000000-0000-0000-0000-000000000000' OR category_id=$1) ORDER BY created_at DESC,id DESC LIMIT $2`, r.schema)
	rows, err := r.db.Query(ctx, query, in.CategoryID, in.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]entity.DefinitionView, 0)
	for rows.Next() {
		var item entity.DefinitionView
		if err := rows.Scan(&item.ID, &item.CategoryID, &item.Code, &item.Name, &item.Description, &item.NameI18n, &item.DescriptionI18n, &item.IconKey, &item.State, &item.RowVersion, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *definitionRepository) GetDefinition(ctx context.Context, in *entity.GetDefinition) (*entity.DefinitionView, error) {
	query := fmt.Sprintf(`SELECT id,category_id,code,name,description,name_i18n,description_i18n,icon_key,state::text,row_version,created_at,updated_at FROM %s.service_definitions WHERE id=$1`, r.schema)
	out := &entity.DefinitionView{}
	err := r.db.QueryRow(ctx, query, in.DefinitionID).Scan(&out.ID, &out.CategoryID, &out.Code, &out.Name, &out.Description, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *definitionRepository) UpdateDefinition(ctx context.Context, in *entity.UpdateDefinition) (*entity.DefinitionView, error) {
	query := fmt.Sprintf(`WITH target AS (SELECT * FROM %s.service_definitions WHERE id=$1 FOR UPDATE),updated AS(
		UPDATE %s.service_definitions definition SET name=$2,description=$3,name_i18n=$4,description_i18n=$5,icon_key=$6,updated_by=$7,row_version=definition.row_version+1,updated_at=now()
		WHERE definition.id=$1 AND definition.row_version=$8 AND definition.state='active' RETURNING definition.*),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,action,record_kind,record_id,record_version,outcome,after_hash) SELECT $9,$7,'definition.update','definition',id,row_version,'succeeded',$10 FROM updated)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),
		COALESCE((SELECT id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT category_id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT code FROM updated),''),COALESCE((SELECT name FROM updated),''),COALESCE((SELECT description FROM updated),''),COALESCE((SELECT name_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT description_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT icon_key FROM updated),''),COALESCE((SELECT state::text FROM updated),''),COALESCE((SELECT row_version FROM updated),0),COALESCE((SELECT created_at FROM updated),now()),COALESCE((SELECT updated_at FROM updated),now())`, r.schema, r.schema, r.schema)
	out := &entity.DefinitionView{}
	var exists bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.DefinitionID, in.Name, in.Description, in.NameI18n, in.DescriptionI18n, in.IconKey, in.Actor, in.ExpectedVersion, in.AuditID, in.AfterHash).Scan(&exists, &state, &current, &out.ID, &out.CategoryID, &out.Code, &out.Name, &out.Description, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
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

func (r *definitionRepository) RetireDefinition(ctx context.Context, in *entity.RetireDefinition) (*entity.DefinitionView, error) {
	query := fmt.Sprintf(`WITH target AS(SELECT * FROM %s.service_definitions WHERE id=$1 FOR UPDATE),updated AS(
		UPDATE %s.service_definitions definition SET state='retired',retired_by=$2,updated_by=$2,row_version=definition.row_version+1,updated_at=now()
		WHERE definition.id=$1 AND definition.row_version=$3 AND definition.state='active' RETURNING definition.*),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome) SELECT $4,$2,$5,'definition.retire','definition',id,row_version,'succeeded' FROM updated)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),COALESCE((SELECT id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT category_id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT code FROM updated),''),COALESCE((SELECT name FROM updated),''),COALESCE((SELECT description FROM updated),''),COALESCE((SELECT name_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT description_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT icon_key FROM updated),''),COALESCE((SELECT state::text FROM updated),''),COALESCE((SELECT row_version FROM updated),0),COALESCE((SELECT created_at FROM updated),now()),COALESCE((SELECT updated_at FROM updated),now())`, r.schema, r.schema, r.schema)
	out := &entity.DefinitionView{}
	var exists bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.DefinitionID, in.Actor, in.ExpectedVersion, in.AuditID, in.ProofID).Scan(&exists, &state, &current, &out.ID, &out.CategoryID, &out.Code, &out.Name, &out.Description, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
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
