package hierarchyRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"

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
	resolveQuery  string
}

func NewZoneEncryptionKeyRepository(
	db *pgxpool.Pool,
	hierarchySchema string,
	storageSchema string,
	mailSchema string,
	hypervisorSchema string,
	managedServiceSchema string,
) hierarchyRepoInterface.ZoneEncryptionKeyRepository {
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
		`, hierarchySchema, hierarchySchema),
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
		`, hierarchySchema, hierarchySchema),
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
		`, hierarchySchema, hierarchySchema, hierarchySchema, hierarchySchema, hierarchySchema),
		retireQuery: fmt.Sprintf(`
			WITH zone_lock AS MATERIALIZED (
				SELECT id FROM %s.zones WHERE id = $1 FOR UPDATE
			), target AS MATERIALIZED (
				SELECT key.*
				FROM %s.zone_encryption_keys key
				JOIN zone_lock ON zone_lock.id = key.zone_id
				WHERE key.id = $2
					FOR UPDATE OF key
			), retained_ciphertext AS MATERIALIZED (
				-- [COMMENT]: Retirement is the permission boundary for removing the
				-- Zone private counterpart. Check every retained command copy while the
				-- Zone mutex is held so rotation and retirement cannot race.
				SELECT EXISTS (
					SELECT 1 FROM %s.storage_outbox_records WHERE payload_key_id = $2
					UNION ALL
					SELECT 1 FROM %s.mail_outbox_records WHERE payload_key_id = $2
					UNION ALL
					SELECT 1 FROM %s.mail_protected_projections WHERE payload_key_id = $2
					UNION ALL
					SELECT 1 FROM %s.hypervisor_outbox_records WHERE payload_key_id = $2
					UNION ALL
					SELECT 1 FROM %s.managed_service_outbox_records WHERE payload_key_id = $2
					UNION ALL
					SELECT 1 FROM %s.personal_managed_service_instance_revisions WHERE payload_key_id = $2
					UNION ALL
					SELECT 1 FROM %s.tenant_managed_service_instance_revisions WHERE payload_key_id = $2
				) AS has_retained_ciphertext
			), retired AS (
				UPDATE %s.zone_encryption_keys key
				SET status = 'retired', retired_by = $3, retired_proof_id = $4,
					retired_at = now(), updated_at = now()
				FROM target
				WHERE key.id = target.id
					AND (
						target.status = 'staged'
						OR (target.status = 'decrypt_only' AND target.decrypt_only_at <= now() - interval '5 minutes')
					)
					AND NOT (SELECT has_retained_ciphertext FROM retained_ciphertext)
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
				COALESCE((SELECT state_changed FROM selected), false),
				COALESCE((SELECT has_retained_ciphertext FROM retained_ciphertext), false)
		`, hierarchySchema, hierarchySchema, storageSchema, mailSchema, mailSchema, hypervisorSchema, managedServiceSchema, managedServiceSchema, managedServiceSchema, hierarchySchema),
		resolveQuery: fmt.Sprintf(`
			WITH target_zone AS MATERIALIZED (
				SELECT id
				FROM %s.zones
				WHERE id = $1
			), selected AS MATERIALIZED (
				SELECT key.id, key.zone_id, key.public_key,
					(EXTRACT(EPOCH FROM (
						key.loaded_observed_at + interval '30 seconds' - statement_timestamp()
					)) * 1000000000)::bigint AS ready_for_nanoseconds
				FROM %s.zone_encryption_keys key
				JOIN target_zone ON target_zone.id = key.zone_id
				WHERE key.status = 'active'
					AND key.algorithm = $2
					AND key.loaded_at IS NOT NULL
					AND key.loaded_observed_at >= statement_timestamp() - interval '30 seconds'
					AND key.loaded_observed_fencing_token IS NOT NULL
			)
			SELECT
				EXISTS(SELECT 1 FROM target_zone),
				EXISTS(SELECT 1 FROM selected),
				COALESCE((SELECT id FROM selected), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT zone_id FROM selected), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT public_key FROM selected), decode('', 'hex')),
				COALESCE((SELECT ready_for_nanoseconds FROM selected), 0)
		`, hierarchySchema, hierarchySchema),
	}
}

func (r *zoneEncryptionKeyRepository) RegisterZoneEncryptionKey(ctx context.Context, in *hierarchyEntity.RegisterZoneEncryptionKey) (*hierarchyEntity.RegisterZoneEncryptionKey, error) {
	out := &hierarchyEntity.RegisterZoneEncryptionKey{}
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
			return nil, hierarchyTaxonomy.ErrConflict
		}
		return nil, err
	}
	if !zoneExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	// [COMMENT]: Fingerprint uniqueness is global. Returning a row owned by a
	// different Zone means the same private counterpart was accidentally reused.
	if out.ZoneID != in.ZoneID {
		return nil, hierarchyTaxonomy.ErrConflict
	}
	return out, nil
}

func (r *zoneEncryptionKeyRepository) ListZoneEncryptionKeys(ctx context.Context, in *hierarchyEntity.ListZoneEncryptionKeys) ([]hierarchyEntity.ListZoneEncryptionKeys, error) {
	rows, err := r.db.Query(ctx, r.listQuery, in.ZoneID, in.HasCursor, in.CursorCreatedAt, in.CursorID, in.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	zoneExists := false
	out := make([]hierarchyEntity.ListZoneEncryptionKeys, 0)
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
		item := hierarchyEntity.ListZoneEncryptionKeys{
			ZoneID: zoneID, ID: *keyID, PublicKey: publicKey, Fingerprint: fingerprint,
			ActivatedAt: activatedAt, DecryptOnlyAt: decryptOnlyAt, RetiredAt: retiredAt,
		}
		if algorithm != nil {
			item.Algorithm = *algorithm
		}
		if status != nil {
			item.Status = hierarchyEntity.ZoneEncryptionKeyStatus(*status)
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
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	return out, nil
}

func (r *zoneEncryptionKeyRepository) ActivateZoneEncryptionKey(ctx context.Context, in *hierarchyEntity.ActivateZoneEncryptionKey) (*hierarchyEntity.ActivateZoneEncryptionKey, error) {
	out := &hierarchyEntity.ActivateZoneEncryptionKey{}
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
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !keyExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !selected || (currentStatus != string(hierarchyEntity.ZoneEncryptionKeyStatusStaged) && currentStatus != string(hierarchyEntity.ZoneEncryptionKeyStatusActive)) {
		return nil, hierarchyTaxonomy.ErrInvalidTransition
	}
	return out, nil
}

func (r *zoneEncryptionKeyRepository) RetireZoneEncryptionKey(ctx context.Context, in *hierarchyEntity.RetireZoneEncryptionKey) (*hierarchyEntity.RetireZoneEncryptionKey, error) {
	out := &hierarchyEntity.RetireZoneEncryptionKey{}
	var zoneExists, keyExists, selected, retainedCiphertext bool
	var currentStatus string
	err := r.db.QueryRow(ctx, r.retireQuery, in.ZoneID, in.KeyID, in.Actor, in.ProofID).Scan(
		&zoneExists, &keyExists, &currentStatus, &selected,
		&out.KeyID, &out.ZoneID, &out.PublicKey, &out.Fingerprint, &out.Algorithm,
		&out.Status, &out.RetiredBy, &out.CreatedAt, &out.UpdatedAt,
		&out.RetiredAt, &out.StateChanged, &retainedCiphertext,
	)
	if err != nil {
		return nil, err
	}
	if !zoneExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !keyExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if retainedCiphertext && currentStatus != string(hierarchyEntity.ZoneEncryptionKeyStatusRetired) {
		return nil, hierarchyTaxonomy.ErrConflict
	}
	if !selected && currentStatus == string(hierarchyEntity.ZoneEncryptionKeyStatusDecryptOnly) {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	if !selected || (currentStatus != string(hierarchyEntity.ZoneEncryptionKeyStatusStaged) &&
		currentStatus != string(hierarchyEntity.ZoneEncryptionKeyStatusDecryptOnly) &&
		currentStatus != string(hierarchyEntity.ZoneEncryptionKeyStatusRetired)) {
		return nil, hierarchyTaxonomy.ErrInvalidTransition
	}
	return out, nil
}

func (r *zoneEncryptionKeyRepository) ResolveZonePayloadKey(ctx context.Context, in *hierarchyEntity.ResolveZonePayloadKey) (*hierarchyEntity.ResolveZonePayloadKey, error) {
	out := &hierarchyEntity.ResolveZonePayloadKey{ZoneID: in.ZoneID}
	var zoneExists, selected bool
	var readyForNanoseconds int64
	err := r.db.QueryRow(ctx, r.resolveQuery, in.ZoneID, hierarchyEntity.ZoneEncryptionKeyAlgorithm).Scan(
		&zoneExists,
		&selected,
		&out.KeyID,
		&out.ZoneID,
		&out.PublicKey,
		&readyForNanoseconds,
	)
	if err != nil {
		return nil, err
	}
	if !zoneExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !selected || readyForNanoseconds <= 0 {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	out.ReadyFor = time.Duration(readyForNanoseconds)
	return out, nil
}
