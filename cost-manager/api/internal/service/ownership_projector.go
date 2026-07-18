package service

import (
	"context"
	"fmt"
	"time"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ownershipProjectionLock int64 = 20260002

var (
	ownershipProjectionNamespace = uuid.MustParse("717c033b-cb5d-5df8-b81a-7d3e2d6eb84c")
	credentialBindingNamespace   = uuid.MustParse("dddb17d8-b830-55e4-aa34-6c5f4ab4c6fc")
)

// OwnershipProjector reconciles Controlplane SoT into Billing DB; billing hot path never joins cross-DB.
type OwnershipProjector struct {
	source   *pgxpool.Pool
	target   *pgxpool.Pool
	interval time.Duration
}

func NewOwnershipProjector(source *pgxpool.Pool, target *pgxpool.Pool) *OwnershipProjector {
	return &OwnershipProjector{source: source, target: target, interval: 15 * time.Second}
}

type ownershipSourceRow struct {
	ResourceType string
	ResourceID   uuid.UUID
	ResourceName string
	OwnerID      uuid.UUID
	OwnerType    entity.OwnerType
	ZoneID       uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type credentialSourceRow struct {
	ID           uuid.UUID
	AccessKey    string
	ResourceType string
	ResourceID   uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Run continuously heals missed changes; only one Cost Manager replica reconciles at a time.
func (p *OwnershipProjector) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		if err := p.reconcile(ctx); err != nil && ctx.Err() == nil {
			fmt.Printf("Ownership projection reconciliation error: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *OwnershipProjector) reconcile(ctx context.Context) error {
	resources, err := p.loadResources(ctx)
	if err != nil {
		return err
	}
	credentials, err := p.loadCredentials(ctx)
	if err != nil {
		return err
	}

	tx, err := p.target.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin projection tx: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var elected bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, ownershipProjectionLock).Scan(&elected); err != nil {
		return fmt.Errorf("elect ownership projector: %w", err)
	}
	if !elected {
		return nil
	}

	seenResources := make(map[string]struct{}, len(resources))
	for _, source := range resources {
		key := source.ResourceType + ":" + source.ResourceID.String()
		seenResources[key] = struct{}{}
		if err = reconcileResource(ctx, tx, source); err != nil {
			return err
		}
	}
	if err = closeMissingResources(ctx, tx, seenResources); err != nil {
		return err
	}

	seenCredentials := make(map[uuid.UUID]struct{}, len(credentials))
	for _, source := range credentials {
		bindingID := uuid.NewSHA1(credentialBindingNamespace, []byte(source.ID.String()))
		// So sánh theo binding ID ngay từ đầu để reconciliation luôn O(n), kể cả khi credential lớn.
		seenCredentials[bindingID] = struct{}{}
		_, err = tx.Exec(ctx, `
			INSERT INTO billing.credential_bindings
				(id, access_key, credential_kind, resource_type, resource_id, valid_from, status, source_updated_at)
			VALUES ($1, $2, 'STATIC', $3, $4, $5, 'ACTIVE', $6)
			ON CONFLICT (id) DO UPDATE SET
				access_key = EXCLUDED.access_key,
				resource_type = EXCLUDED.resource_type,
				resource_id = EXCLUDED.resource_id,
				status = 'ACTIVE', valid_to = NULL,
				source_updated_at = EXCLUDED.source_updated_at,
				reconciled_at = NOW()
		`, bindingID, source.AccessKey, source.ResourceType, source.ResourceID, source.CreatedAt, source.UpdatedAt)
		if err != nil {
			return fmt.Errorf("upsert credential binding %s: %w", source.ID, err)
		}
	}
	if err = revokeMissingCredentials(ctx, tx, seenCredentials); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *OwnershipProjector) loadResources(ctx context.Context) ([]ownershipSourceRow, error) {
	rows, err := p.source.Query(ctx, `
		SELECT 'STORAGE_BUCKET', b.id, b.name, w.owner_id, 'PERSONAL', b.zone_id, b.created_at,
		       GREATEST(b.updated_at, w.updated_at)
		FROM storage.personal_buckets b
		JOIN hierarchy.personal_workspaces w ON w.id = b.workspace_id
		UNION ALL
		SELECT 'STORAGE_BUCKET', b.id, b.name, b.tenant_id, 'TENANT', b.zone_id, b.created_at,
		       GREATEST(b.updated_at, w.updated_at)
		FROM storage.tenant_buckets b
		JOIN hierarchy.tenant_workspaces w ON w.id = b.workspace_id
	`)
	if err != nil {
		return nil, fmt.Errorf("load storage ownership source: %w", err)
	}
	defer rows.Close()
	result := make([]ownershipSourceRow, 0)
	for rows.Next() {
		var row ownershipSourceRow
		var rawOwnerType string
		if err = rows.Scan(&row.ResourceType, &row.ResourceID, &row.ResourceName, &row.OwnerID, &rawOwnerType,
			&row.ZoneID, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan storage ownership source: %w", err)
		}
		row.OwnerType = entity.OwnerType(rawOwnerType)
		result = append(result, row)
	}
	return result, rows.Err()
}

func (p *OwnershipProjector) loadCredentials(ctx context.Context) ([]credentialSourceRow, error) {
	rows, err := p.source.Query(ctx, `
		SELECT c.id, c.access_key, 'STORAGE_BUCKET', c.bucket_id, c.created_at, c.updated_at
		FROM storage.personal_credentials c
		UNION ALL
		SELECT c.id, c.access_key, 'STORAGE_BUCKET', c.bucket_id, c.created_at, c.updated_at
		FROM storage.tenant_credentials c
	`)
	if err != nil {
		return nil, fmt.Errorf("load storage credential source: %w", err)
	}
	defer rows.Close()
	result := make([]credentialSourceRow, 0)
	for rows.Next() {
		var row credentialSourceRow
		if err = rows.Scan(&row.ID, &row.AccessKey, &row.ResourceType, &row.ResourceID, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan storage credential source: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func reconcileResource(ctx context.Context, tx pgx.Tx, source ownershipSourceRow) error {
	var currentID uuid.UUID
	var currentOwnerID uuid.UUID
	var currentOwnerType string
	var currentName string
	var currentZoneID uuid.UUID
	var currentVersion int
	var effectiveFrom time.Time
	err := tx.QueryRow(ctx, `
		SELECT id, owner_id, owner_type::text, resource_name, zone_id, ownership_version, effective_from
		FROM billing.resource_ownership_projection
		WHERE resource_type = $1 AND resource_id = $2 AND effective_to IS NULL
		FOR UPDATE
	`, source.ResourceType, source.ResourceID).Scan(&currentID, &currentOwnerID, &currentOwnerType, &currentName,
		&currentZoneID, &currentVersion, &effectiveFrom)
	if err == nil && currentOwnerID == source.OwnerID && currentOwnerType == string(source.OwnerType) &&
		currentName == source.ResourceName && currentZoneID == source.ZoneID {
		_, err = tx.Exec(ctx, `
			UPDATE billing.resource_ownership_projection
			SET source_updated_at = GREATEST(source_updated_at, $1), reconciled_at = NOW()
			WHERE id = $2
		`, source.UpdatedAt, currentID)
		return err
	}
	if err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("read active resource projection %s: %w", source.ResourceID, err)
	}

	version := 1
	effectiveAt := source.CreatedAt
	if err == nil {
		version = currentVersion + 1
		effectiveAt = source.UpdatedAt
		if !effectiveAt.After(effectiveFrom) {
			effectiveAt = time.Now().UTC()
		}
		if _, err = tx.Exec(ctx, `UPDATE billing.resource_ownership_projection SET effective_to = $1, reconciled_at = NOW() WHERE id = $2`, effectiveAt, currentID); err != nil {
			return fmt.Errorf("close resource projection %s: %w", source.ResourceID, err)
		}
	}

	projectionID := uuid.NewSHA1(ownershipProjectionNamespace, []byte(fmt.Sprintf("%s:%s:%d", source.ResourceType, source.ResourceID, version)))
	_, err = tx.Exec(ctx, `
		INSERT INTO billing.resource_ownership_projection
			(id, resource_type, resource_id, resource_name, owner_id, owner_type, zone_id,
			 ownership_version, effective_from, source_updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::billing.owner_type, $7, $8, $9, $10)
	`, projectionID, source.ResourceType, source.ResourceID, source.ResourceName, source.OwnerID,
		string(source.OwnerType), source.ZoneID, version, effectiveAt, source.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert resource projection %s: %w", source.ResourceID, err)
	}
	return nil
}

func closeMissingResources(ctx context.Context, tx pgx.Tx, seen map[string]struct{}) error {
	rows, err := tx.Query(ctx, `SELECT id, resource_type, resource_id FROM billing.resource_ownership_projection WHERE effective_to IS NULL FOR UPDATE`)
	if err != nil {
		return fmt.Errorf("list active resource projections: %w", err)
	}
	type activeRow struct {
		id           uuid.UUID
		resourceType string
		resourceID   uuid.UUID
	}
	active := make([]activeRow, 0)
	for rows.Next() {
		var row activeRow
		if err = rows.Scan(&row.id, &row.resourceType, &row.resourceID); err != nil {
			rows.Close()
			return err
		}
		active = append(active, row)
	}
	rows.Close()
	for _, row := range active {
		if _, ok := seen[row.resourceType+":"+row.resourceID.String()]; ok {
			continue
		}
		if _, err = tx.Exec(ctx, `UPDATE billing.resource_ownership_projection SET effective_to = NOW(), reconciled_at = NOW() WHERE id = $1`, row.id); err != nil {
			return fmt.Errorf("close missing resource projection %s: %w", row.resourceID, err)
		}
	}
	return nil
}

func revokeMissingCredentials(ctx context.Context, tx pgx.Tx, seen map[uuid.UUID]struct{}) error {
	rows, err := tx.Query(ctx, `SELECT id FROM billing.credential_bindings WHERE credential_kind = 'STATIC' AND status = 'ACTIVE' FOR UPDATE`)
	if err != nil {
		return fmt.Errorf("list active credential bindings: %w", err)
	}
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var bindingID uuid.UUID
		if err = rows.Scan(&bindingID); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, bindingID)
	}
	rows.Close()
	for _, bindingID := range ids {
		if _, present := seen[bindingID]; present {
			continue
		}
		if _, err = tx.Exec(ctx, `UPDATE billing.credential_bindings SET status = 'REVOKED', valid_to = NOW(), reconciled_at = NOW() WHERE id = $1`, bindingID); err != nil {
			return fmt.Errorf("revoke missing credential binding %s: %w", bindingID, err)
		}
	}
	return nil
}
