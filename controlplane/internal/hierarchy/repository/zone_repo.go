package hierarchyRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ZoneRepoImpl struct {
	db                     *pgxpool.Pool
	listZonesQuery         string
	listZoneCatalogQuery   string
	resolveZoneByCodeQuery string
	createZoneQuery        string
	createZoneServiceQuery string
	getZoneDetailQuery     string
	updateZoneStatusQuery  string
	deleteZoneQuery        string
	updateZoneServiceQuery string
}

func NewZoneRepoImpl(cfg *config.Config, db *pgxpool.Pool) hierarchyRepoInterface.ZoneRepository {
	schema := cfg.SchemaSQL.Hierarchy
	return &ZoneRepoImpl{
		db: db,
		listZonesQuery: fmt.Sprintf(`
			SELECT id, code, name, location, status, updated_at
			FROM %s.zones
			ORDER BY created_at DESC
		`, schema),
		listZoneCatalogQuery: fmt.Sprintf(`
			SELECT id, code, name, status
			FROM %s.zones
			ORDER BY created_at DESC
		`, schema),
		resolveZoneByCodeQuery: fmt.Sprintf(`
			SELECT id, code, name, status
			FROM %s.zones
			WHERE code = $1
			LIMIT 1
		`, schema),
		createZoneQuery: fmt.Sprintf(`
			INSERT INTO %s.zones (id, code, name, location, description, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id, code, name, location, COALESCE(description, ''), status, created_at, updated_at
		`, schema),
		createZoneServiceQuery: fmt.Sprintf(`
			INSERT INTO %s.zone_services
				(id, zone_id, service_type, desired_state, created_at, updated_at)
			VALUES ($1, $2, $3, $4, now(), now())
		`, schema),
		getZoneDetailQuery: fmt.Sprintf(`
			SELECT z.id, z.code, z.name, z.location, COALESCE(z.description, ''),
				z.status, z.created_at, z.updated_at,
				s.id, s.service_type, s.desired_state, s.actual_state, s.created_at, s.updated_at
			FROM %s.zones z
			LEFT JOIN %s.zone_services s ON s.zone_id = z.id
			WHERE z.id = $1
			ORDER BY s.service_type
		`, schema, schema),
		// The Zone row is the serialization point shared by status, service and
		// delete workflows; their CTEs must depend on the locked target row.
		updateZoneStatusQuery: fmt.Sprintf(`
			WITH target AS MATERIALIZED (
				SELECT id, code, name, status::text FROM %s.zones WHERE id = $1 FOR UPDATE
			), updated AS (
				UPDATE %s.zones zone
				SET status = $2, updated_at = now()
				FROM target
				WHERE zone.id = $1 AND zone.id = target.id AND target.status = ANY($3)
				RETURNING zone.id
			)
			SELECT EXISTS(SELECT 1 FROM target), EXISTS(SELECT 1 FROM updated),
				COALESCE((SELECT code FROM target), ''),
				COALESCE((SELECT name FROM target), ''),
				COALESCE((SELECT status FROM target), '')
		`, schema, schema),
		deleteZoneQuery: fmt.Sprintf(`
			WITH target AS MATERIALIZED (
				SELECT id, code, status::text FROM %s.zones WHERE id = $1 FOR UPDATE
			), enabled_service AS MATERIALIZED (
				SELECT EXISTS(
					SELECT 1 FROM %s.zone_services service
					JOIN target ON target.id = service.zone_id
					WHERE service.desired_state = true
				) AS present
			), deleted AS (
				DELETE FROM %s.zones zone
				USING target, enabled_service
				WHERE zone.id = target.id
					AND target.status = 'disabled'
					AND NOT enabled_service.present
				RETURNING zone.id
			)
			SELECT EXISTS(SELECT 1 FROM target),
				COALESCE((SELECT status FROM target), ''),
				COALESCE((SELECT present FROM enabled_service), false),
				EXISTS(SELECT 1 FROM deleted),
				COALESCE((SELECT code FROM target), '')
		`, schema, schema, schema),
		updateZoneServiceQuery: fmt.Sprintf(`
			WITH target_zone AS MATERIALIZED (
				SELECT id, code, name, status::text FROM %s.zones WHERE id = $2 FOR UPDATE
			), upserted AS (
				INSERT INTO %s.zone_services
					(id, zone_id, service_type, desired_state, created_at, updated_at)
				SELECT $1, target_zone.id, $3, $4, now(), now()
				FROM target_zone
				WHERE target_zone.status = 'maintenance'
				ON CONFLICT (zone_id, service_type)
				DO UPDATE SET desired_state = EXCLUDED.desired_state, updated_at = now()
				RETURNING id, zone_id, service_type, desired_state, actual_state, created_at, updated_at
			)
			SELECT EXISTS(SELECT 1 FROM target_zone),
				COALESCE((SELECT status FROM target_zone), ''),
				EXISTS(SELECT 1 FROM upserted),
				COALESCE((SELECT code FROM target_zone), ''),
				COALESCE((SELECT name FROM target_zone), ''),
				COALESCE((SELECT id FROM upserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT zone_id FROM upserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT service_type::text FROM upserted), ''),
				COALESCE((SELECT desired_state FROM upserted), false),
				COALESCE((SELECT actual_state FROM upserted), 'unknown'),
				COALESCE((SELECT created_at FROM upserted), now()),
				COALESCE((SELECT updated_at FROM upserted), now())
		`, schema, schema),
	}
}

