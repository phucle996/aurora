package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	entity "controlplane/internal/hierarchy/domain/entity"
	hierarchyrepo "controlplane/internal/hierarchy/domain/repo"
	taxonomy "controlplane/internal/hierarchy/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type zoneEncryptionKeyRepository struct {
	db            *pgxpool.Pool
	registerQuery string
	listQuery     string
	activateQuery string
	retireQuery   string
}

func NewZoneEncryptionKeyRepository(db *pgxpool.Pool, schema string) hierarchyrepo.ZoneEncryptionKeyRepository {
	return &zoneEncryptionKeyRepository{
		db: db,
		registerQuery: fmt.Sprintf(`
			WITH target_zone AS MATERIALIZED (
				SELECT id FROM %s.zones WHERE id = $2 FOR KEY SHARE
			), upserted AS (
				INSERT INTO %s.zone_encryption_keys (
					id, zone_id, public_key, fingerprint, algorithm, status,
					registered_by, registered_proof_id, created_at, updated_at
				)
				SELECT $1, target_zone.id, $3, $4, $5, 'staged', $6, $7, now(), now()
				FROM target_zone
				ON CONFLICT (fingerprint) DO UPDATE
				SET fingerprint = EXCLUDED.fingerprint
				RETURNING id, zone_id, public_key, fingerprint, algorithm, status::text,
					registered_by, created_at, updated_at
			)
			SELECT
				EXISTS(SELECT 1 FROM target_zone),
				COALESCE((SELECT id FROM upserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT zone_id FROM upserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT public_key FROM upserted), decode('', 'hex')),
				COALESCE((SELECT fingerprint FROM upserted), decode('', 'hex')),
				COALESCE((SELECT algorithm FROM upserted), ''),
				COALESCE((SELECT status FROM upserted), ''),
				COALESCE((SELECT registered_by FROM upserted), ''),
				COALESCE((SELECT created_at FROM upserted), now()),
				COALESCE((SELECT updated_at FROM upserted), now())
		`, schema, schema),
		listQuery: fmt.Sprintf(`
			WITH target_zone AS MATERIALIZED (
				SELECT id FROM %s.zones WHERE id = $1
			)
			SELECT target_zone.id, key.id, key.public_key, key.fingerprint,
				key.algorithm, key.status::text, key.registered_by,
				key.activated_by, key.decrypt_only_by, key.retired_by,
				key.created_at, key.updated_at, key.activated_at,
				key.decrypt_only_at, key.retired_at
			FROM target_zone
			LEFT JOIN %s.zone_encryption_keys key
				ON key.zone_id = target_zone.id
				AND (NOT $2 OR (key.created_at, key.id) < ($3, $4))
			ORDER BY key.created_at DESC, key.id DESC
			LIMIT $5
		`, schema, schema),
		activateQuery: fmt.Sprintf(`
			WITH zone_lock AS MATERIALIZED (
				-- [COMMENT]: The Zone row is the per-Zone mutex. It serializes
				-- concurrent rotations across every Controlplane replica.
				SELECT id FROM %s.zones WHERE id = $1 FOR UPDATE
			), target AS MATERIALIZED (
				SELECT key.*
				FROM %s.zone_encryption_keys key
				JOIN zone_lock ON zone_lock.id = key.zone_id
				WHERE key.id = $2
				FOR UPDATE OF key
			), rotated AS (
				-- [COMMENT]: Promote the target and demote the previous ACTIVE key
				-- in one UPDATE statement. The partial unique index therefore sees
				-- only the final state and never a transient two-ACTIVE-key state.
				UPDATE %s.zone_encryption_keys key
				SET status = CASE WHEN key.id = $2 THEN 'active'::%s.zone_encryption_key_status ELSE 'decrypt_only'::%s.zone_encryption_key_status END,
					activated_by = CASE WHEN key.id = $2 THEN $3 ELSE key.activated_by END,
					activated_proof_id = CASE WHEN key.id = $2 THEN $4 ELSE key.activated_proof_id END,
					activated_at = CASE WHEN key.id = $2 THEN now() ELSE key.activated_at END,
					decrypt_only_by = CASE WHEN key.id <> $2 THEN $3 ELSE key.decrypt_only_by END,
					decrypt_only_proof_id = CASE WHEN key.id <> $2 THEN $4 ELSE key.decrypt_only_proof_id END,
					decrypt_only_at = CASE WHEN key.id <> $2 THEN now() ELSE key.decrypt_only_at END,
					updated_at = now()
				FROM target
				WHERE target.status = 'staged'
					AND key.zone_id = $1
					AND (key.id = $2 OR key.status = 'active')
				RETURNING key.*
			), activated AS (
				SELECT * FROM rotated WHERE id = $2
			), selected AS (
				SELECT id, zone_id, public_key, fingerprint, algorithm, status::text,
					activated_by, created_at, updated_at, activated_at, true AS state_changed
				FROM activated
				UNION ALL
				SELECT target.id, target.zone_id, target.public_key, target.fingerprint,
					target.algorithm, target.status::text, target.activated_by,
					target.created_at, target.updated_at, target.activated_at, false
				FROM target
				WHERE target.status = 'active' AND NOT EXISTS(SELECT 1 FROM activated)
			)
			SELECT
				EXISTS(SELECT 1 FROM zone_lock),
				EXISTS(SELECT 1 FROM target),
				COALESCE((SELECT status::text FROM target), ''),
				EXISTS(SELECT 1 FROM selected),
				COALESCE((SELECT id FROM selected), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT zone_id FROM selected), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT public_key FROM selected), decode('', 'hex')),
				COALESCE((SELECT fingerprint FROM selected), decode('', 'hex')),
				COALESCE((SELECT algorithm FROM selected), ''),
				COALESCE((SELECT status FROM selected), ''),
				COALESCE((SELECT activated_by FROM selected), ''),
				COALESCE((SELECT created_at FROM selected), now()),
				COALESCE((SELECT updated_at FROM selected), now()),
				(SELECT activated_at FROM selected),
				COALESCE((SELECT state_changed FROM selected), false)
		`, schema, schema, schema, schema, schema),
		retireQuery: fmt.Sprintf(`
			WITH zone_lock AS MATERIALIZED (
				SELECT id FROM %s.zones WHERE id = $1 FOR UPDATE
			), target AS MATERIALIZED (
				SELECT key.*
				FROM %s.zone_encryption_keys key
				JOIN zone_lock ON zone_lock.id = key.zone_id
				WHERE key.id = $2
				FOR UPDATE OF key
			), retired AS (
				UPDATE %s.zone_encryption_keys key
				SET status = 'retired', retired_by = $3, retired_proof_id = $4,
					retired_at = now(), updated_at = now()
				FROM target
				WHERE key.id = target.id AND target.status IN ('staged', 'decrypt_only')
				RETURNING key.*
			), selected AS (
				SELECT id, zone_id, public_key, fingerprint, algorithm, status::text,
					retired_by, created_at, updated_at, retired_at, true AS state_changed
				FROM retired
				UNION ALL
				SELECT target.id, target.zone_id, target.public_key, target.fingerprint,
					target.algorithm, target.status::text, target.retired_by,
					target.created_at, target.updated_at, target.retired_at, false
				FROM target
				WHERE target.status = 'retired' AND NOT EXISTS(SELECT 1 FROM retired)
			)
			SELECT
				EXISTS(SELECT 1 FROM zone_lock),
				EXISTS(SELECT 1 FROM target),
				COALESCE((SELECT status::text FROM target), ''),
				EXISTS(SELECT 1 FROM selected),
				COALESCE((SELECT id FROM selected), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT zone_id FROM selected), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT public_key FROM selected), decode('', 'hex')),
				COALESCE((SELECT fingerprint FROM selected), decode('', 'hex')),
				COALESCE((SELECT algorithm FROM selected), ''),
				COALESCE((SELECT status FROM selected), ''),
				COALESCE((SELECT retired_by FROM selected), ''),
				COALESCE((SELECT created_at FROM selected), now()),
				COALESCE((SELECT updated_at FROM selected), now()),
				(SELECT retired_at FROM selected),
				COALESCE((SELECT state_changed FROM selected), false)
		`, schema, schema, schema),
	}
}

