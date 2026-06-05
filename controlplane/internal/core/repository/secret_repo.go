package coreRepoImpl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreModel "controlplane/internal/core/model"
	"controlplane/pkg/id"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// secretBootstrapLockNamespace định nghĩa namespace riêng biệt cho PostgreSQL Advisory Lock dùng trong quá trình Bootstrap.
// Việc cô lập namespace đảm bảo không xung đột với các tiến trình khóa khác trong cùng database.
const secretBootstrapLockNamespace int32 = 20260512

// secretRotationLockNamespace định nghĩa namespace riêng biệt cho PostgreSQL Advisory Lock dùng trong quá trình Rotate Secret.
const secretRotationLockNamespace int32 = 20260513

// SecretRepository triển khai repoInterface.SecretRepository sử dụng pgxpool làm driver persistence.
// Triển khai cô lập theo schema động trong môi trường Cloud Native để tăng tính độc lập dữ liệu.
type SecretRepository struct {
	db     *pgxpool.Pool
	schema string
}

// secretAdvisoryLock giữ connection transaction-level phục vụ việc giải phóng khóa phân tán PostgreSQL.
// Nhờ sử dụng transaction-scoped advisory lock, nếu tiến trình/node bị crash đột ngột,
// PostgreSQL sẽ tự động rollback transaction và giải phóng lock lập tức, tránh lỗi lock leak trong hệ thống HA.
type secretAdvisoryLock struct {
	conn *pgxpool.Conn
	key1 int32 // Namespace ID của lock
	key2 int32 // CRC32 Hash ID của Family Code để khóa cụ thể từng secret family
}

// NewSecretRepository khởi tạo đối tượng SecretRepository mới.
// CONTRACT: Yêu cầu pgxpool hoạt động và SchemaSQL được cấu hình chính xác từ file config.
// BOUNDARY: Module Core quản lý vòng đời và lưu trữ của các Secrets thuộc core domain.
func NewSecretRepository(cfg *config.Config,
	db *pgxpool.Pool) coreRepoInterface.SecretRepository {
	return &SecretRepository{
		db:     db,
		schema: cfg.SchemaSQL.Core,
	}
}

// AcquireFamilyBootstrapLock thiết lập khóa phân tán mức Transaction để khởi tạo (bootstrap) Secret Family một cách độc quyền.
// Đảm bảo không xảy ra race condition khi nhiều node HA cùng khởi tạo một Secret Family đồng thời.
// Operational Details:
// - Case nào xảy ra: Khi bắt đầu tiến trình khởi tạo cấu hình secret family.
// - Xử lý cái gì: Đảm bảo mutual exclusion trên một familyCode xác định.
// - Xử lý bằng cách nào: Acquire kết nối độc quyền, bắt đầu transaction và gọi pg_advisory_xact_lock.
func (r *SecretRepository) AcquireFamilyBootstrapLock(ctx context.Context, familyCode string) (coreRepoInterface.SecretBootstrapLock, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		conn.Release()
		return nil, err
	}
	// Băm familyCode thành số int32 bằng CRC32 để truyền vào PostgreSQL Advisory Lock.
	// TrimSpace để loại bỏ các khoảng trắng thừa giúp tránh băm sai lệch.
	key2 := int32(id.CRC32String(strings.TrimSpace(familyCode)))
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, secretBootstrapLockNamespace, key2); err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		conn.Release()
		return nil, err
	}
	return &secretAdvisoryLock{conn: conn, key1: secretBootstrapLockNamespace, key2: key2}, nil
}

// Release giải phóng Advisory Lock bằng cách rollback transaction hiện tại.
// Trong PostgreSQL, transaction-level advisory locks được tự động giải phóng khi kết thúc transaction (rollback/commit).
func (l *secretAdvisoryLock) Release(ctx context.Context) error {
	if l == nil || l.conn == nil {
		return nil
	}
	_, err := l.conn.Exec(ctx, "ROLLBACK")
	l.conn.Release()
	l.conn = nil
	return err
}