func (r *ZoneRepoImpl) ListZones(ctx context.Context, _ *hierarchyEntity.ListZones) ([]hierarchyEntity.ListZones, error) {
	rows, err := r.db.Query(ctx, r.listZonesQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]hierarchyEntity.ListZones, 0)
	for rows.Next() {
		var item hierarchyEntity.ListZones
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Location, &item.Status, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *ZoneRepoImpl) ListZoneCatalog(ctx context.Context, _ *hierarchyEntity.ListZoneCatalog) ([]hierarchyEntity.ListZoneCatalog, error) {
	rows, err := r.db.Query(ctx, r.listZoneCatalogQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]hierarchyEntity.ListZoneCatalog, 0)
	for rows.Next() {
		var item hierarchyEntity.ListZoneCatalog
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *ZoneRepoImpl) ResolveZoneByCode(ctx context.Context, in *hierarchyEntity.ResolveZoneByCode) (*hierarchyEntity.ResolveZoneByCode, error) {
	out := &hierarchyEntity.ResolveZoneByCode{Code: in.Code}
	err := r.db.QueryRow(ctx, r.resolveZoneByCodeQuery, in.Code).Scan(&out.ID, &out.Code, &out.Name, &out.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.Found = true
	return out, nil
}

func (r *ZoneRepoImpl) CreateZone(ctx context.Context, in *hierarchyEntity.CreateZone) (*hierarchyEntity.CreateZone, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	out := &hierarchyEntity.CreateZone{
		EnableHypervisor: in.EnableHypervisor, EnableStorage: in.EnableStorage,
		EnableMail: in.EnableMail, EnableKubernetes: in.EnableKubernetes,
		EnableAI: in.EnableAI, EnableDatabase: in.EnableDatabase,
		EnableManagedService: in.EnableManagedService,
		HypervisorServiceID:  in.HypervisorServiceID, StorageServiceID: in.StorageServiceID,
		MailServiceID: in.MailServiceID, KubernetesServiceID: in.KubernetesServiceID,
		AIServiceID: in.AIServiceID, DatabaseServiceID: in.DatabaseServiceID,
		ManagedServiceID: in.ManagedServiceID,
	}
	err = tx.QueryRow(ctx, r.createZoneQuery,
		in.ID, in.Code, in.Name, in.Location, in.Description, in.Status, in.CreatedAt, in.UpdatedAt,
	).Scan(&out.ID, &out.Code, &out.Name, &out.Location, &out.Description, &out.Status, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, hierarchyTaxonomy.ErrAlreadyExists
		}
		return nil, err
	}

	services := []struct {
		id      uuid.UUID
		kind    hierarchyEntity.ZoneServiceType
		enabled bool
	}{
		{in.HypervisorServiceID, hierarchyEntity.ZoneServiceTypeHypervisor, in.EnableHypervisor},
		{in.StorageServiceID, hierarchyEntity.ZoneServiceTypeStorage, in.EnableStorage},
		{in.MailServiceID, hierarchyEntity.ZoneServiceTypeMail, in.EnableMail},
		{in.KubernetesServiceID, hierarchyEntity.ZoneServiceTypeKubernetes, in.EnableKubernetes},
		{in.AIServiceID, hierarchyEntity.ZoneServiceTypeAI, in.EnableAI},
		{in.DatabaseServiceID, hierarchyEntity.ZoneServiceTypeDatabase, in.EnableDatabase},
		{in.ManagedServiceID, hierarchyEntity.ZoneServiceTypeManagedService, in.EnableManagedService},
	}
	for _, service := range services {
		if _, err := tx.Exec(ctx, r.createZoneServiceQuery, service.id, in.ID, service.kind, service.enabled); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ZoneRepoImpl) GetZoneDetail(ctx context.Context, in *hierarchyEntity.GetZoneDetail) ([]hierarchyEntity.GetZoneDetail, error) {
	rows, err := r.db.Query(ctx, r.getZoneDetailQuery, in.ZoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]hierarchyEntity.GetZoneDetail, 0)
	for rows.Next() {
		var item hierarchyEntity.GetZoneDetail
		var serviceID *uuid.UUID
		var serviceType *string
		var desiredState *bool
		var actualState *string
		var serviceCreatedAt, serviceUpdatedAt *time.Time
		if err := rows.Scan(
			&item.ZoneID, &item.ZoneCode, &item.ZoneName, &item.ZoneLocation,
			&item.ZoneDescription, &item.ZoneStatus, &item.ZoneCreatedAt, &item.ZoneUpdatedAt,
			&serviceID, &serviceType, &desiredState, &actualState, &serviceCreatedAt, &serviceUpdatedAt,
		); err != nil {
			return nil, err
		}
		if serviceID != nil && serviceType != nil && desiredState != nil && actualState != nil && serviceCreatedAt != nil && serviceUpdatedAt != nil {
			item.HasService = true
			item.ServiceID = *serviceID
			item.ServiceType = hierarchyEntity.ZoneServiceType(*serviceType)
			item.DesiredState = *desiredState
			item.ActualState = *actualState
			item.ServiceCreatedAt = *serviceCreatedAt
			item.ServiceUpdatedAt = *serviceUpdatedAt
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	return items, nil
}

func (r *ZoneRepoImpl) UpdateZoneStatus(ctx context.Context, in *hierarchyEntity.UpdateZoneStatus) (*hierarchyEntity.UpdateZoneStatus, error) {
	allowed := make([]string, len(in.AllowedFrom))
	for index, status := range in.AllowedFrom {
		allowed[index] = string(status)
	}
	var exists, updated bool
	var previousStatus string
	out := &hierarchyEntity.UpdateZoneStatus{ZoneID: in.ZoneID, Status: in.Status, AllowedFrom: in.AllowedFrom}
	if err := r.db.QueryRow(ctx, r.updateZoneStatusQuery, in.ZoneID, in.Status, allowed).Scan(
		&exists, &updated, &out.ZoneCode, &out.ZoneName, &previousStatus,
	); err != nil {
		return nil, err
	}
	if !exists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !updated {
		return nil, hierarchyTaxonomy.ErrInvalidTransition
	}
	out.StateChanged = previousStatus != string(in.Status)
	return out, nil
}

func (r *ZoneRepoImpl) DeleteZone(ctx context.Context, in *hierarchyEntity.DeleteZone) (*hierarchyEntity.DeleteZone, error) {
	var exists, hasServices, deleted bool
	var status string
	out := &hierarchyEntity.DeleteZone{ZoneID: in.ZoneID}
	if err := r.db.QueryRow(ctx, r.deleteZoneQuery, in.ZoneID).Scan(
		&exists, &status, &hasServices, &deleted, &out.ZoneCode,
	); err != nil {
		return nil, err
	}
	if !exists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if status != string(hierarchyEntity.ZoneStatusDisabled) || hasServices || !deleted {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	return out, nil
}

func (r *ZoneRepoImpl) UpdateZoneService(ctx context.Context, in *hierarchyEntity.UpdateZoneService) (*hierarchyEntity.UpdateZoneService, error) {
	var zoneExists, upserted bool
	var currentStatus string
	out := &hierarchyEntity.UpdateZoneService{}
	if err := r.db.QueryRow(ctx, r.updateZoneServiceQuery,
		in.ID, in.ZoneID, in.ServiceType, in.DesiredState,
	).Scan(
		&zoneExists, &currentStatus, &upserted, &out.ZoneCode, &out.ZoneName,
		&out.ID, &out.ZoneID, &out.ServiceType, &out.DesiredState, &out.ActualState,
		&out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if !zoneExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	out.ZoneStatus = hierarchyEntity.ZoneStatus(currentStatus)
	if currentStatus != string(hierarchyEntity.ZoneStatusMaintenance) || !upserted {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	return out, nil
}