func (r *zoneEncryptionKeyRepository) RegisterZoneEncryptionKey(ctx context.Context, in *entity.RegisterZoneEncryptionKey) (*entity.RegisterZoneEncryptionKey, error) {
	out := &entity.RegisterZoneEncryptionKey{}
	var zoneExists bool
	err := r.db.QueryRow(ctx, r.registerQuery,
		in.ID, in.ZoneID, in.PublicKey, in.Fingerprint, in.Algorithm, in.Actor, in.ProofID,
	).Scan(
		&zoneExists, &out.ID, &out.ZoneID, &out.PublicKey, &out.Fingerprint,
		&out.Algorithm, &out.Status, &out.RegisteredBy, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, taxonomy.ErrZoneEncryptionKeyMaterialConflict
		}
		return nil, err
	}
	if !zoneExists {
		return nil, taxonomy.ErrZoneEncryptionKeyZoneNotFound
	}
	// [COMMENT]: Fingerprint uniqueness is global. Returning a row owned by a
	// different Zone means the same private counterpart was accidentally reused.
	if out.ZoneID != in.ZoneID {
		return nil, taxonomy.ErrZoneEncryptionKeyMaterialConflict
	}
	return out, nil
}

func (r *zoneEncryptionKeyRepository) ListZoneEncryptionKeys(ctx context.Context, in *entity.ListZoneEncryptionKeys) ([]entity.ListZoneEncryptionKeys, error) {
	rows, err := r.db.Query(ctx, r.listQuery, in.ZoneID, in.HasCursor, in.CursorCreatedAt, in.CursorID, in.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	zoneExists := false
	out := make([]entity.ListZoneEncryptionKeys, 0)
	for rows.Next() {
		zoneExists = true
		var zoneID uuid.UUID
		var keyID *uuid.UUID
		var publicKey, fingerprint []byte
		var algorithm, status, registeredBy *string
		var activatedBy, decryptOnlyBy, retiredBy *string
		var createdAt, updatedAt, activatedAt, decryptOnlyAt, retiredAt *time.Time
		if err := rows.Scan(
			&zoneID, &keyID, &publicKey, &fingerprint, &algorithm, &status,
			&registeredBy, &activatedBy, &decryptOnlyBy, &retiredBy,
			&createdAt, &updatedAt, &activatedAt, &decryptOnlyAt, &retiredAt,
		); err != nil {
			return nil, err
		}
		if keyID == nil {
			continue
		}
		item := entity.ListZoneEncryptionKeys{
			ZoneID: zoneID, ID: *keyID, PublicKey: publicKey, Fingerprint: fingerprint,
			ActivatedAt: activatedAt, DecryptOnlyAt: decryptOnlyAt, RetiredAt: retiredAt,
		}
		if algorithm != nil {
			item.Algorithm = *algorithm
		}
		if status != nil {
			item.Status = entity.ZoneEncryptionKeyStatus(*status)
		}
		if registeredBy != nil {
			item.RegisteredBy = *registeredBy
		}
		if activatedBy != nil {
			item.ActivatedBy = *activatedBy
		}
		if decryptOnlyBy != nil {
			item.DecryptOnlyBy = *decryptOnlyBy
		}
		if retiredBy != nil {
			item.RetiredBy = *retiredBy
		}
		if createdAt != nil {
			item.CreatedAt = *createdAt
		}
		if updatedAt != nil {
			item.UpdatedAt = *updatedAt
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !zoneExists {
		return nil, taxonomy.ErrZoneEncryptionKeyZoneNotFound
	}
	return out, nil
}

func (r *zoneEncryptionKeyRepository) ActivateZoneEncryptionKey(ctx context.Context, in *entity.ActivateZoneEncryptionKey) (*entity.ActivateZoneEncryptionKey, error) {
	out := &entity.ActivateZoneEncryptionKey{}
	var zoneExists, keyExists, selected bool
	var currentStatus string
	err := r.db.QueryRow(ctx, r.activateQuery, in.ZoneID, in.KeyID, in.Actor, in.ProofID).Scan(
		&zoneExists, &keyExists, &currentStatus, &selected,
		&out.KeyID, &out.ZoneID, &out.PublicKey, &out.Fingerprint, &out.Algorithm,
		&out.Status, &out.ActivatedBy, &out.CreatedAt, &out.UpdatedAt,
		&out.ActivatedAt, &out.StateChanged,
	)
	if err != nil {
		return nil, err
	}
	if !zoneExists {
		return nil, taxonomy.ErrZoneEncryptionKeyZoneNotFound
	}
	if !keyExists {
		return nil, taxonomy.ErrZoneEncryptionKeyNotFound
	}
	if !selected || (currentStatus != string(entity.ZoneEncryptionKeyStatusStaged) && currentStatus != string(entity.ZoneEncryptionKeyStatusActive)) {
		return nil, taxonomy.ErrZoneEncryptionKeyInvalidTransition
	}
	return out, nil
}

func (r *zoneEncryptionKeyRepository) RetireZoneEncryptionKey(ctx context.Context, in *entity.RetireZoneEncryptionKey) (*entity.RetireZoneEncryptionKey, error) {
	out := &entity.RetireZoneEncryptionKey{}
	var zoneExists, keyExists, selected bool
	var currentStatus string
	err := r.db.QueryRow(ctx, r.retireQuery, in.ZoneID, in.KeyID, in.Actor, in.ProofID).Scan(
		&zoneExists, &keyExists, &currentStatus, &selected,
		&out.KeyID, &out.ZoneID, &out.PublicKey, &out.Fingerprint, &out.Algorithm,
		&out.Status, &out.RetiredBy, &out.CreatedAt, &out.UpdatedAt,
		&out.RetiredAt, &out.StateChanged,
	)
	if err != nil {
		return nil, err
	}
	if !zoneExists {
		return nil, taxonomy.ErrZoneEncryptionKeyZoneNotFound
	}
	if !keyExists {
		return nil, taxonomy.ErrZoneEncryptionKeyNotFound
	}
	if !selected || (currentStatus != string(entity.ZoneEncryptionKeyStatusStaged) &&
		currentStatus != string(entity.ZoneEncryptionKeyStatusDecryptOnly) &&
		currentStatus != string(entity.ZoneEncryptionKeyStatusRetired)) {
		return nil, taxonomy.ErrZoneEncryptionKeyInvalidTransition
	}
	return out, nil
}
