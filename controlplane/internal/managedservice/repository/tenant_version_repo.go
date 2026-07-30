package repository

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	"controlplane/internal/managedservice/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type tenantCatalogVersionRepository struct {
	db              *pgxpool.Pool
	managedSchema   string
	hierarchySchema string
}

func NewTenantCatalogVersionRepository(db *pgxpool.Pool, managedSchema, hierarchySchema string) managedrepo.TenantCatalogVersionRepository {
	return &tenantCatalogVersionRepository{db: db, managedSchema: managedSchema, hierarchySchema: hierarchySchema}
}

func (r *tenantCatalogVersionRepository) GetTenantCatalogVersion(ctx context.Context, in *entity.GetTenantCatalogVersion) (*entity.TenantCatalogVersionView, error) {
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.tenant_workspaces workspace
		WHERE workspace.id=$1 AND workspace.tenant_id=$2 AND workspace.zone_id=$3
		  AND EXISTS (
			SELECT 1 FROM %s.zone_services capability
			WHERE capability.zone_id=$3 AND capability.desired_state=true
			  AND capability.service_type::text='managed_service'
		  )
	)
	SELECT category.id,category.code,category.name_i18n,category.description_i18n,category.icon_key,
		definition.id,definition.code,definition.name_i18n,definition.description_i18n,definition.icon_key,
		version.id,version.code,version.display_version,version.name_i18n,version.description_i18n,version.icon_key,
		revision.id,revision.revision,revision.contract_version,revision.contract_sha256,
		revision.input_schema,revision.input_schema_sha256,revision.ui_schema,revision.ui_schema_sha256
	FROM scope
	JOIN %s.service_versions version ON version.id=$4 AND version.state='available'
	JOIN %s.service_definitions definition ON definition.id=version.definition_id AND definition.state='active'
	JOIN %s.service_categories category ON category.id=definition.category_id AND category.state='active'
	JOIN %s.service_blueprints blueprint ON blueprint.version_id=version.id AND blueprint.state='active'
	JOIN %s.blueprint_revisions revision ON revision.id=blueprint.published_revision_id AND revision.state='published'
	WHERE revision.validated_row_version=revision.row_version
	  AND revision.validated_bundle_sha256=revision.template_bundle_sha256
	  AND revision.validated_contract_sha256=revision.contract_sha256
	  AND (
		revision.zone_selector->>'mode'='all'
		OR (
			revision.zone_selector->>'mode'='allow_list'
			AND jsonb_typeof(revision.zone_selector->'zone_ids')='array'
			AND EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(revision.zone_selector->'zone_ids') selected(zone_id)
				WHERE selected.zone_id=$3::text
			)
		)
	  )
	  AND jsonb_typeof(revision.capability_requirement->'all_of')='array'
	  AND NOT EXISTS (
		SELECT 1 FROM jsonb_array_elements_text(revision.capability_requirement->'all_of') required(service_type)
		WHERE NOT EXISTS (
			SELECT 1 FROM %s.zone_services service
			WHERE service.zone_id=$3 AND service.desired_state=true
			  AND service.service_type::text=required.service_type
		)
	  )`, r.hierarchySchema, r.hierarchySchema, r.managedSchema, r.managedSchema, r.managedSchema, r.managedSchema, r.managedSchema, r.hierarchySchema)

	out := &entity.TenantCatalogVersionView{}
	err := r.db.QueryRow(ctx, query, in.WorkspaceID, in.TenantID, in.ZoneID, in.VersionID).Scan(
		&out.CategoryID, &out.CategoryCode, &out.CategoryNameI18n, &out.CategoryDescriptionI18n, &out.CategoryIconKey,
		&out.DefinitionID, &out.DefinitionCode, &out.DefinitionNameI18n, &out.DefinitionDescriptionI18n, &out.DefinitionIconKey,
		&out.VersionID, &out.VersionCode, &out.VersionDisplay, &out.VersionNameI18n, &out.VersionDescriptionI18n, &out.VersionIconKey,
		&out.RevisionID, &out.RevisionNumber, &out.ContractVersion, &out.ContractSHA256,
		&out.InputSchema, &out.InputSchemaSHA256, &out.UISchema, &out.UISchemaSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, taxonomy.ErrCustomerCatalogNotFound
	}
	if err != nil {
		return nil, taxonomy.ErrCustomerCatalogUnavailable
	}
	if in.ExpectedRevisionID != uuid.Nil && in.ExpectedRevisionID != out.RevisionID {
		return nil, taxonomy.ErrCustomerCatalogStale
	}
	return out, nil
}
