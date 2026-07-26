package repository

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type tierRepository struct {
	db *pgxpool.Pool
}

// [COMMENT]: NewTierRepository khởi tạo instance của tierRepository.
func NewTierRepository(db *pgxpool.Pool) billingRepoInterface.TierRepository {
	return &tierRepository{db: db}
}

// [COMMENT]: ListTiers đọc version có hiệu lực thành flat rows, tránh N+1 và không chạm legacy mutable ranges.
func (r *tierRepository) ListTiers(ctx context.Context, page, limit int, serviceType entity.ServiceType, search string) ([]*entity.Tier, int64, error) {
	offset := (page - 1) * limit
	searchPattern := ""
	if search != "" {
		searchPattern = "%" + search + "%"
	}

	// 1. Tính tổng số dòng cước chi tiết khớp bộ lọc phục vụ phân trang ở Client
	countQuery := `
		SELECT count(*)
		FROM billing.tiers t
		JOIN LATERAL (
			SELECT v.id
			FROM billing.tier_versions v
			WHERE v.tier_id = t.id
			  AND v.status <> 'CANCELLED'
			  AND v.effective_from <= NOW()
			  AND (v.effective_to IS NULL OR NOW() < v.effective_to)
			ORDER BY v.version_number DESC
			LIMIT 1
		) v ON TRUE
		JOIN billing.tier_version_ranges r ON r.tier_version_id = v.id
		WHERE ($1 = '' OR t.service_type = $1::billing.service_type)
		  AND ($2 = '' OR t.name ILIKE $3 OR t.code ILIKE $3)
	`
	var total int64
	err := r.db.QueryRow(ctx, countQuery, string(serviceType), search, searchPattern).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("tier repo: count flat tiers: %w", err)
	}

	if total == 0 {
		return []*entity.Tier{}, 0, nil
	}

	// 2. Query 1 câu SQL JOIN duy nhất để lấy thông tin chi tiết từng dòng cước phẳng
	flatQuery := `
		SELECT 
			r.id, 
			t.id,
			t.name, 
			t.code, 
			t.service_type, 
			t.metadata_version,
			v.version_number,
			r.range_start, 
			r.range_end, 
			r.base_unit_price, 
			r.created_at, 
			t.updated_at
		FROM billing.tiers t
		JOIN LATERAL (
			SELECT candidate.id, candidate.version_number
			FROM billing.tier_versions candidate
			WHERE candidate.tier_id = t.id
			  AND candidate.status <> 'CANCELLED'
			  AND candidate.effective_from <= NOW()
			  AND (candidate.effective_to IS NULL OR NOW() < candidate.effective_to)
			ORDER BY candidate.version_number DESC
			LIMIT 1
		) v ON TRUE
		JOIN billing.tier_version_ranges r ON r.tier_version_id = v.id
		WHERE ($1 = '' OR t.service_type = $1::billing.service_type)
		  AND ($2 = '' OR t.name ILIKE $3 OR t.code ILIKE $3)
		ORDER BY t.created_at DESC, r.range_start ASC
		LIMIT $4 OFFSET $5
	`
	rows, err := r.db.Query(ctx, flatQuery, string(serviceType), search, searchPattern, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("tier repo: query flat tiers: %w", err)
	}
	defer rows.Close()

	tiers := make([]*entity.Tier, 0)
	for rows.Next() {
		var t entity.Tier
		var rawServiceType string
		// Scan trực tiếp dữ liệu dạng native từ các cột DB
		err := rows.Scan(
			&t.ID,
			&t.TierID,
			&t.Name,
			&t.Code,
			&rawServiceType,
			&t.MetadataVersion,
			&t.PricingVersion,
			&t.RangeStart,
			&t.RangeEnd,
			&t.BaseUnitPrice,
			&t.CreatedAt,
			&t.UpdatedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("tier repo: scan flat tier: %w", err)
		}
		t.ServiceType = entity.ServiceType(rawServiceType)
		tiers = append(tiers, &t)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("tier repo: rows flat tiers iteration: %w", err)
	}

	return tiers, total, nil
}