// AcquireFamilyRotationLock thiết lập khóa phân tán mức Transaction để xoay vòng (rotation) Secret Family một cách độc quyền.
// Ngăn ngừa rủi ro race condition khi hai tiến trình/node cùng lúc sinh ra các phiên bản secret mới và tranh chấp primary key.
// Operational Details:
// - Case nào xảy ra: Khi có yêu cầu xoay vòng phiên bản (rotate version) của secret family.
// - Xử lý cái gì: Đảm bảo chỉ một node có quyền thay đổi các phiên bản và promote primary version.
// - Xử lý bằng cách nào: pg_advisory_xact_lock mức transaction sử dụng rotation namespace (20260513).
func (r *SecretRepository) AcquireFamilyRotationLock(ctx context.Context, familyCode string) (coreRepoInterface.SecretRotationLock, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		conn.Release()
		return nil, err
	}
	key2 := int32(id.CRC32String(strings.TrimSpace(familyCode)))
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, secretRotationLockNamespace, key2); err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		conn.Release()
		return nil, err
	}
	return &secretAdvisoryLock{conn: conn, key1: secretRotationLockNamespace, key2: key2}, nil
}

// GetFamilyByCode truy vấn thông tin SecretFamily dựa trên mã định danh duy nhất (code).
// Sử dụng parameterized query để tránh hoàn toàn lỗ hổng SQL Injection.
func (r *SecretRepository) GetFamilyByCode(ctx context.Context, code string) (*coreEntity.SecretFamily, error) {
	query := fmt.Sprintf(`SELECT id, code, name, description, created_at FROM %s.core_secret_families WHERE code = $1`, r.schema)
	var row coreModel.SecretFamily
	if err := r.db.QueryRow(ctx, query, strings.TrimSpace(code)).Scan(&row.ID, &row.Code, &row.Name, &row.Description, &row.CreatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // Trả về nil nếu không tồn tại bản ghi nào thỏa mãn điều kiện
		}
		return nil, err
	}
	entityValue := coreModel.SecretFamilyModelToEntity(row)
	return &entityValue, nil
}

// EnsureFamily đảm bảo sự tồn tại của SecretFamily trong Database.
// Áp dụng kỹ thuật "ON CONFLICT (code) DO NOTHING" để xử lý race condition do trùng lắp chèn song song trên môi trường HA.
func (r *SecretRepository) EnsureFamily(ctx context.Context, family coreEntity.SecretFamily) (*coreEntity.SecretFamily, error) {
	query := fmt.Sprintf(`INSERT INTO %s.core_secret_families (id, code, name, description, created_at) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (code) DO NOTHING`, r.schema)
	value := coreModel.SecretFamilyEntityToModel(family)
	if _, err := r.db.Exec(ctx, query, value.ID, value.Code, value.Name, value.Description, value.CreatedAt); err != nil {
		return nil, err
	}
	return r.GetFamilyByCode(ctx, family.Code)
}

