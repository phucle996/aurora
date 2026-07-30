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

type versionRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewVersionRepository(db *pgxpool.Pool, schema string) managedrepo.VersionRepository {
	return &versionRepository{db: db, schema: schema}
}

func (r *versionRepository) CreateVersion(ctx context.Context, in *entity.CreateVersion) (*entity.VersionView, error) {
	query := fmt.Sprintf(`WITH parent AS(SELECT id,state FROM %s.service_definitions WHERE id=$1 FOR KEY SHARE),inserted AS(
		INSERT INTO %s.service_versions(id,definition_id,code,display_version,name_i18n,description_i18n,icon_key,created_by,updated_by)
		SELECT $2,$1,$3,$4,$5,$6,$7,$8,$8 FROM parent WHERE state='active' RETURNING *),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,action,record_kind,record_id,record_version,outcome,after_hash)SELECT $9,$8,'version.create','version',id,row_version,'succeeded',$10 FROM inserted)
		SELECT EXISTS(SELECT 1 FROM parent),COALESCE((SELECT state::text FROM parent),''),COALESCE((SELECT id FROM inserted),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT definition_id FROM inserted),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT code FROM inserted),''),COALESCE((SELECT display_version FROM inserted),''),COALESCE((SELECT name_i18n FROM inserted),'{}'::jsonb),COALESCE((SELECT description_i18n FROM inserted),'{}'::jsonb),COALESCE((SELECT icon_key FROM inserted),''),COALESCE((SELECT state::text FROM inserted),''),COALESCE((SELECT row_version FROM inserted),0),COALESCE((SELECT created_at FROM inserted),now()),COALESCE((SELECT updated_at FROM inserted),now())`, r.schema, r.schema, r.schema)
	out := &entity.VersionView{}
	var parentExists bool
	var parentState string
	err := r.db.QueryRow(ctx, query, in.DefinitionID, in.ID, in.Code, in.DisplayVersion, in.NameI18n, in.DescriptionI18n, in.IconKey, in.Actor, in.AuditID, in.AfterHash).Scan(&parentExists, &parentState, &out.ID, &out.DefinitionID, &out.Code, &out.DisplayVersion, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
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

func (r *versionRepository) ListVersions(ctx context.Context, in *entity.ListVersions) ([]entity.VersionView, error) {
	query := fmt.Sprintf(`SELECT id,definition_id,code,display_version,name_i18n,description_i18n,icon_key,state::text,row_version,created_at,updated_at FROM %s.service_versions WHERE($1::uuid='00000000-0000-0000-0000-000000000000' OR definition_id=$1)ORDER BY created_at DESC,id DESC LIMIT $2`, r.schema)
	rows, err := r.db.Query(ctx, query, in.DefinitionID, in.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]entity.VersionView, 0)
	for rows.Next() {
		var item entity.VersionView
		if err := rows.Scan(&item.ID, &item.DefinitionID, &item.Code, &item.DisplayVersion, &item.NameI18n, &item.DescriptionI18n, &item.IconKey, &item.State, &item.RowVersion, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *versionRepository) GetVersion(ctx context.Context, in *entity.GetVersion) (*entity.VersionView, error) {
	query := fmt.Sprintf(`SELECT id,definition_id,code,display_version,name_i18n,description_i18n,icon_key,state::text,row_version,created_at,updated_at FROM %s.service_versions WHERE id=$1`, r.schema)
	out := &entity.VersionView{}
	err := r.db.QueryRow(ctx, query, in.VersionID).Scan(&out.ID, &out.DefinitionID, &out.Code, &out.DisplayVersion, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *versionRepository) UpdateVersion(ctx context.Context, in *entity.UpdateVersion) (*entity.VersionView, error) {
	query := fmt.Sprintf(`WITH target AS(SELECT * FROM %s.service_versions WHERE id=$1 FOR UPDATE),updated AS(
		UPDATE %s.service_versions version SET display_version=$2,name_i18n=$3,description_i18n=$4,icon_key=$5,updated_by=$6,row_version=version.row_version+1,updated_at=now()
		WHERE version.id=$1 AND version.row_version=$7 AND version.state<>'retired' RETURNING version.*),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,action,record_kind,record_id,record_version,outcome,after_hash)SELECT $8,$6,'version.update','version',id,row_version,'succeeded',$9 FROM updated)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),COALESCE((SELECT id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT definition_id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT code FROM updated),''),COALESCE((SELECT display_version FROM updated),''),COALESCE((SELECT name_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT description_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT icon_key FROM updated),''),COALESCE((SELECT state::text FROM updated),''),COALESCE((SELECT row_version FROM updated),0),COALESCE((SELECT created_at FROM updated),now()),COALESCE((SELECT updated_at FROM updated),now())`, r.schema, r.schema, r.schema)
	out := &entity.VersionView{}
	var exists bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.VersionID, in.DisplayVersion, in.NameI18n, in.DescriptionI18n, in.IconKey, in.Actor, in.ExpectedVersion, in.AuditID, in.AfterHash).Scan(&exists, &state, &current, &out.ID, &out.DefinitionID, &out.Code, &out.DisplayVersion, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if state == "retired" {
		return nil, taxonomy.ErrCatalogInvalidTransition
	}
	if current != in.ExpectedVersion {
		return nil, taxonomy.ErrCatalogConcurrentChange
	}
	return out, nil
}

func (r *versionRepository) DeprecateVersion(ctx context.Context, in *entity.DeprecateVersion) (*entity.VersionView, error) {
	query := fmt.Sprintf(`WITH target AS(SELECT * FROM %s.service_versions WHERE id=$1 FOR UPDATE),updated AS(
		UPDATE %s.service_versions version SET state='deprecated',deprecated_by=$2,updated_by=$2,row_version=version.row_version+1,updated_at=now()WHERE version.id=$1 AND version.row_version=$3 AND version.state='available' RETURNING version.*),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome)SELECT $4,$2,$5,'version.deprecate','version',id,row_version,'succeeded' FROM updated)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),COALESCE((SELECT id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT definition_id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT code FROM updated),''),COALESCE((SELECT display_version FROM updated),''),COALESCE((SELECT name_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT description_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT icon_key FROM updated),''),COALESCE((SELECT state::text FROM updated),''),COALESCE((SELECT row_version FROM updated),0),COALESCE((SELECT created_at FROM updated),now()),COALESCE((SELECT updated_at FROM updated),now())`, r.schema, r.schema, r.schema)
	out := &entity.VersionView{}
	var exists bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.VersionID, in.Actor, in.ExpectedVersion, in.AuditID, in.ProofID).Scan(&exists, &state, &current, &out.ID, &out.DefinitionID, &out.Code, &out.DisplayVersion, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if state != "available" {
		return nil, taxonomy.ErrCatalogInvalidTransition
	}
	if current != in.ExpectedVersion {
		return nil, taxonomy.ErrCatalogConcurrentChange
	}
	return out, nil
}

func (r *versionRepository) RetireVersion(ctx context.Context, in *entity.RetireVersion) (*entity.VersionView, error) {
	query := fmt.Sprintf(`WITH target AS(SELECT * FROM %s.service_versions WHERE id=$1 FOR UPDATE),updated AS(
		UPDATE %s.service_versions version SET state='retired',retired_by=$2,updated_by=$2,row_version=version.row_version+1,updated_at=now()WHERE version.id=$1 AND version.row_version=$3 AND version.state IN('available','deprecated')RETURNING version.*),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome)SELECT $4,$2,$5,'version.retire','version',id,row_version,'succeeded' FROM updated)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),COALESCE((SELECT id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT definition_id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT code FROM updated),''),COALESCE((SELECT display_version FROM updated),''),COALESCE((SELECT name_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT description_i18n FROM updated),'{}'::jsonb),COALESCE((SELECT icon_key FROM updated),''),COALESCE((SELECT state::text FROM updated),''),COALESCE((SELECT row_version FROM updated),0),COALESCE((SELECT created_at FROM updated),now()),COALESCE((SELECT updated_at FROM updated),now())`, r.schema, r.schema, r.schema)
	out := &entity.VersionView{}
	var exists bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.VersionID, in.Actor, in.ExpectedVersion, in.AuditID, in.ProofID).Scan(&exists, &state, &current, &out.ID, &out.DefinitionID, &out.Code, &out.DisplayVersion, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if state == "retired" {
		return nil, taxonomy.ErrCatalogInvalidTransition
	}
	if current != in.ExpectedVersion {
		return nil, taxonomy.ErrCatalogConcurrentChange
	}
	return out, nil
}
