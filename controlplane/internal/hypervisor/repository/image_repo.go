package hypervisorRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	jobpayload "controlplane/internal/security"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ImageRepoPostgres struct {
	db         *pgxpool.Pool
	hypervisor string
	hierarchy  string
	protector  jobpayload.Protector
}

func NewImageRepo(
	db *pgxpool.Pool,
	cfg *config.Config,
	protector jobpayload.Protector,
) hypervisorRepoInterface.ImageRepository {
	return &ImageRepoPostgres{
		db:         db,
		hypervisor: cfg.SchemaSQL.Hypervisor,
		hierarchy:  cfg.SchemaSQL.Hierarchy,
		protector:  protector,
	}
}

func (r *ImageRepoPostgres) RegisterImageMetadata(
	ctx context.Context,
	image *hypervisorEntity.ImageArtifact,
) (*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		INSERT INTO %s.image_artifacts (
			id, zone_id, name, code, distribution, release, revision,
			architecture, format, size_bytes, sha256, object_key,
			state, created_by, created_at, updated_at
		)
		SELECT $1, $2, $3, $4, $5, $6,
		       $7, $8, $9, $10, $11,
		       $12, $13, $14, $15, $16
		FROM %s.zones zone
		WHERE zone.id = $2
		  AND zone.status IN ('active', 'draining')
		  AND EXISTS (
		      SELECT 1
		      FROM %s.zone_services service
		      WHERE service.zone_id = zone.id
		        AND service.service_type = 'hypervisor'
		        AND service.desired_state = TRUE
		  )
		RETURNING id, zone_id, name, code, distribution, release, revision,
		          architecture, format, size_bytes, sha256, object_key,
		          state, created_by, provider_template_vmid, error_code, error_message,
		          created_at, updated_at, available_at
	`, r.hypervisor, r.hierarchy, r.hierarchy)

	current := &hypervisorEntity.ImageArtifact{}
	err := r.db.QueryRow(
		ctx,
		query,
		image.ID,
		image.ZoneID,
		image.Name,
		image.Code,
		image.Distribution,
		image.Release,
		image.Revision,
		image.Architecture,
		image.Format,
		image.SizeBytes,
		image.SHA256,
		image.ObjectKey,
		image.State,
		image.CreatedBy,
		image.CreatedAt,
		image.UpdatedAt,
	).Scan(
		&current.ID,
		&current.ZoneID,
		&current.Name,
		&current.Code,
		&current.Distribution,
		&current.Release,
		&current.Revision,
		&current.Architecture,
		&current.Format,
		&current.SizeBytes,
		&current.SHA256,
		&current.ObjectKey,
		&current.State,
		&current.CreatedBy,
		&current.ProviderTemplateVMID,
		&current.ErrorCode,
		&current.ErrorMessage,
		&current.CreatedAt,
		&current.UpdatedAt,
		&current.AvailableAt,
	)
	if err == nil {
		return current, nil
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return nil, hypervisorTaxonomy.ErrImageConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrScopeUnavailable
	}
	return nil, fmt.Errorf("hypervisor image repository: create upload: %w", err)
}

func (r *ImageRepoPostgres) ListAdmin(
	ctx context.Context,
	zoneID uuid.UUID,
	limit int32,
) ([]*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		SELECT id, zone_id, name, code, distribution, release, revision,
		       architecture, format, size_bytes, sha256, object_key,
		       state, created_by, provider_template_vmid, error_code, error_message,
		       created_at, updated_at, available_at
		FROM %s.image_artifacts
		WHERE zone_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, r.hypervisor)
	rows, err := r.db.Query(ctx, query, zoneID, limit)
	if err != nil {
		return nil, fmt.Errorf("hypervisor image repository: list admin images: %w", err)
	}
	defer rows.Close()

	images := make([]*hypervisorEntity.ImageArtifact, 0)
	for rows.Next() {
		image := &hypervisorEntity.ImageArtifact{}
		if err := rows.Scan(
			&image.ID,
			&image.ZoneID,
			&image.Name,
			&image.Code,
			&image.Distribution,
			&image.Release,
			&image.Revision,
			&image.Architecture,
			&image.Format,
			&image.SizeBytes,
			&image.SHA256,
			&image.ObjectKey,
			&image.State,
			&image.CreatedBy,
			&image.ProviderTemplateVMID,
			&image.ErrorCode,
			&image.ErrorMessage,
			&image.CreatedAt,
			&image.UpdatedAt,
			&image.AvailableAt,
		); err != nil {
			return nil, fmt.Errorf("hypervisor image repository: scan admin image: %w", err)
		}
		images = append(images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hypervisor image repository: iterate admin images: %w", err)
	}
	return images, nil
}

func (r *ImageRepoPostgres) ListCatalog(
	ctx context.Context,
	zoneID uuid.UUID,
) ([]*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		SELECT image.id, image.zone_id, image.name, image.code, image.distribution,
		       image.release, image.revision, image.architecture, image.format,
		       image.size_bytes, image.sha256, image.object_key, image.state,
		       image.created_by, image.provider_template_vmid, image.error_code, image.error_message,
		       image.created_at, image.updated_at, image.available_at
		FROM %s.image_artifacts image
		JOIN %s.zones zone
		  ON zone.id = image.zone_id
		 AND zone.status IN ('active', 'draining')
		WHERE image.zone_id = $1
		  AND image.state = 'AVAILABLE'
		  AND image.provider_template_vmid IS NOT NULL
		  AND EXISTS (
		      SELECT 1
		      FROM %s.zone_services service
		      WHERE service.zone_id = zone.id
		        AND service.service_type = 'hypervisor'
		        AND service.desired_state = TRUE
		  )
		ORDER BY image.distribution, image.release, image.name, image.revision DESC
	`, r.hypervisor, r.hierarchy, r.hierarchy)
	rows, err := r.db.Query(ctx, query, zoneID)
	if err != nil {
		return nil, fmt.Errorf("hypervisor image repository: list catalog: %w", err)
	}
	defer rows.Close()

	images := make([]*hypervisorEntity.ImageArtifact, 0)
	for rows.Next() {
		image := &hypervisorEntity.ImageArtifact{}
		if err := rows.Scan(
			&image.ID,
			&image.ZoneID,
			&image.Name,
			&image.Code,
			&image.Distribution,
			&image.Release,
			&image.Revision,
			&image.Architecture,
			&image.Format,
			&image.SizeBytes,
			&image.SHA256,
			&image.ObjectKey,
			&image.State,
			&image.CreatedBy,
			&image.ProviderTemplateVMID,
			&image.ErrorCode,
			&image.ErrorMessage,
			&image.CreatedAt,
			&image.UpdatedAt,
			&image.AvailableAt,
		); err != nil {
			return nil, fmt.Errorf("hypervisor image repository: scan catalog image: %w", err)
		}
		images = append(images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hypervisor image repository: iterate catalog: %w", err)
	}
	return images, nil
}