// ListVersionsByFamilyID trả về danh sách các phiên bản secret thuộc một family cụ thể, sắp xếp theo số phiên bản giảm dần.
// Việc sắp xếp giảm dần giúp các consumer dễ dàng lấy được phiên bản mới nhất làm ưu tiên số một.
func (r *SecretRepository) ListVersionsByFamilyID(ctx context.Context, familyID string) ([]coreEntity.SecretVersion, error) {
	query := fmt.Sprintf(`SELECT id, family_id, version, secret_ciphertext, secret_fingerprint, status, is_primary, not_before, not_after, activated_at, retired_at, revoked_at, rotation_reason, created_at, updated_at FROM %s.core_secret_versions WHERE family_id = $1 ORDER BY version DESC`, r.schema)
	rows, err := r.db.Query(ctx, query, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // Giải phóng connection của rows tránh block hoặc rò rỉ connection pool

	result := make([]coreEntity.SecretVersion, 0)
	for rows.Next() {
		var item coreModel.SecretVersion
		if err := rows.Scan(&item.ID, &item.FamilyID, &item.Version, &item.SecretCiphertext, &item.SecretFingerprint, &item.Status, &item.IsPrimary, &item.NotBefore, &item.NotAfter, &item.ActivatedAt, &item.RetiredAt, &item.RevokedAt, &item.RotationReason, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, coreModel.SecretVersionModelToEntity(item))
	}
	return result, rows.Err()
}

// CreateSecretVersion thêm một phiên bản secret mới vào DB mà chưa kích hoạt nó làm Primary.
// ciphertext lưu trữ trong DB được mã hóa an toàn ở mức ứng dụng trước khi lưu vào persistence layer.
func (r *SecretRepository) CreateSecretVersion(ctx context.Context, version coreEntity.SecretVersion) error {
	query := fmt.Sprintf(`INSERT INTO %s.core_secret_versions (id, family_id, version, secret_ciphertext, secret_fingerprint, status, is_primary, not_before, not_after, activated_at, retired_at, revoked_at, rotation_reason, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, r.schema)
	value := coreModel.SecretVersionEntityToModel(version)
	_, err := r.db.Exec(ctx, query, value.ID, value.FamilyID, value.Version, value.SecretCiphertext, value.SecretFingerprint, value.Status, value.IsPrimary, value.NotBefore, value.NotAfter, value.ActivatedAt, value.RetiredAt, value.RevokedAt, value.RotationReason, value.CreatedAt, value.UpdatedAt)
	return err
}

// ReplacePrimaryVersion hoán đổi phiên bản Primary hiện tại sang một phiên bản được chỉ định.
// Ràng buộc tất cả các cập nhật trạng thái Primary trong một Database Transaction duy nhất để bảo đảm tính nhất quán nguyên tử (Atomicity).
func (r *SecretRepository) ReplacePrimaryVersion(ctx context.Context, familyID string, nextVersionID string, previousVersionID string, now time.Time) error {
	// B1: Khởi tạo database transaction và đăng ký trì hoãn rollback nếu lỗi xảy ra.
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// B2: Hạ cờ is_primary của bất kỳ version nào thuộc family này mà không phải là version mới sắp promote.
	clearQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET is_primary = false, updated_at = $2 WHERE family_id = $1 AND id <> $3 AND is_primary = true`, r.schema)
	if _, err := tx.Exec(ctx, clearQuery, familyID, now.UTC(), nextVersionID); err != nil {
		return err
	}

	// B3: Đặt trạng thái của version mới thành Active và bật cờ is_primary = true.
	promoteQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET status = $2, is_primary = true, activated_at = COALESCE(activated_at, $3), updated_at = $3 WHERE id = $1`, r.schema)
	if _, err := tx.Exec(ctx, promoteQuery, nextVersionID, coreEntity.SecretStatusActive, now.UTC()); err != nil {
		return err
	}

	// B4: Nếu có previousVersionID cụ thể, hạ cờ is_primary của version cũ đó để đảm bảo tính an toàn dữ liệu kép.
	if previousVersionID != "" {
		demoteQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET is_primary = false, updated_at = $2 WHERE id = $1`, r.schema)
		if _, err := tx.Exec(ctx, demoteQuery, previousVersionID, now.UTC()); err != nil {
			return err
		}
	}

	// B5: Commit transaction để lưu trữ thay đổi atomically.
	return tx.Commit(ctx)
}

// RetireVersion chuyển đổi trạng thái của secret version sang Retired (đã thu hồi) và huỷ bỏ cờ primary.
// Việc thu hồi này chỉ tắt cờ sử dụng nhưng giữ nguyên bản ghi phục vụ cho mục đích kiểm toán sau này.
func (r *SecretRepository) RetireVersion(ctx context.Context, versionID string, retiredAt time.Time) error {
	query := fmt.Sprintf(`UPDATE %s.core_secret_versions SET status = $2, is_primary = false, retired_at = $3, updated_at = $3 WHERE id = $1`, r.schema)
	_, err := r.db.Exec(ctx, query, versionID, coreEntity.SecretStatusRetired, retiredAt.UTC())
	return err
}

// DeleteVersion xóa vĩnh viễn một bản ghi secret version khỏi cơ sở dữ liệu.
func (r *SecretRepository) DeleteVersion(ctx context.Context, versionID string) error {
	query := fmt.Sprintf(`DELETE FROM %s.core_secret_versions WHERE id = $1`, r.schema)
	_, err := r.db.Exec(ctx, query, versionID)
	return err
}

