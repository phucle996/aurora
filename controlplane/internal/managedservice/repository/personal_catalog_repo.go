package repository

import (
	"context"
	"fmt"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	"controlplane/internal/managedservice/taxonomy"

	"github.com/jackc/pgx/v5/pgxpool"
)

type personalCatalogRepository struct {
	db              *pgxpool.Pool
	managedSchema   string
	hierarchySchema string
}

func NewPersonalCatalogRepository(db *pgxpool.Pool, managedSchema, hierarchySchema string) managedrepo.PersonalCatalogRepository {
	return &personalCatalogRepository{db: db, managedSchema: managedSchema, hierarchySchema: hierarchySchema}
}

func (r *personalCatalogRepository) ListPersonalCatalog(ctx context.Context, in *entity.ListPersonalCatalog) (*entity.PersonalCatalogPage, error) {
	// [COMMENT]: The trusted personal workspace/Zone binding and catalog
	// eligibility are evaluated by one PostgreSQL statement snapshot. A list
	// response is discovery only; P04 must recheck the same predicate while
	// committing desired state because the default revision may change later.
	query := fmt.Sprintf(`WITH scope AS MATERIALIZED (
		SELECT workspace.id
		FROM %s.personal_workspaces workspace
		WHERE workspace.id=$1 AND workspace.owner_id=$2 AND workspace.zone_id=$3
	), eligible AS (
		SELECT category.id,category.code,category.name_i18n,category.description_i18n,category.icon_key,
			definition.id,definition.code,definition.name_i18n,definition.description_i18n,definition.icon_key,
			version.id,version.code,version.display_version,version.name_i18n,version.description_i18n,version.icon_key,
			revision.id,revision.revision,revision.contract_version,revision.contract_sha256
		FROM scope
		JOIN %s.service_categories category ON category.state='active'
		JOIN %s.service_definitions definition ON definition.category_id=category.id AND definition.state='active'
		JOIN %s.service_versions version ON version.definition_id=definition.id AND version.state='available'
		JOIN %s.service_blueprints blueprint ON blueprint.version_id=version.id AND blueprint.state='active'
		JOIN %s.blueprint_revisions revision ON revision.id=blueprint.published_revision_id AND revision.state='published'
		WHERE version.id>$4
		  AND revision.validated_row_version=revision.row_version
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
			SELECT 1
			FROM jsonb_array_elements_text(revision.capability_requirement->'all_of') required(service_type)
			WHERE NOT EXISTS (
				SELECT 1 FROM %s.zone_services service
				WHERE service.zone_id=$3 AND service.desired_state=true
				  AND service.service_type::text=required.service_type
			)
		  )
		ORDER BY version.id
		LIMIT $5
	)
	SELECT * FROM eligible`, r.hierarchySchema, r.managedSchema, r.managedSchema, r.managedSchema, r.managedSchema, r.managedSchema, r.hierarchySchema)

	rows, err := r.db.Query(ctx, query, in.WorkspaceID, in.UserID, in.ZoneID, in.AfterVersionID, in.Limit+1)
	if err != nil {
		return nil, taxonomy.ErrCustomerCatalogUnavailable
	}
	defer rows.Close()

	items := make([]entity.PersonalCatalogItem, 0, in.Limit+1)
	for rows.Next() {
		var item entity.PersonalCatalogItem
		if err := rows.Scan(
			&item.CategoryID, &item.CategoryCode, &item.CategoryNameI18n, &item.CategoryDescriptionI18n, &item.CategoryIconKey,
			&item.DefinitionID, &item.DefinitionCode, &item.DefinitionNameI18n, &item.DefinitionDescriptionI18n, &item.DefinitionIconKey,
			&item.VersionID, &item.VersionCode, &item.VersionDisplay, &item.VersionNameI18n, &item.VersionDescriptionI18n, &item.VersionIconKey,
			&item.RevisionID, &item.RevisionNumber, &item.ContractVersion, &item.ContractSHA256,
		); err != nil {
			return nil, taxonomy.ErrCustomerCatalogUnavailable
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, taxonomy.ErrCustomerCatalogUnavailable
	}

	page := &entity.PersonalCatalogPage{Items: items}
	if len(items) > in.Limit {
		page.HasMore = true
		page.Items = items[:in.Limit]
	}
	return page, nil
}