func (r *ImageRepoPostgres) GetAvailable(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
) (*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		SELECT id, zone_id, name, code, distribution, release, revision,
		       architecture, format, size_bytes, sha256, object_key,
		       state, created_by, provider_template_vmid, error_code, error_message,
		       created_at, updated_at, available_at
		FROM %s.image_artifacts
		WHERE id = $1
		  AND zone_id = $2
		  AND state = 'AVAILABLE'
		  AND provider_template_vmid IS NOT NULL
	`, r.hypervisor)
	image := &hypervisorEntity.ImageArtifact{}
	err := r.db.QueryRow(ctx, query, imageID, zoneID).Scan(
		&image.ID,
		&image.ZoneID,
		&image.Name,
		&image.Code,
		&image.Distribution,
		&image.Release,
		&image.Revision,
		&image.Architecture,
		&image.Format,
		&image.SizeBytes,
		&image.SHA256,
		&image.ObjectKey,
		&image.State,
		&image.CreatedBy,
		&image.ProviderTemplateVMID,
		&image.ErrorCode,
		&image.ErrorMessage,
		&image.CreatedAt,
		&image.UpdatedAt,
		&image.AvailableAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrImageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("hypervisor image repository: get available image: %w", err)
	}
	return image, nil
}

func (r *ImageRepoPostgres) Get(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
) (*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		SELECT id, zone_id, name, code, distribution, release, revision,
		       architecture, format, size_bytes, sha256, object_key,
		       state, created_by, provider_template_vmid, error_code, error_message,
		       created_at, updated_at, available_at
		FROM %s.image_artifacts
		WHERE id = $1
		  AND zone_id = $2
	`, r.hypervisor)
	image := &hypervisorEntity.ImageArtifact{}
	err := r.db.QueryRow(ctx, query, imageID, zoneID).Scan(
		&image.ID,
		&image.ZoneID,
		&image.Name,
		&image.Code,
		&image.Distribution,
		&image.Release,
		&image.Revision,
		&image.Architecture,
		&image.Format,
		&image.SizeBytes,
		&image.SHA256,
		&image.ObjectKey,
		&image.State,
		&image.CreatedBy,
		&image.ProviderTemplateVMID,
		&image.ErrorCode,
		&image.ErrorMessage,
		&image.CreatedAt,
		&image.UpdatedAt,
		&image.AvailableAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrImageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("hypervisor image repository: get image: %w", err)
	}
	return image, nil
}

