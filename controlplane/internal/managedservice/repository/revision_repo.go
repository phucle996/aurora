package repository

import (
	"context"
	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	"controlplane/internal/managedservice/taxonomy"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type revisionRepository struct {
	db     *pgxpool.Pool
	schema string
}

func NewRevisionRepository(db *pgxpool.Pool, schema string) managedrepo.RevisionRepository {
	return &revisionRepository{db: db, schema: schema}
}

func (r *revisionRepository) CreateDraft(ctx context.Context, in *entity.CreateDraft) (*entity.DraftView, error) {
	query := fmt.Sprintf(`WITH parent AS(SELECT id,state FROM %s.service_blueprints WHERE id=$1 FOR UPDATE),next_revision AS(
		SELECT COALESCE(MAX(revision),0)+1 value FROM %s.blueprint_revisions WHERE blueprint_id=$1),inserted AS(
		INSERT INTO %s.blueprint_revisions(id,blueprint_id,revision,template_yaml,template_bundle_sha256,contract_version,contract_sha256,component_contract,component_contract_sha256,input_schema,input_schema_sha256,ui_schema,ui_schema_sha256,safe_observed_output_schema,safe_observed_output_schema_sha256,zone_selector,zone_selector_sha256,capability_requirement,capability_requirement_sha256,created_by)
		SELECT $2,$1,next_revision.value,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19 FROM parent CROSS JOIN next_revision WHERE parent.state='active' RETURNING *),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome,after_hash)SELECT $20,$19,$21,'draft.create','revision',id,row_version,'succeeded',template_bundle_sha256 FROM inserted)
		SELECT EXISTS(SELECT 1 FROM parent),COALESCE((SELECT state::text FROM parent),''),COALESCE((SELECT id FROM inserted),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT blueprint_id FROM inserted),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT revision FROM inserted),0),COALESCE((SELECT state::text FROM inserted),''),COALESCE((SELECT template_yaml FROM inserted),''),COALESCE((SELECT template_bundle_sha256 FROM inserted),decode('','hex')),COALESCE((SELECT contract_version FROM inserted),''),COALESCE((SELECT contract_sha256 FROM inserted),decode('','hex')),COALESCE((SELECT component_contract FROM inserted),'[]'::jsonb),COALESCE((SELECT input_schema FROM inserted),'{}'::jsonb),COALESCE((SELECT ui_schema FROM inserted),'{}'::jsonb),COALESCE((SELECT safe_observed_output_schema FROM inserted),'{}'::jsonb),COALESCE((SELECT zone_selector FROM inserted),'{}'::jsonb),COALESCE((SELECT capability_requirement FROM inserted),'{}'::jsonb),COALESCE((SELECT row_version FROM inserted),0),(SELECT validated_row_version FROM inserted),(SELECT validated_at FROM inserted),COALESCE((SELECT created_at FROM inserted),now()),(SELECT published_at FROM inserted),(SELECT retired_at FROM inserted)`, r.schema, r.schema, r.schema, r.schema)
	out := &entity.DraftView{}
	var parentExists bool
	var parentState string
	err := r.db.QueryRow(ctx, query, in.BlueprintID, in.ID, in.TemplateYAML, in.TemplateBundleSHA256, in.ContractVersion, in.ContractSHA256, in.ComponentContract, in.ComponentContractSHA256, in.InputSchema, in.InputSchemaSHA256, in.UISchema, in.UISchemaSHA256, in.SafeObservedOutputSchema, in.SafeOutputSHA256, in.ZoneSelector, in.ZoneSelectorSHA256, in.CapabilityRequirement, in.CapabilitySHA256, in.Actor, in.AuditID, in.ProofID).Scan(&parentExists, &parentState, &out.ID, &out.BlueprintID, &out.Revision, &out.State, &out.TemplateYAML, &out.TemplateBundleSHA256, &out.ContractVersion, &out.ContractSHA256, &out.ComponentContract, &out.InputSchema, &out.UISchema, &out.SafeObservedOutputSchema, &out.ZoneSelector, &out.CapabilityRequirement, &out.RowVersion, &out.ValidatedRowVersion, &out.ValidatedAt, &out.CreatedAt, &out.PublishedAt, &out.RetiredAt)
	if err != nil {
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

func (r *revisionRepository) GetDraft(ctx context.Context, in *entity.GetDraft) (*entity.DraftView, error) {
	query := fmt.Sprintf(`SELECT id,blueprint_id,revision,state::text,template_yaml,template_bundle_sha256,contract_version,contract_sha256,component_contract,input_schema,ui_schema,safe_observed_output_schema,zone_selector,capability_requirement,row_version,validated_row_version,validated_at,created_at,published_at,retired_at FROM %s.blueprint_revisions WHERE id=$1 AND state='draft'`, r.schema)
	out := &entity.DraftView{}
	err := r.db.QueryRow(ctx, query, in.DraftID).Scan(&out.ID, &out.BlueprintID, &out.Revision, &out.State, &out.TemplateYAML, &out.TemplateBundleSHA256, &out.ContractVersion, &out.ContractSHA256, &out.ComponentContract, &out.InputSchema, &out.UISchema, &out.SafeObservedOutputSchema, &out.ZoneSelector, &out.CapabilityRequirement, &out.RowVersion, &out.ValidatedRowVersion, &out.ValidatedAt, &out.CreatedAt, &out.PublishedAt, &out.RetiredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *revisionRepository) ListRevisions(ctx context.Context, in *entity.ListRevisions) ([]entity.DraftView, error) {
	// [COMMENT]: The list projection intentionally excludes template_yaml and
	// contract documents; raw artifacts remain confined to the draft editor.
	query := fmt.Sprintf(`SELECT id,blueprint_id,revision,state::text,template_bundle_sha256,contract_version,contract_sha256,row_version,validated_row_version,validated_at,created_at,published_at,retired_at FROM %s.blueprint_revisions WHERE blueprint_id=$1 ORDER BY revision DESC,id DESC LIMIT $2`, r.schema)
	rows, err := r.db.Query(ctx, query, in.BlueprintID, in.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]entity.DraftView, 0)
	for rows.Next() {
		var item entity.DraftView
		if err := rows.Scan(&item.ID, &item.BlueprintID, &item.Revision, &item.State, &item.TemplateBundleSHA256, &item.ContractVersion, &item.ContractSHA256, &item.RowVersion, &item.ValidatedRowVersion, &item.ValidatedAt, &item.CreatedAt, &item.PublishedAt, &item.RetiredAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *revisionRepository) PatchDraft(ctx context.Context, in *entity.PatchDraft) (*entity.DraftView, error) {
	query := fmt.Sprintf(`WITH target AS(SELECT * FROM %s.blueprint_revisions WHERE id=$1 FOR UPDATE),updated AS(
		UPDATE %s.blueprint_revisions revision SET template_yaml=$2,template_bundle_sha256=$3,contract_version=$4,contract_sha256=$5,component_contract=$6,component_contract_sha256=$7,input_schema=$8,input_schema_sha256=$9,ui_schema=$10,ui_schema_sha256=$11,safe_observed_output_schema=$12,safe_observed_output_schema_sha256=$13,zone_selector=$14,zone_selector_sha256=$15,capability_requirement=$16,capability_requirement_sha256=$17,row_version=revision.row_version+1,validated_row_version=NULL,validation_contract_version=NULL,validated_bundle_sha256=NULL,validated_contract_sha256=NULL,validated_at=NULL,validated_by=NULL
		WHERE revision.id=$1 AND revision.row_version=$18 AND revision.state='draft' RETURNING revision.*),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome,after_hash)SELECT $19,$20,$21,'draft.patch','revision',id,row_version,'succeeded',template_bundle_sha256 FROM updated)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),COALESCE((SELECT id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT blueprint_id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT revision FROM updated),0),COALESCE((SELECT state::text FROM updated),''),COALESCE((SELECT template_yaml FROM updated),''),COALESCE((SELECT template_bundle_sha256 FROM updated),decode('','hex')),COALESCE((SELECT contract_version FROM updated),''),COALESCE((SELECT contract_sha256 FROM updated),decode('','hex')),COALESCE((SELECT component_contract FROM updated),'[]'::jsonb),COALESCE((SELECT input_schema FROM updated),'{}'::jsonb),COALESCE((SELECT ui_schema FROM updated),'{}'::jsonb),COALESCE((SELECT safe_observed_output_schema FROM updated),'{}'::jsonb),COALESCE((SELECT zone_selector FROM updated),'{}'::jsonb),COALESCE((SELECT capability_requirement FROM updated),'{}'::jsonb),COALESCE((SELECT row_version FROM updated),0),(SELECT validated_row_version FROM updated),(SELECT validated_at FROM updated),COALESCE((SELECT created_at FROM updated),now()),(SELECT published_at FROM updated),(SELECT retired_at FROM updated)`, r.schema, r.schema, r.schema)
	out := &entity.DraftView{}
	var exists bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.DraftID, in.TemplateYAML, in.TemplateBundleSHA256, in.ContractVersion, in.ContractSHA256, in.ComponentContract, in.ComponentContractSHA256, in.InputSchema, in.InputSchemaSHA256, in.UISchema, in.UISchemaSHA256, in.SafeObservedOutputSchema, in.SafeOutputSHA256, in.ZoneSelector, in.ZoneSelectorSHA256, in.CapabilityRequirement, in.CapabilitySHA256, in.ExpectedVersion, in.AuditID, in.Actor, in.ProofID).Scan(&exists, &state, &current, &out.ID, &out.BlueprintID, &out.Revision, &out.State, &out.TemplateYAML, &out.TemplateBundleSHA256, &out.ContractVersion, &out.ContractSHA256, &out.ComponentContract, &out.InputSchema, &out.UISchema, &out.SafeObservedOutputSchema, &out.ZoneSelector, &out.CapabilityRequirement, &out.RowVersion, &out.ValidatedRowVersion, &out.ValidatedAt, &out.CreatedAt, &out.PublishedAt, &out.RetiredAt)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if state != "draft" {
		return nil, taxonomy.ErrCatalogRecordImmutable
	}
	if current != in.ExpectedVersion {
		return nil, taxonomy.ErrCatalogConcurrentChange
	}
	return out, nil
}

func (r *revisionRepository) ValidateDraft(ctx context.Context, in *entity.ValidateDraft) (*entity.DraftView, error) {
	query := fmt.Sprintf(`WITH target AS(SELECT * FROM %s.blueprint_revisions WHERE id=$1 FOR UPDATE),updated AS(
		UPDATE %s.blueprint_revisions revision SET validated_row_version=revision.row_version,validation_contract_version=$2,validated_bundle_sha256=$3,validated_contract_sha256=$4,validated_at=now(),validated_by=$5
		WHERE revision.id=$1 AND revision.row_version=$6 AND revision.state='draft' AND revision.template_bundle_sha256=$3 AND revision.contract_sha256=$4 RETURNING revision.*),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome,after_hash)SELECT $7,$5,$8,'draft.validate','revision',id,row_version,'succeeded',template_bundle_sha256 FROM updated)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),COALESCE((SELECT template_bundle_sha256=$3 AND contract_sha256=$4 FROM target),false),COALESCE((SELECT id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT blueprint_id FROM updated),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT revision FROM updated),0),COALESCE((SELECT state::text FROM updated),''),COALESCE((SELECT template_yaml FROM updated),''),COALESCE((SELECT template_bundle_sha256 FROM updated),decode('','hex')),COALESCE((SELECT contract_version FROM updated),''),COALESCE((SELECT contract_sha256 FROM updated),decode('','hex')),COALESCE((SELECT component_contract FROM updated),'[]'::jsonb),COALESCE((SELECT input_schema FROM updated),'{}'::jsonb),COALESCE((SELECT ui_schema FROM updated),'{}'::jsonb),COALESCE((SELECT safe_observed_output_schema FROM updated),'{}'::jsonb),COALESCE((SELECT zone_selector FROM updated),'{}'::jsonb),COALESCE((SELECT capability_requirement FROM updated),'{}'::jsonb),COALESCE((SELECT row_version FROM updated),0),(SELECT validated_row_version FROM updated),(SELECT validated_at FROM updated),COALESCE((SELECT created_at FROM updated),now()),(SELECT published_at FROM updated),(SELECT retired_at FROM updated)`, r.schema, r.schema, r.schema)
	out := &entity.DraftView{}
	var exists, hashesMatch bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.DraftID, in.ValidationContract, in.TemplateBundleSHA256, in.ContractSHA256, in.Actor, in.ExpectedVersion, in.AuditID, in.ProofID).Scan(&exists, &state, &current, &hashesMatch, &out.ID, &out.BlueprintID, &out.Revision, &out.State, &out.TemplateYAML, &out.TemplateBundleSHA256, &out.ContractVersion, &out.ContractSHA256, &out.ComponentContract, &out.InputSchema, &out.UISchema, &out.SafeObservedOutputSchema, &out.ZoneSelector, &out.CapabilityRequirement, &out.RowVersion, &out.ValidatedRowVersion, &out.ValidatedAt, &out.CreatedAt, &out.PublishedAt, &out.RetiredAt)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if state != "draft" {
		return nil, taxonomy.ErrCatalogRecordImmutable
	}
	if current != in.ExpectedVersion || !hashesMatch {
		return nil, taxonomy.ErrCatalogRevisionStale
	}
	return out, nil
}

func (r *revisionRepository) PublishDraft(ctx context.Context, in *entity.PublishDraft) (*entity.DraftView, error) {
	query := fmt.Sprintf(`WITH locked AS(SELECT revision.* FROM %s.service_versions version JOIN %s.service_blueprints blueprint ON blueprint.version_id=version.id JOIN %s.blueprint_revisions revision ON revision.blueprint_id=blueprint.id WHERE revision.id=$1 FOR UPDATE OF version,blueprint,revision),published AS(
		UPDATE %s.blueprint_revisions revision SET state='published',published_at=now(),published_by=$2 WHERE revision.id=$1 AND revision.row_version=$3 AND revision.state='draft' AND revision.template_bundle_sha256=$4 AND revision.contract_sha256=$5 AND revision.validated_row_version=revision.row_version AND revision.validated_bundle_sha256=revision.template_bundle_sha256 AND revision.validated_contract_sha256=revision.contract_sha256 RETURNING revision.*),switched AS(
		UPDATE %s.service_blueprints blueprint SET published_revision_id=published.id,row_version=blueprint.row_version+1,updated_by=$2,updated_at=now()FROM published WHERE blueprint.id=published.blueprint_id RETURNING blueprint.id),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome,after_hash)SELECT $6,$2,$7,'draft.publish','revision',published.id,published.row_version,'succeeded',published.template_bundle_sha256 FROM published JOIN switched ON switched.id=published.blueprint_id)
		SELECT EXISTS(SELECT 1 FROM locked),COALESCE((SELECT state::text FROM locked),''),COALESCE((SELECT row_version FROM locked),0),COALESCE((SELECT template_bundle_sha256=$4 AND contract_sha256=$5 FROM locked),false),COALESCE((SELECT validated_row_version=row_version AND validated_bundle_sha256=template_bundle_sha256 AND validated_contract_sha256=contract_sha256 FROM locked),false),COALESCE((SELECT id FROM published),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT blueprint_id FROM published),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT revision FROM published),0),COALESCE((SELECT state::text FROM published),''),COALESCE((SELECT template_yaml FROM published),''),COALESCE((SELECT template_bundle_sha256 FROM published),decode('','hex')),COALESCE((SELECT contract_version FROM published),''),COALESCE((SELECT contract_sha256 FROM published),decode('','hex')),COALESCE((SELECT component_contract FROM published),'[]'::jsonb),COALESCE((SELECT input_schema FROM published),'{}'::jsonb),COALESCE((SELECT ui_schema FROM published),'{}'::jsonb),COALESCE((SELECT safe_observed_output_schema FROM published),'{}'::jsonb),COALESCE((SELECT zone_selector FROM published),'{}'::jsonb),COALESCE((SELECT capability_requirement FROM published),'{}'::jsonb),COALESCE((SELECT row_version FROM published),0),(SELECT validated_row_version FROM published),(SELECT validated_at FROM published),COALESCE((SELECT created_at FROM published),now()),(SELECT published_at FROM published),(SELECT retired_at FROM published)`, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema)
	out := &entity.DraftView{}
	var exists, hashesMatch, validationCurrent bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.DraftID, in.Actor, in.ExpectedVersion, in.ExpectedBundleSHA256, in.ExpectedContractHash, in.AuditID, in.ProofID).Scan(&exists, &state, &current, &hashesMatch, &validationCurrent, &out.ID, &out.BlueprintID, &out.Revision, &out.State, &out.TemplateYAML, &out.TemplateBundleSHA256, &out.ContractVersion, &out.ContractSHA256, &out.ComponentContract, &out.InputSchema, &out.UISchema, &out.SafeObservedOutputSchema, &out.ZoneSelector, &out.CapabilityRequirement, &out.RowVersion, &out.ValidatedRowVersion, &out.ValidatedAt, &out.CreatedAt, &out.PublishedAt, &out.RetiredAt)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if state != "draft" {
		return nil, taxonomy.ErrCatalogRecordImmutable
	}
	if current != in.ExpectedVersion || !hashesMatch {
		return nil, taxonomy.ErrCatalogRevisionStale
	}
	if !validationCurrent {
		return nil, taxonomy.ErrCatalogValidationFailed
	}
	return out, nil
}

func (r *revisionRepository) RetireRevision(ctx context.Context, in *entity.RetireRevision) (*entity.DraftView, error) {
	query := fmt.Sprintf(`WITH target AS(SELECT * FROM %s.blueprint_revisions WHERE id=$1 FOR UPDATE),eligible AS(
		SELECT * FROM target WHERE row_version=$3 AND state='published'),detached AS(
		UPDATE %s.service_blueprints blueprint SET published_revision_id=NULL,row_version=blueprint.row_version+1,updated_by=$2,updated_at=now()FROM eligible WHERE blueprint.id=eligible.blueprint_id AND blueprint.published_revision_id=eligible.id RETURNING blueprint.id),retired AS(
		UPDATE %s.blueprint_revisions revision SET state='retired',retired_at=now(),retired_by=$2 FROM eligible WHERE revision.id=eligible.id RETURNING revision.*),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome,after_hash)SELECT $4,$2,$5,'revision.retire','revision',id,row_version,'succeeded',template_bundle_sha256 FROM retired)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),COALESCE((SELECT id FROM retired),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT blueprint_id FROM retired),'00000000-0000-0000-0000-000000000000'::uuid),COALESCE((SELECT revision FROM retired),0),COALESCE((SELECT state::text FROM retired),''),COALESCE((SELECT template_yaml FROM retired),''),COALESCE((SELECT template_bundle_sha256 FROM retired),decode('','hex')),COALESCE((SELECT contract_version FROM retired),''),COALESCE((SELECT contract_sha256 FROM retired),decode('','hex')),COALESCE((SELECT component_contract FROM retired),'[]'::jsonb),COALESCE((SELECT input_schema FROM retired),'{}'::jsonb),COALESCE((SELECT ui_schema FROM retired),'{}'::jsonb),COALESCE((SELECT safe_observed_output_schema FROM retired),'{}'::jsonb),COALESCE((SELECT zone_selector FROM retired),'{}'::jsonb),COALESCE((SELECT capability_requirement FROM retired),'{}'::jsonb),COALESCE((SELECT row_version FROM retired),0),(SELECT validated_row_version FROM retired),(SELECT validated_at FROM retired),COALESCE((SELECT created_at FROM retired),now()),(SELECT published_at FROM retired),(SELECT retired_at FROM retired)`, r.schema, r.schema, r.schema, r.schema)
	out := &entity.DraftView{}
	var exists bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.RevisionID, in.Actor, in.ExpectedVersion, in.AuditID, in.ProofID).Scan(&exists, &state, &current, &out.ID, &out.BlueprintID, &out.Revision, &out.State, &out.TemplateYAML, &out.TemplateBundleSHA256, &out.ContractVersion, &out.ContractSHA256, &out.ComponentContract, &out.InputSchema, &out.UISchema, &out.SafeObservedOutputSchema, &out.ZoneSelector, &out.CapabilityRequirement, &out.RowVersion, &out.ValidatedRowVersion, &out.ValidatedAt, &out.CreatedAt, &out.PublishedAt, &out.RetiredAt)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, taxonomy.ErrCatalogNotFound
	}
	if state != "published" {
		return nil, taxonomy.ErrCatalogInvalidTransition
	}
	if current != in.ExpectedVersion {
		return nil, taxonomy.ErrCatalogConcurrentChange
	}
	return out, nil
}