// QualifiedTable tạo tên bảng đầy đủ gồm tên schema SQL của module core kết hợp tên bảng.
// Giúp cô lập dữ liệu hiệu quả trong môi trường multi-schema.
func (r *SecretRepository) QualifiedTable(table string) string {
	return fmt.Sprintf("%s.%s", r.schema, strings.TrimSpace(table))
}

// RotateFamilyVersions thực hiện nghiệp vụ xoay vòng các phiên bản secret của một family trong một transaction duy nhất.
// Hoạt động này đảm bảo tính bền vững của dữ liệu và triệt tiêu mọi khả năng xảy ra race condition giữa việc thêm mới, kích hoạt và xóa phiên bản cũ.
func (r *SecretRepository) RotateFamilyVersions(ctx context.Context, familyID string, nextVersion coreEntity.SecretVersion, previousPrimaryID string, oldestVersionID string, retirePreviousNow bool, now time.Time) error {
	// B1: Khởi tạo database transaction và trì hoãn rollback khi có lỗi phát sinh.
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// B2: Xóa phiên bản cũ nhất (oldestVersionID) nếu được truyền vào để dọn dẹp dung lượng.
	if strings.TrimSpace(oldestVersionID) != "" {
		deleteQuery := fmt.Sprintf(`DELETE FROM %s.core_secret_versions WHERE id = $1`, r.schema)
		if _, err := tx.Exec(ctx, deleteQuery, oldestVersionID); err != nil {
			return err
		}
	}

	// B3: Reset toàn bộ cờ is_primary của các phiên bản hiện tại về false nhằm tránh tình trạng đa phiên bản primary.
	clearQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET is_primary = false, updated_at = $2 WHERE family_id = $1 AND is_primary = true`, r.schema)
	if _, err := tx.Exec(ctx, clearQuery, familyID, now.UTC()); err != nil {
		return err
	}

	// B4: Tạo mới phiên bản kế tiếp (nextVersion) với cờ is_primary = false.
	insertQuery := fmt.Sprintf(`INSERT INTO %s.core_secret_versions (id, family_id, version, secret_ciphertext, secret_fingerprint, status, is_primary, not_before, not_after, activated_at, retired_at, revoked_at, rotation_reason, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, r.schema)
	value := coreModel.SecretVersionEntityToModel(nextVersion)
	if _, err := tx.Exec(ctx, insertQuery, value.ID, value.FamilyID, value.Version, value.SecretCiphertext, value.SecretFingerprint, value.Status, false, value.NotBefore, value.NotAfter, value.ActivatedAt, value.RetiredAt, value.RevokedAt, value.RotationReason, value.CreatedAt, value.UpdatedAt); err != nil {
		return err
	}

	// B5: Đặt cờ is_primary = true cho phiên bản kế tiếp vừa thêm và đánh dấu Active.
	promoteQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET status = $2, is_primary = true, activated_at = COALESCE(activated_at, $3), updated_at = $3 WHERE id = $1`, r.schema)
	if _, err := tx.Exec(ctx, promoteQuery, nextVersion.ID, coreEntity.SecretStatusActive, now.UTC()); err != nil {
		return err
	}

	// B6: Nếu có previousPrimaryID, hạ cờ is_primary của nó. Chuyển trạng thái sang Retired nếu có cờ retirePreviousNow.
	if strings.TrimSpace(previousPrimaryID) != "" {
		demoteQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET is_primary = false, updated_at = $2 WHERE id = $1`, r.schema)
		if _, err := tx.Exec(ctx, demoteQuery, previousPrimaryID, now.UTC()); err != nil {
			return err
		}
		if retirePreviousNow {
			retireQuery := fmt.Sprintf(`UPDATE %s.core_secret_versions SET status = $2, is_primary = false, retired_at = $3, updated_at = $3 WHERE id = $1`, r.schema)
			if _, err := tx.Exec(ctx, retireQuery, previousPrimaryID, coreEntity.SecretStatusRetired, now.UTC()); err != nil {
				return err
			}
		}
	}

	// B7: Commit transaction atomically.
	return tx.Commit(ctx)
}
