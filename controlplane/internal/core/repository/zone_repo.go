package coreRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreErrorx "controlplane/internal/core/errorx"
	coreModel "controlplane/internal/core/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ZoneRepoImpl struct {
	db     *pgxpool.Pool
	schema string
}

func NewZoneRepoImpl(cfg *config.Config, db *pgxpool.Pool) coreRepoInterface.ZoneRepository {
	return &ZoneRepoImpl{db: db, schema: cfg.SchemaSQL.Core}
}

func (r *ZoneRepoImpl) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	query := fmt.Sprintf(`SELECT id, code, name, status, created_at, updated_at FROM %s.zones ORDER BY created_at DESC`, r.schema)
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]coreEntity.Zone, 0)
	for rows.Next() {
		var value coreModel.Zone
		if err := rows.Scan(&value.ID, &value.Code, &value.Name, &value.Status, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, coreModel.ZoneModelToEntity(value))
	}
	return out, rows.Err()
}

func (r *ZoneRepoImpl) CreateZone(ctx context.Context, zone coreEntity.Zone) error {
	value := coreModel.ZoneEntityToModel(zone)
	query := fmt.Sprintf(`INSERT INTO %s.zones (id, code, name, status, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6)`, r.schema)
	_, err := r.db.Exec(ctx, query, value.ID, value.Code, value.Name, value.Status, value.CreatedAt.UTC(), value.UpdatedAt.UTC())
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return coreErrorx.ErrZoneCodeAlreadyExists
	}
	return err
}

func (r *ZoneRepoImpl) GetZoneByID(ctx context.Context, id uuid.UUID) (*coreEntity.Zone, error) {
	query := fmt.Sprintf(`SELECT id, code, name, status, created_at, updated_at FROM %s.zones WHERE id=$1 LIMIT 1`, r.schema)
	var value coreModel.Zone
	if err := r.db.QueryRow(ctx, query, id).Scan(&value.ID, &value.Code, &value.Name, &value.Status, &value.CreatedAt, &value.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	zone := coreModel.ZoneModelToEntity(value)
	return &zone, nil
}

func (r *ZoneRepoImpl) UpdateZoneStatus(ctx context.Context, id uuid.UUID, status coreEntity.ZoneStatus) error {
	query := fmt.Sprintf(`UPDATE %s.zones SET status=$2, updated_at=now() WHERE id=$1`, r.schema)
	result, err := r.db.Exec(ctx, query, id, string(status))
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return coreErrorx.ErrZoneNotFound
	}
	return nil
}

func (r *ZoneRepoImpl) DeleteZone(ctx context.Context, id uuid.UUID) error {
	query := fmt.Sprintf(`DELETE FROM %s.zones WHERE id=$1`, r.schema)
	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return coreErrorx.ErrZoneNotFound
	}
	return nil
}

func (r *ZoneRepoImpl) HasDataplaneNodesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error) {
	query := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s.dataplane_nodes WHERE zone_id=$1)`, r.schema)
	var exists bool
	if err := r.db.QueryRow(ctx, query, zoneID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (r *ZoneRepoImpl) HasEnabledZoneServicesByZone(ctx context.Context, zoneID uuid.UUID) (bool, error) {
	query := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s.zone_services WHERE zone_id=$1 AND enabled=true)`, r.schema)
	var exists bool
	if err := r.db.QueryRow(ctx, query, zoneID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (r *ZoneRepoImpl) ListZoneServicesByZoneID(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error) {
	query := fmt.Sprintf(`SELECT id, zone_id, service_type, enabled, created_at, updated_at FROM %s.zone_services WHERE zone_id=$1 ORDER BY service_type`, r.schema)
	rows, err := r.db.Query(ctx, query, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]coreEntity.ZoneService, 0)
	for rows.Next() {
		var value coreModel.ZoneService
		if err := rows.Scan(&value.ID, &value.ZoneID, &value.ServiceType, &value.Enabled, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, coreModel.ZoneServiceModelToEntity(value))
	}
	return out, rows.Err()
}

func (r *ZoneRepoImpl) UpsertZoneServiceByZoneAndType(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error) {
	query := fmt.Sprintf(`INSERT INTO %s.zone_services (id, zone_id, service_type, enabled, created_at, updated_at) VALUES ($1,$2,$3,$4,now(),now()) ON CONFLICT (zone_id, service_type) DO UPDATE SET enabled=EXCLUDED.enabled, updated_at=now() RETURNING id, zone_id, service_type, enabled, created_at, updated_at`, r.schema)
	newID, _ := uuid.NewV7()
	var value coreModel.ZoneService
	if err := r.db.QueryRow(ctx, query, newID, zoneID, string(serviceType), enabled).Scan(&value.ID, &value.ZoneID, &value.ServiceType, &value.Enabled, &value.CreatedAt, &value.UpdatedAt); err != nil {
		return nil, err
	}
	ent := coreModel.ZoneServiceModelToEntity(value)
	return &ent, nil
}