func (r *revisionRepository) DeleteDraft(ctx context.Context, in *entity.DeleteDraft) error {
	query := fmt.Sprintf(`WITH target AS(SELECT * FROM %s.blueprint_revisions WHERE id=$1 FOR UPDATE),pinned AS(
		SELECT EXISTS(SELECT 1 FROM %s.personal_managed_service_instance_revisions WHERE blueprint_revision_id=$1)OR EXISTS(SELECT 1 FROM %s.tenant_managed_service_instance_revisions WHERE blueprint_revision_id=$1)value),deleted AS(
		DELETE FROM %s.blueprint_revisions revision WHERE revision.id=$1 AND revision.row_version=$2 AND revision.state='draft' AND NOT(SELECT value FROM pinned)RETURNING revision.id,revision.row_version),audited AS(
		INSERT INTO %s.catalog_audit_events(id,actor_subject,critical_proof_id,action,record_kind,record_id,record_version,outcome)SELECT $3,$4,$5,'draft.delete','revision',id,row_version,'succeeded' FROM deleted)
		SELECT EXISTS(SELECT 1 FROM target),COALESCE((SELECT state::text FROM target),''),COALESCE((SELECT row_version FROM target),0),(SELECT value FROM pinned),EXISTS(SELECT 1 FROM deleted)`, r.schema, r.schema, r.schema, r.schema, r.schema)
	var exists, pinned, deleted bool
	var state string
	var current int64
	err := r.db.QueryRow(ctx, query, in.DraftID, in.ExpectedVersion, in.AuditID, in.Actor, in.ProofID).Scan(&exists, &state, &current, &pinned, &deleted)
	if err != nil {
		return err
	}
	if !exists {
		return taxonomy.ErrCatalogNotFound
	}
	if pinned {
		return taxonomy.ErrCatalogRecordPinned
	}
	if state != "draft" {
		return taxonomy.ErrCatalogRecordImmutable
	}
	if current != in.ExpectedVersion {
		return taxonomy.ErrCatalogConcurrentChange
	}
	if !deleted {
		return taxonomy.ErrCatalogRecordImmutable
	}
	return nil
}