func (r *ImageRepoPostgres) BeginImport(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
	outbox *hypervisorEntity.HypervisorOutboxRecord,
) (*hypervisorEntity.ImageArtifact, error) {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "HYPERVISOR", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: uint32(outbox.JobVersion), PayloadSchemaVersion: uint32(outbox.PayloadSchemaVersion)}, outbox.Payload)
	if err != nil {
		return nil, err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	query := fmt.Sprintf(`
		WITH locked_image AS (
			SELECT id
			FROM %s.image_artifacts
			WHERE id = $1
			  AND zone_id = $2
			  AND state IN ('UPLOADING', 'FAILED', 'QUARANTINED')
			FOR UPDATE
		),
		updated_image AS (
			UPDATE %s.image_artifacts image
			SET state = 'IMPORTING',
			    error_code = NULL,
			    error_message = NULL,
			    updated_at = NOW()
			FROM locked_image
			WHERE image.id = locked_image.id
			RETURNING image.*
		),
		inserted_outbox AS (
			INSERT INTO %s.hypervisor_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id,
				status, job_version, resource_id, payload_schema_version,
				trace_id, idle, payload_key_id
			)
			SELECT $3, $4, $5, $6, $7,
			       $8, $9, $10, $11, $12, $13, $14
			FROM updated_image
			RETURNING event_id
		)
		SELECT image.id, image.zone_id, image.name, image.code, image.distribution,
		       image.release, image.revision, image.architecture, image.format,
		       image.size_bytes, image.sha256, image.object_key, image.state,
		       image.created_by, image.provider_template_vmid, image.error_code, image.error_message,
		       image.created_at, image.updated_at, image.available_at
		FROM updated_image image
		JOIN inserted_outbox ON TRUE
	`, r.hypervisor, r.hypervisor, r.hypervisor)
	image := &hypervisorEntity.ImageArtifact{}
	err = r.db.QueryRow(
		ctx,
		query,
		imageID,
		zoneID,
		outbox.EventID,
		outbox.ZoneID,
		outbox.JobTopic,
		outbox.Payload,
		outbox.ActorUserID,
		outbox.Status,
		outbox.JobVersion,
		outbox.ResourceID,
		outbox.PayloadSchemaVersion,
		outbox.TraceID,
		outbox.IdleSeconds,
		outbox.PayloadKeyID,
	).Scan(
		&image.ID,
		&image.ZoneID,
		&image.Name,
		&image.Code,
		&image.Distribution,
		&image.Release,
		&image.Revision,
		&image.Architecture,
		&image.Format,
		&image.SizeBytes,
		&image.SHA256,
		&image.ObjectKey,
		&image.State,
		&image.CreatedBy,
		&image.ProviderTemplateVMID,
		&image.ErrorCode,
		&image.ErrorMessage,
		&image.CreatedAt,
		&image.UpdatedAt,
		&image.AvailableAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrImageStateConflict
	}
	if err != nil {
		return nil, fmt.Errorf("hypervisor image repository: begin import: %w", err)
	}
	return image, nil
}

