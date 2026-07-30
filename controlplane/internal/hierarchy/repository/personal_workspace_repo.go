package hierarchyRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PersonalWorkspaceRepoImpl struct {
	db               *pgxpool.Pool
	createQuery      string
	listQuery        string
	listCatalogQuery string
	deleteQuery      string
}

func NewPersonalWorkspaceRepoImpl(cfg *config.Config, db *pgxpool.Pool) hierarchyRepoInterface.PersonalWorkspaceRepository {
	schema := cfg.SchemaSQL.Hierarchy
	return &PersonalWorkspaceRepoImpl{
		db: db,
		// FOR SHARE keeps a concurrent Zone status transition from invalidating
		// the active-parent precondition between the check and insert.
		createQuery: fmt.Sprintf(`
			WITH target_zone AS MATERIALIZED (
				SELECT id FROM %s.zones WHERE id = $5 AND status = 'active' FOR SHARE
			), inserted AS (
				INSERT INTO %s.personal_workspaces
					(id, name, code, description, zone_id, owner_id, created_at, updated_at)
				SELECT $1, $2, $3, $4, target_zone.id, $6, $7, $8
				FROM target_zone
				RETURNING id, name, code, COALESCE(description, ''), zone_id, owner_id, created_at, updated_at
			)
			SELECT EXISTS(SELECT 1 FROM target_zone),
				EXISTS(SELECT 1 FROM inserted),
				COALESCE((SELECT id FROM inserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT name FROM inserted), ''),
				COALESCE((SELECT code FROM inserted), ''),
				COALESCE((SELECT description FROM inserted), ''),
				COALESCE((SELECT zone_id FROM inserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT owner_id FROM inserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT created_at FROM inserted), now()),
				COALESCE((SELECT updated_at FROM inserted), now())
		`, schema, schema),
		listQuery: fmt.Sprintf(`
			SELECT id, name, code, COALESCE(description, ''), created_at
			FROM %s.personal_workspaces
			WHERE owner_id = $1
			ORDER BY created_at, id
		`, schema),
		listCatalogQuery: fmt.Sprintf(`
			SELECT id, code, name
			FROM %s.personal_workspaces
			WHERE owner_id = $1 AND zone_id = $2
			ORDER BY created_at, id
		`, schema),
		// Locking the complete owner scope serializes concurrent deletes so two
		// replicas cannot both observe a count of two and remove the final pair.
		deleteQuery: fmt.Sprintf(`
			WITH locked_workspaces AS MATERIALIZED (
				SELECT id FROM %s.personal_workspaces WHERE owner_id = $2 FOR UPDATE
			), target AS MATERIALIZED (
				SELECT id FROM locked_workspaces WHERE id = $1
			), workspace_count AS MATERIALIZED (
				SELECT COUNT(*) AS total FROM locked_workspaces
			), deleted AS (
				DELETE FROM %s.personal_workspaces
				WHERE id = $1 AND owner_id = $2 AND (SELECT total FROM workspace_count) > 1
				RETURNING id
			)
			SELECT EXISTS(SELECT 1 FROM target),
				COALESCE((SELECT total FROM workspace_count), 0),
				EXISTS(SELECT 1 FROM deleted)
		`, schema, schema),
	}
}

func (r *PersonalWorkspaceRepoImpl) CreateWorkspaceForPersonal(ctx context.Context, in *hierarchyEntity.CreatePersonalWorkspace) (*hierarchyEntity.CreatePersonalWorkspace, error) {
	var zoneExists, inserted bool
	out := &hierarchyEntity.CreatePersonalWorkspace{}
	err := r.db.QueryRow(ctx, r.createQuery,
		in.ID, in.Name, in.Code, in.Description, in.ZoneID, in.OwnerID, in.CreatedAt, in.UpdatedAt,
	).Scan(
		&zoneExists, &inserted, &out.ID, &out.Name, &out.Code, &out.Description,
		&out.ZoneID, &out.OwnerID, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, hierarchyTaxonomy.ErrAlreadyExists
		}
		return nil, err
	}
	if !zoneExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !inserted || out.ID == uuid.Nil {
		return nil, fmt.Errorf("create personal workspace returned no row")
	}
	return out, nil
}

func (r *PersonalWorkspaceRepoImpl) ListWorkspacesForPersonal(ctx context.Context, in *hierarchyEntity.ListPersonalWorkspaces) ([]hierarchyEntity.ListPersonalWorkspaces, error) {
	rows, err := r.db.Query(ctx, r.listQuery, in.OwnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]hierarchyEntity.ListPersonalWorkspaces, 0)
	for rows.Next() {
		item := hierarchyEntity.ListPersonalWorkspaces{OwnerID: in.OwnerID}
		if err := rows.Scan(&item.ID, &item.Name, &item.Code, &item.Description, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PersonalWorkspaceRepoImpl) ListWorkspaceCatalogForPersonal(ctx context.Context, in *hierarchyEntity.ListPersonalWorkspaceCatalog) ([]hierarchyEntity.ListPersonalWorkspaceCatalog, error) {
	rows, err := r.db.Query(ctx, r.listCatalogQuery, in.OwnerID, in.ZoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]hierarchyEntity.ListPersonalWorkspaceCatalog, 0)
	for rows.Next() {
		item := hierarchyEntity.ListPersonalWorkspaceCatalog{OwnerID: in.OwnerID, ZoneID: in.ZoneID}
		if err := rows.Scan(&item.ID, &item.Code, &item.Name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PersonalWorkspaceRepoImpl) DeleteWorkspaceForPersonal(ctx context.Context, in *hierarchyEntity.DeletePersonalWorkspace) error {
	var exists, deleted bool
	var total int
	err := r.db.QueryRow(ctx, r.deleteQuery, in.ID, in.OwnerID).Scan(&exists, &total, &deleted)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return hierarchyTaxonomy.ErrPreconditionFailed
		}
		return err
	}
	if !exists {
		return hierarchyTaxonomy.ErrNotFound
	}
	if total <= 1 || !deleted {
		return hierarchyTaxonomy.ErrPreconditionFailed
	}
	return nil
}