// GetTierDetail dùng latest version CTE để UI luôn nhận đủ ranges từ cùng một immutable snapshot.
func (r *tierRepository) GetTierDetail(ctx context.Context, code string, serviceType entity.ServiceType) (*entity.TierDetail, error) {
	rows, err := r.db.Query(ctx, `
		WITH selected_tier AS (
			SELECT id, name, code, service_type, metadata_version
			FROM billing.tiers
			WHERE code = $1 AND service_type = $2::billing.service_type
		), latest_version AS (
			SELECT v.*
			FROM billing.tier_versions v
			JOIN selected_tier t ON t.id = v.tier_id
			WHERE v.status <> 'CANCELLED'
			ORDER BY v.version_number DESC
			LIMIT 1
		)
		SELECT t.id, t.name, t.code, t.service_type, t.metadata_version,
		       v.id, v.version_number, v.status, v.effective_from, v.effective_to, v.checksum,
		       r.id, r.range_start, r.range_end, r.base_unit_price
		FROM selected_tier t
		JOIN latest_version v ON v.tier_id = t.id
		JOIN billing.tier_version_ranges r ON r.tier_version_id = v.id
		ORDER BY r.range_start
	`, code, string(serviceType))
	if err != nil {
		return nil, fmt.Errorf("tier repo: query tier detail: %w", err)
	}
	defer rows.Close()

	var detail *entity.TierDetail
	for rows.Next() {
		var tierID, versionID uuid.UUID
		var name, rowCode, rowServiceType, status, checksum string
		var metadataVersion, versionNumber int
		var effectiveFrom time.Time
		var effectiveTo *time.Time
		var tierRange entity.TierRangeInput
		if err = rows.Scan(
			&tierID, &name, &rowCode, &rowServiceType, &metadataVersion,
			&versionID, &versionNumber, &status, &effectiveFrom, &effectiveTo, &checksum,
			&tierRange.ID, &tierRange.RangeStart, &tierRange.RangeEnd, &tierRange.BaseUnitPrice,
		); err != nil {
			return nil, fmt.Errorf("tier repo: scan tier detail row: %w", err)
		}
		if detail == nil {
			detail = &entity.TierDetail{
				ID: tierID, Name: name, Code: rowCode, ServiceType: entity.ServiceType(rowServiceType), MetadataVersion: metadataVersion,
				LatestVersion: entity.TierVersion{
					ID: versionID, TierID: tierID, VersionNumber: versionNumber, Status: status,
					EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo, Checksum: checksum,
				},
			}
		}
		detail.LatestVersion.Ranges = append(detail.LatestVersion.Ranges, tierRange)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("tier repo: iterate tier detail: %w", err)
	}
	if detail == nil {
		return nil, billingTaxonomy.ErrTierNotFound
	}
	return detail, nil
}

// GetActivePricingSnapshot chọn một snapshot effective hiện tại bằng cùng boundary mà estimate cần dùng.
// Tiers/ranges được đọc trong một query; không ghép các version khác nhau ở application layer.
func (r *tierRepository) GetActivePricingSnapshot(ctx context.Context, serviceType entity.ServiceType) (*entity.PricingSnapshot, error) {
	rows, err := r.db.Query(ctx, `
		WITH candidate AS (
			SELECT t.id AS tier_id, t.code, t.service_type,
			       v.id AS tier_version_id, v.version_number,
			       v.effective_from, v.effective_to, v.checksum
			FROM billing.tiers t
			JOIN billing.tier_versions v ON v.tier_id = t.id
			WHERE t.service_type = $1::billing.service_type
			  AND v.status <> 'CANCELLED'
			  AND v.effective_from <= NOW()
			  AND (v.effective_to IS NULL OR NOW() < v.effective_to)
			ORDER BY v.version_number DESC, v.effective_from DESC, t.code ASC
			LIMIT 1
		)
		SELECT c.tier_id, c.code, c.service_type, c.tier_version_id, c.version_number,
		       c.effective_from, c.effective_to, c.checksum,
		       r.id, r.range_start, r.range_end, r.base_unit_price
		FROM candidate c
		JOIN billing.tier_version_ranges r ON r.tier_version_id = c.tier_version_id
		ORDER BY r.range_start ASC
	`, string(serviceType))
	if err != nil {
		return nil, fmt.Errorf("tier repo: query active pricing snapshot: %w", err)
	}
	defer rows.Close()

	var snapshot *entity.PricingSnapshot
	for rows.Next() {
		var tierID, versionID, rangeID uuid.UUID
		var code, rawServiceType, checksum string
		var versionNumber int
		var effectiveFrom time.Time
		var effectiveTo *time.Time
		var tierRange entity.TierRangeInput
		if err := rows.Scan(
			&tierID, &code, &rawServiceType, &versionID, &versionNumber,
			&effectiveFrom, &effectiveTo, &checksum,
			&rangeID, &tierRange.RangeStart, &tierRange.RangeEnd, &tierRange.BaseUnitPrice,
		); err != nil {
			return nil, fmt.Errorf("tier repo: scan active pricing snapshot: %w", err)
		}
		tierRange.ID = rangeID
		if snapshot == nil {
			snapshot = &entity.PricingSnapshot{
				TierID: tierID, TierVersionID: versionID, Code: code,
				ServiceType: entity.ServiceType(rawServiceType), VersionNumber: versionNumber,
				EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo, Checksum: checksum,
			}
		}
		snapshot.Ranges = append(snapshot.Ranges, tierRange)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tier repo: iterate active pricing snapshot: %w", err)
	}
	if snapshot == nil {
		return nil, billingTaxonomy.ErrTierNotFound
	}
	snapshot.Currency = "USD"
	return snapshot, nil
}