func (r *ImageRepoPostgres) BeginDelete(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
	outbox *hypervisorEntity.HypervisorOutboxRecord,
) (*hypervisorEntity.ImageArtifact, error) {
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{ZoneID: outbox.ZoneID, SourceDomain: "HYPERVISOR", JobTopic: outbox.JobTopic, ResourceID: outbox.ResourceID, JobVersion: uint32(outbox.JobVersion), PayloadSchemaVersion: uint32(outbox.PayloadSchemaVersion)}, outbox.Payload)
	if err != nil {
		return nil, err
	}
	outbox.Payload, outbox.PayloadKeyID = protected.Payload, protected.KeyID
	query := fmt.Sprintf(`
		WITH locked_image AS (
			SELECT id
			FROM %s.image_artifacts
			WHERE id = $1
			  AND zone_id = $2
			  AND state IN ('AVAILABLE', 'FAILED', 'QUARANTINED')
			FOR UPDATE
		),
		updated_image AS (
			UPDATE %s.image_artifacts image
			SET state = 'DELETING',
			    error_code = NULL,
			    error_message = NULL,
			    updated_at = NOW()
			FROM locked_image
			WHERE image.id = locked_image.id
			RETURNING image.*
		),
		inserted_outbox AS (
			INSERT INTO %s.hypervisor_outbox_records (
				event_id, zone_id, job_topic, payload, actor_user_id,
				status, job_version, resource_id, payload_schema_version,
				trace_id, idle, payload_key_id
			)
			SELECT $3, $4, $5, $6, $7,
			       $8, $9, $10, $11, $12, $13, $14
			FROM updated_image
			RETURNING event_id
		)
		SELECT image.id, image.zone_id, image.name, image.code, image.distribution,
		       image.release, image.revision, image.architecture, image.format,
		       image.size_bytes, image.sha256, image.object_key, image.state,
		       image.created_by, image.provider_template_vmid, image.error_code, image.error_message,
		       image.created_at, image.updated_at, image.available_at
		FROM updated_image image
		JOIN inserted_outbox ON TRUE
	`, r.hypervisor, r.hypervisor, r.hypervisor)
	image := &hypervisorEntity.ImageArtifact{}
	err = r.db.QueryRow(
		ctx,
		query,
		imageID,
		zoneID,
		outbox.EventID,
		outbox.ZoneID,
		outbox.JobTopic,
		outbox.Payload,
		outbox.ActorUserID,
		outbox.Status,
		outbox.JobVersion,
		outbox.ResourceID,
		outbox.PayloadSchemaVersion,
		outbox.TraceID,
		outbox.IdleSeconds,
		outbox.PayloadKeyID,
	).Scan(
		&image.ID,
		&image.ZoneID,
		&image.Name,
		&image.Code,
		&image.Distribution,
		&image.Release,
		&image.Revision,
		&image.Architecture,
		&image.Format,
		&image.SizeBytes,
		&image.SHA256,
		&image.ObjectKey,
		&image.State,
		&image.CreatedBy,
		&image.ProviderTemplateVMID,
		&image.ErrorCode,
		&image.ErrorMessage,
		&image.CreatedAt,
		&image.UpdatedAt,
		&image.AvailableAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrImageStateConflict
	}
	if err != nil {
		return nil, fmt.Errorf("hypervisor image repository: begin delete: %w", err)
	}
	return image, nil
}
