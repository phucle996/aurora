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

// ImageRepoPostgres triển khai ImageRepository trên PostgreSQL cho Hypervisor Controlplane.
// Đảm bảo việc quản lý vòng đời OS Image Artifact theo từng Zone độc lập và giao dịch Outbox nguyên tử qua CTE.
type ImageRepoPostgres struct {
	db         *pgxpool.Pool
	hypervisor string
	hierarchy  string
	protector  jobpayload.Protector
}

// NewImageRepo khởi tạo một thể hiện ImageRepoPostgres mới với schema và protector mã hóa outbox.
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

// RegisterImageMetadata đăng ký metadata của Image Artifact mới vào cơ sở dữ liệu.
// Thực hiện kiểm tra tính khả dụng của Zone (active/draining) và dịch vụ hypervisor trước khi chèn bản ghi.
func (r *ImageRepoPostgres) RegisterImageMetadata(
	ctx context.Context,
	image *hypervisorEntity.ImageArtifact,
) (*hypervisorEntity.ImageArtifact, error) {
	// [COMMENT]: Sử dụng INSERT ... SELECT để kiểm tra nguyên tử:
	// 1. Zone phải tồn tại và có trạng thái 'active' hoặc 'draining'.
	// 2. Zone phải được cấu hình dịch vụ 'hypervisor' với desired_state = TRUE.
	query := fmt.Sprintf(`
		INSERT INTO %s.image_artifacts (
			id,
			zone_id,
			name,
			code,
			distribution,
			release,
			revision,
			architecture,
			format,
			size_bytes,
			sha256,
			object_key,
			state,
			created_by,
			created_at,
			updated_at
		)
		SELECT
			$1,  -- id
			$2,  -- zone_id
			$3,  -- name
			$4,  -- code
			$5,  -- distribution
			$6,  -- release
			$7,  -- revision
			$8,  -- architecture
			$9,  -- format
			$10, -- size_bytes
			$11, -- sha256
			$12, -- object_key
			$13, -- state
			$14, -- created_by
			$15, -- created_at
			$16  -- updated_at
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
		RETURNING
			id,
			zone_id,
			name,
			code,
			distribution,
			release,
			revision,
			architecture,
			format,
			size_bytes,
			sha256,
			object_key,
			state,
			created_by,
			provider_template_vmid,
			error_code,
			error_message,
			created_at,
			updated_at,
			available_at
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

	// [COMMENT]: Bắt lỗi trùng lặp ràng buộc UNIQUE (zone_id, code, revision)
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return nil, hypervisorTaxonomy.ErrImageConflict
	}

	// [COMMENT]: Nếu không có dòng nào được chèn do Zone không hợp lệ hoặc thiếu service hypervisor
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrScopeUnavailable
	}

	return nil, fmt.Errorf("hypervisor image repository: create upload: %w", err)
}

// ListAdmin truy vấn danh sách tất cả các Image Artifact trong một Zone phục vụ SRE Admin quản trị.
func (r *ImageRepoPostgres) ListAdmin(
	ctx context.Context,
	zoneID uuid.UUID,
	limit int32,
) ([]*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		SELECT
			id,
			zone_id,
			name,
			code,
			distribution,
			release,
			revision,
			architecture,
			format,
			size_bytes,
			sha256,
			object_key,
			state,
			created_by,
			provider_template_vmid,
			error_code,
			error_message,
			created_at,
			updated_at,
			available_at
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

// ListCatalog truy vấn danh mục OS Images đang sẵn sàng (AVAILABLE) trong Zone để User chọn khi tạo VM.
func (r *ImageRepoPostgres) ListCatalog(
	ctx context.Context,
	zoneID uuid.UUID,
) ([]*hypervisorEntity.ImageArtifact, error) {
	// [COMMENT]: Chỉ trả về các image ở trạng thái AVAILABLE, đã có VM Template ID trên Proxmox,
	// và thuộc Zone đang active/draining có dịch vụ hypervisor hoạt động.
	query := fmt.Sprintf(`
		SELECT
			image.id,
			image.zone_id,
			image.name,
			image.code,
			image.distribution,
			image.release,
			image.revision,
			image.architecture,
			image.format,
			image.size_bytes,
			image.sha256,
			image.object_key,
			image.state,
			image.created_by,
			image.provider_template_vmid,
			image.error_code,
			image.error_message,
			image.created_at,
			image.updated_at,
			image.available_at
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

// GetAvailable truy vấn thông tin chi tiết của một Image Artifact đang ở trạng thái AVAILABLE trong Zone.
func (r *ImageRepoPostgres) GetAvailable(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
) (*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		SELECT
			id,
			zone_id,
			name,
			code,
			distribution,
			release,
			revision,
			architecture,
			format,
			size_bytes,
			sha256,
			object_key,
			state,
			created_by,
			provider_template_vmid,
			error_code,
			error_message,
			created_at,
			updated_at,
			available_at
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

// Get truy vấn thông tin của một Image Artifact theo ID và ZoneID (bất kể trạng thái).
func (r *ImageRepoPostgres) Get(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
) (*hypervisorEntity.ImageArtifact, error) {
	query := fmt.Sprintf(`
		SELECT
			id,
			zone_id,
			name,
			code,
			distribution,
			release,
			revision,
			architecture,
			format,
			size_bytes,
			sha256,
			object_key,
			state,
			created_by,
			provider_template_vmid,
			error_code,
			error_message,
			created_at,
			updated_at,
			available_at
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

// BeginImport thực hiện chuyển trạng thái Image sang IMPORTING và ghi Outbox record trong một CTE transaction nguyên tử.
func (r *ImageRepoPostgres) BeginImport(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
	outbox *hypervisorEntity.HypervisorOutboxRecord,
) (*hypervisorEntity.ImageArtifact, error) {
	// [COMMENT]: 1. Mã hóa phong ấn payload outbox (Sealing) với Protector
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{
		ZoneID:               outbox.ZoneID,
		SourceDomain:         "HYPERVISOR",
		JobTopic:             outbox.JobTopic,
		ResourceID:           outbox.ResourceID,
		JobVersion:           uint32(outbox.JobVersion),
		PayloadSchemaVersion: uint32(outbox.PayloadSchemaVersion),
	}, outbox.Payload)
	if err != nil {
		return nil, err
	}
	outbox.Payload = protected.Payload
	outbox.PayloadKeyID = protected.KeyID

	// [COMMENT]: 2. Giao dịch CTE nguyên tử:
	// - locked_image: Khóa dòng FOR UPDATE và kiểm tra tiền điều kiện state IN ('UPLOADING', 'FAILED', 'QUARANTINED').
	// - updated_image: Cập nhật state = 'IMPORTING', xóa lỗi cũ và cập nhật updated_at.
	// - inserted_outbox: Ghi lệnh job vào hypervisor_outbox_records để relay đưa sang Dataplane.
	query := fmt.Sprintf(`
		WITH locked_image AS (
			SELECT
				id
			FROM %s.image_artifacts
			WHERE id = $1
			  AND zone_id = $2
			  AND state IN ('UPLOADING', 'FAILED', 'QUARANTINED')
			FOR UPDATE
		),
		updated_image AS (
			UPDATE %s.image_artifacts image
			SET
				state         = 'IMPORTING',
				error_code    = NULL,
				error_message = NULL,
				updated_at    = NOW()
			FROM locked_image
			WHERE image.id = locked_image.id
			RETURNING
				image.id,
				image.zone_id,
				image.name,
				image.code,
				image.distribution,
				image.release,
				image.revision,
				image.architecture,
				image.format,
				image.size_bytes,
				image.sha256,
				image.object_key,
				image.state,
				image.created_by,
				image.provider_template_vmid,
				image.error_code,
				image.error_message,
				image.created_at,
				image.updated_at,
				image.available_at
		),
		inserted_outbox AS (
			INSERT INTO %s.hypervisor_outbox_records (
				event_id,
				zone_id,
				job_topic,
				payload,
				actor_user_id,
				status,
				job_version,
				resource_id,
				payload_schema_version,
				trace_id,
				idle,
				payload_key_id
			)
			SELECT
				$3,  -- event_id
				$4,  -- zone_id
				$5,  -- job_topic
				$6,  -- payload (sealed)
				$7,  -- actor_user_id
				$8,  -- status
				$9,  -- job_version
				$10, -- resource_id
				$11, -- payload_schema_version
				$12, -- trace_id
				$13, -- idle (seconds)
				$14  -- payload_key_id
			FROM updated_image
			RETURNING event_id
		)
		SELECT
			image.id,
			image.zone_id,
			image.name,
			image.code,
			image.distribution,
			image.release,
			image.revision,
			image.architecture,
			image.format,
			image.size_bytes,
			image.sha256,
			image.object_key,
			image.state,
			image.created_by,
			image.provider_template_vmid,
			image.error_code,
			image.error_message,
			image.created_at,
			image.updated_at,
			image.available_at
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

	// [COMMENT]: Nếu không tìm thấy dòng nào khớp điều kiện state -> Xung đột trạng thái vòng đời
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrImageStateConflict
	}
	if err != nil {
		return nil, fmt.Errorf("hypervisor image repository: begin import: %w", err)
	}

	return image, nil
}

// BeginDelete thực hiện chuyển trạng thái Image sang DELETING và ghi Outbox record xóa hạ tầng trong một CTE transaction nguyên tử.
func (r *ImageRepoPostgres) BeginDelete(
	ctx context.Context,
	imageID uuid.UUID,
	zoneID uuid.UUID,
	outbox *hypervisorEntity.HypervisorOutboxRecord,
) (*hypervisorEntity.ImageArtifact, error) {
	// [COMMENT]: 1. Mã hóa phong ấn payload outbox (Sealing) với Protector
	protected, err := r.protector.Seal(ctx, jobpayload.Metadata{
		ZoneID:               outbox.ZoneID,
		SourceDomain:         "HYPERVISOR",
		JobTopic:             outbox.JobTopic,
		ResourceID:           outbox.ResourceID,
		JobVersion:           uint32(outbox.JobVersion),
		PayloadSchemaVersion: uint32(outbox.PayloadSchemaVersion),
	}, outbox.Payload)
	if err != nil {
		return nil, err
	}
	outbox.Payload = protected.Payload
	outbox.PayloadKeyID = protected.KeyID

	// [COMMENT]: 2. Giao dịch CTE nguyên tử:
	// - locked_image: Khóa dòng FOR UPDATE và kiểm tra tiền điều kiện state IN ('AVAILABLE', 'FAILED', 'QUARANTINED').
	// - updated_image: Cập nhật state = 'DELETING', xóa lỗi cũ và cập nhật updated_at.
	// - inserted_outbox: Ghi lệnh job xóa template/storage vào hypervisor_outbox_records.
	query := fmt.Sprintf(`
		WITH locked_image AS (
			SELECT
				id
			FROM %s.image_artifacts
			WHERE id = $1
			  AND zone_id = $2
			  AND state IN ('AVAILABLE', 'FAILED', 'QUARANTINED')
			FOR UPDATE
		),
		updated_image AS (
			UPDATE %s.image_artifacts image
			SET
				state         = 'DELETING',
				error_code    = NULL,
				error_message = NULL,
				updated_at    = NOW()
			FROM locked_image
			WHERE image.id = locked_image.id
			RETURNING
				image.id,
				image.zone_id,
				image.name,
				image.code,
				image.distribution,
				image.release,
				image.revision,
				image.architecture,
				image.format,
				image.size_bytes,
				image.sha256,
				image.object_key,
				image.state,
				image.created_by,
				image.provider_template_vmid,
				image.error_code,
				image.error_message,
				image.created_at,
				image.updated_at,
				image.available_at
		),
		inserted_outbox AS (
			INSERT INTO %s.hypervisor_outbox_records (
				event_id,
				zone_id,
				job_topic,
				payload,
				actor_user_id,
				status,
				job_version,
				resource_id,
				payload_schema_version,
				trace_id,
				idle,
				payload_key_id
			)
			SELECT
				$3,  -- event_id
				$4,  -- zone_id
				$5,  -- job_topic
				$6,  -- payload (sealed)
				$7,  -- actor_user_id
				$8,  -- status
				$9,  -- job_version
				$10, -- resource_id
				$11, -- payload_schema_version
				$12, -- trace_id
				$13, -- idle (seconds)
				$14  -- payload_key_id
			FROM updated_image
			RETURNING event_id
		)
		SELECT
			image.id,
			image.zone_id,
			image.name,
			image.code,
			image.distribution,
			image.release,
			image.revision,
			image.architecture,
			image.format,
			image.size_bytes,
			image.sha256,
			image.object_key,
			image.state,
			image.created_by,
			image.provider_template_vmid,
			image.error_code,
			image.error_message,
			image.created_at,
			image.updated_at,
			image.available_at
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

	// [COMMENT]: Nếu không tìm thấy dòng nào khớp điều kiện state -> Xung đột trạng thái vòng đời
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hypervisorTaxonomy.ErrImageStateConflict
	}
	if err != nil {
		return nil, fmt.Errorf("hypervisor image repository: begin delete: %w", err)
	}

	return image, nil
}
