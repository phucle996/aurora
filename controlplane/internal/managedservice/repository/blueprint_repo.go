package repository

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	"controlplane/internal/managedservice/taxonomy"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type blueprintRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewBlueprintRepository(db *pgxpool.Pool, schema string) managedrepo.BlueprintRepository {
	return &blueprintRepository{db: db, schema: schema}
}

func (r *blueprintRepository) CreateBlueprint(ctx context.Context, in *entity.CreateBlueprint) (*entity.BlueprintView, error) {
	query := fmt.Sprintf(`WITH parent AS(SELECT id,state FROM %s.service_versions WHERE id=$1 FOR KEY SHARE),inserted AS(
		INSERT INTO %s.service_blueprints(id,version_id,code,name,name_i18n,description_i18n,icon_key,created_by,updated_by)
		SELECT $2,$1,$3,$4,$5,$6,$7,$8,$8 FROM parent WHERE state='available' RETURNING *),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome,after_hash)
		SELECT $9,$8,$10,'blueprint.create','blueprint',id,row_version,'succeeded',$11 FROM inserted)
		SELECT EXISTS(SELECT 1 FROM parent),COALESCE((SELECT state::text FROM parent),''),COALESCE((SELECT id FROM inserted),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT version_id FROM inserted),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT code FROM inserted),''),COALESCE((SELECT name FROM inserted),''),COALESCE((SELECT name_i18n FROM inserted),'{}'::jsonb),COALESCE((SELECT description_i18n FROM inserted),'{}'::jsonb),COALESCE((SELECT icon_key FROM inserted),''),COALESCE((SELECT state::text FROM inserted),''),COALESCE((SELECT row_version FROM inserted),0),(SELECT published_revision_id FROM inserted),COALESCE((SELECT created_at FROM inserted),now()),COALESCE((SELECT updated_at FROM inserted),now())`, r.schema, r.schema, r.schema)
	out := &entity.BlueprintView{}
	var parentExists bool
	var parentState string
	err := r.db.QueryRow(ctx, query, in.VersionID, in.ID, in.Code, in.Name, in.NameI18n, in.DescriptionI18n, in.IconKey, in.Actor, in.AuditID, in.ProofID, in.AfterHash).Scan(&parentExists, &parentState, &out.ID, &out.VersionID, &out.Code, &out.Name, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.PublishedRevisionID, &out.CreatedAt, &out.UpdatedAt)
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
	if parentState != "available" {
		return nil, taxonomy.ErrCatalogParentRetired
	}
	return out, nil
}

func (r *blueprintRepository) GetBlueprint(ctx context.Context, in *entity.GetBlueprint) (*entity.BlueprintView, error) {
	query := fmt.Sprintf(`SELECT id,version_id,code,name,name_i18n,description_i18n,icon_key,state::text,row_version,published_revision_id,created_at,updated_at FROM %s.service_blueprints WHERE id=$1`, r.schema)
	out := &entity.BlueprintView{}
	err := r.db.QueryRow(ctx, query, in.BlueprintID).Scan(&out.ID, &out.VersionID, &out.Code, &out.Name, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.PublishedRevisionID, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *blueprintRepository) GetBlueprintByVersion(ctx context.Context, in *entity.GetBlueprintByVersion) (*entity.BlueprintView, error) {
	query := fmt.Sprintf(`SELECT id,version_id,code,name,name_i18n,description_i18n,icon_key,state::text,row_version,published_revision_id,created_at,updated_at FROM %s.service_blueprints WHERE version_id=$1`, r.schema)
	out := &entity.BlueprintView{}
	err := r.db.QueryRow(ctx, query, in.VersionID).Scan(&out.ID, &out.VersionID, &out.Code, &out.Name, &out.NameI18n, &out.DescriptionI18n, &out.IconKey, &out.State, &out.RowVersion, &out.PublishedRevisionID, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *blueprintRepository) DeleteBlueprint(ctx context.Context, in *entity.DeleteBlueprint) error {
	query := fmt.Sprintf(`WITH target AS(SELECT * FROM %s.service_blueprints WHERE id=$1 FOR UPDATE),referenced AS(
		SELECT EXISTS(SELECT 1 FROM %s.blueprint_revisions WHERE blueprint_id=$1)value),deleted AS(
		DELETE FROM %s.service_blueprints blueprint WHERE blueprint.id=$1 AND blueprint.row_version=$2 AND NOT(SELECT value FROM referenced)RETURNING blueprint.id,blueprint.row_version),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome)SELECT $3,$4,$5,'blueprint.delete','blueprint',id,row_version,'succeeded' FROM deleted)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT row_version FROM target),0),(SELECT value FROM referenced),EXISTS(SELECT 1 FROM deleted)`, r.schema, r.schema, r.schema, r.schema)
	var exists, referenced, deleted bool
	var current int64
	err := r.db.QueryRow(ctx, query, in.BlueprintID, in.ExpectedVersion, in.AuditID, in.Actor, in.ProofID).Scan(&exists, &current, &referenced, &deleted)
	if err != nil {
		return err
	}
	if !exists {
		return taxonomy.ErrCatalogNotFound
	}
	if referenced {
		return taxonomy.ErrCatalogRecordPinned
	}
	if current != in.ExpectedVersion {
		return taxonomy.ErrCatalogConcurrentChange
	}
	if !deleted {
		return taxonomy.ErrCatalogRecordImmutable
	}
	return nil
}