// UpdateTierMetadata khóa identity row và chỉ thay name/metadata_version.
func (r *tierRepository) UpdateTierMetadata(ctx context.Context, update entity.TierMetadataUpdate) (*entity.TierMetadata, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("tier repo: begin metadata transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var tierID uuid.UUID
	var currentMetadataVersion int
	err = tx.QueryRow(ctx, `
		SELECT id, metadata_version
		FROM billing.tiers
		WHERE code = $1 AND service_type = $2::billing.service_type
		FOR UPDATE
	`, update.Code, string(update.ServiceType)).Scan(&tierID, &currentMetadataVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrTierNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tier repo: lock tier: %w", err)
	}
	if currentMetadataVersion != update.MetadataVersion {
		return nil, billingTaxonomy.ErrTierMetadataConflict
	}

	var nextMetadataVersion int
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE billing.tiers
		SET name = $1, metadata_version = metadata_version + 1, updated_at = NOW()
		WHERE id = $2
		RETURNING metadata_version, updated_at
	`, update.Name, tierID).Scan(&nextMetadataVersion, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("tier repo: update tier metadata: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tier repo: commit metadata transaction: %w", err)
	}
	return &entity.TierMetadata{
		ID: tierID, Code: update.Code, ServiceType: update.ServiceType,
		MetadataVersion: nextMetadataVersion, Name: update.Name, UpdatedAt: updatedAt,
	}, nil
}

// CreateTierVersion append immutable version/ranges và outbox trong một commit duy nhất.
func (r *tierRepository) CreateTierVersion(ctx context.Context, create entity.TierVersionCreate) (*entity.TierVersion, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("tier repo: begin pricing version transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var tierID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM billing.tiers
		WHERE code = $1 AND service_type = $2::billing.service_type
		FOR UPDATE
	`, create.Code, string(create.ServiceType)).Scan(&tierID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrTierNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tier repo: lock tier for pricing version: %w", err)
	}

	var latestVersion int
	var latestEffectiveFrom time.Time
	err = tx.QueryRow(ctx, `
		SELECT version_number, effective_from
		FROM billing.tier_versions
		WHERE tier_id = $1 AND status <> 'CANCELLED'
		ORDER BY version_number DESC
		LIMIT 1
	`, tierID).Scan(&latestVersion, &latestEffectiveFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		latestVersion = 0
	} else if err != nil {
		return nil, fmt.Errorf("tier repo: read latest pricing version: %w", err)
	}
	if latestVersion != create.ExpectedLatestVersion {
		return nil, billingTaxonomy.ErrTierVersionConflict
	}
	if !create.EffectiveFrom.After(latestEffectiveFrom) {
		return nil, billingTaxonomy.ErrTierEffectiveConflict
	}

	// Khép effective window cũ; pricing content của version cũ vẫn bất biến.
	if _, err = tx.Exec(ctx, `
		UPDATE billing.tier_versions
		SET effective_to = $1
		WHERE tier_id = $2 AND version_number = $3 AND effective_to IS NULL
	`, create.EffectiveFrom, tierID, latestVersion); err != nil {
		return nil, fmt.Errorf("tier repo: close previous effective window: %w", err)
	}

	versionID := uuid.New()
	nextVersion := latestVersion + 1
	status := "SCHEDULED"
	if !create.EffectiveFrom.After(time.Now().UTC()) {
		status = "ACTIVE"
		if _, err = tx.Exec(ctx, `
			UPDATE billing.tier_versions SET status = 'SUPERSEDED'
			WHERE tier_id = $1 AND version_number = $2 AND status = 'ACTIVE'
		`, tierID, latestVersion); err != nil {
			return nil, fmt.Errorf("tier repo: supersede previous pricing version: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO billing.tier_versions
			(id, tier_id, version_number, status, effective_from, checksum, change_reason, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, versionID, tierID, nextVersion, status, create.EffectiveFrom, create.Checksum, create.ChangeReason, create.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("tier repo: insert pricing version: %w", err)
	}

	for i := range create.Ranges {
		create.Ranges[i].ID = uuid.New()
		tierRange := create.Ranges[i]
		_, err = tx.Exec(ctx, `
			INSERT INTO billing.tier_version_ranges
				(id, tier_version_id, range_start, range_end, base_unit_price)
			VALUES ($1, $2, $3, $4, $5)
		`, tierRange.ID, versionID, tierRange.RangeStart, tierRange.RangeEnd, tierRange.BaseUnitPrice)
		if err != nil {
			return nil, fmt.Errorf("tier repo: insert pricing version range: %w", err)
		}
	}

	// Outbox row commit cùng aggregate để không có version đã publish nhưng mất notification.
	_, err = tx.Exec(ctx, `
		INSERT INTO billing.pricing_outbox
			(id, event_type, tier_id, tier_version_id, version_number, service_type, effective_from, checksum)
		VALUES ($1, 'TIER_VERSION_PUBLISHED', $2, $3, $4, $5::billing.service_type, $6, $7)
	`, uuid.New(), tierID, versionID, nextVersion, string(create.ServiceType), create.EffectiveFrom, create.Checksum)
	if err != nil {
		return nil, fmt.Errorf("tier repo: insert pricing outbox: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tier repo: commit pricing version transaction: %w", err)
	}
	return &entity.TierVersion{
		ID:            versionID,
		TierID:        tierID,
		VersionNumber: nextVersion,
		Status:        status,
		EffectiveFrom: create.EffectiveFrom,
		Checksum:      create.Checksum,
		Ranges:        create.Ranges,
	}, nil
}
