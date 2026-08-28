package iamRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamTaxonomy "controlplane/internal/iam/taxonomy"

	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: RefreshTokenRepository quản lý việc cấp phát, phục hồi phiên và thu hồi refresh token của thiết bị
type RefreshTokenRepository struct {
	db     *pgxpool.Pool
	schema config.SchemaSQLCfg
}

// [COMMENT]: NewRefreshTokenRepository khởi tạo một thể hiện mới của RefreshTokenRepository
func NewRefreshTokenRepository(schema config.SchemaSQLCfg, db *pgxpool.Pool) iamRepoInterface.RefreshTokenRepository {
	return &RefreshTokenRepository{
		db:     db,
		schema: schema,
	}
}

// [COMMENT]: IssueDeviceRefreshToken cấp mới refresh token cho thiết bị của user trong 1 CTE nguyên tử:
// 1. active_device: Khóa và kiểm tra thiết bị còn hợp lệ (chưa bị thu hồi) và user đang ở trạng thái active.
// 2. previous_deleted: Xóa toàn bộ refresh token cũ của cặp (user_id, device_id).
// 3. delete_fence: Tạo rào chắn đảm bảo lệnh xóa đã hoàn tất trước khi chèn mới.
// 4. inserted: Chèn refresh token mới với token_hash và thời hạn hết hạn.
func (r *RefreshTokenRepository) IssueDeviceRefreshToken(ctx context.Context, in *iamEntity.IssueDeviceRefreshToken) error {
	query := fmt.Sprintf(`
		WITH active_device AS MATERIALIZED (
			SELECT device.id
			FROM %s.devices device
			JOIN %s.users account ON account.id = device.user_id 
			                    AND account.status = 'active'
			WHERE device.id = $3 
			  AND device.user_id = $2 
			  AND device.revoked_at IS NULL
			FOR UPDATE OF device
		),
		previous_deleted AS (
			DELETE FROM %s.refresh_tokens token
			USING active_device
			WHERE token.user_id = $2 
			  AND token.device_id = active_device.id
			RETURNING token.id
		),
		delete_fence AS MATERIALIZED (
			SELECT COUNT(*) AS deleted_count 
			FROM previous_deleted
		),
		inserted AS (
			INSERT INTO %s.refresh_tokens (
				id, 
				user_id, 
				device_id, 
				token_hash, 
				issued_at, 
				expires_at
			)
			SELECT 
				$1, 
				$2, 
				active_device.id, 
				$4, 
				$5, 
				$6
			FROM active_device 
			CROSS JOIN delete_fence
			RETURNING id
		)
		SELECT 
			EXISTS (SELECT 1 FROM active_device), 
			EXISTS (SELECT 1 FROM inserted)
	`, r.schema.IAM, r.schema.IAM, r.schema.IAM, r.schema.IAM)

	var deviceValid, inserted bool
	if err := r.db.QueryRow(ctx, query,
		in.ID, 
		in.UserID, 
		in.DeviceID, 
		in.TokenHash, 
		in.IssuedAt, 
		in.ExpiresAt,
	).Scan(&deviceValid, &inserted); err != nil {
		return fmt.Errorf("refresh token repo: issue device credential: %w", err)
	}
	if !deviceValid {
		return iamTaxonomy.ErrNotFound
	}
	if !inserted {
		return iamTaxonomy.ErrConflict
	}
	return nil
}

// [COMMENT]: RecoverUserSession phục hồi phiên làm việc của user từ refresh token trong 1 RTT CTE:
// 1. credential: Xác thực token_hash hợp lệ, chưa hết hạn, và thiết bị gắn kết vẫn đang hoạt động (revoked_at IS NULL).
// 2. platform_authority: Lấy quyền và role_level cao nhất ở platform scope (workspace nil UUID).
// 3. tenant_authority: Nếu requestedTenantID được truyền vào, kiểm tra tư cách thành viên tenant và lấy tenant role_level tương ứng.
// 4. SELECT ngoài: Tổng hợp trạng thái hợp lệ của credential, thẩm quyền (context_authorized), và thông tin user/device/tenant.
func (r *RefreshTokenRepository) RecoverUserSession(ctx context.Context, in *iamEntity.RecoverUserSession) (*iamEntity.RecoverUserSession, error) {
	query := fmt.Sprintf(`
		WITH credential AS MATERIALIZED (
			SELECT 
				token.user_id, 
				token.device_id,
				COALESCE(device.client_device_id, device.id)::text AS client_device_id,
				account.username
			FROM %s.refresh_tokens token
			JOIN %s.users account   ON account.id = token.user_id 
			                       AND account.status = 'active'
			JOIN %s.devices device  ON device.id = token.device_id 
			                       AND device.user_id = token.user_id 
			                       AND device.revoked_at IS NULL
			WHERE token.token_hash = $1 
			  AND token.expires_at > $3
		),
		platform_authority AS MATERIALIZED (
			SELECT assignment.role_level
			FROM credential
			JOIN %s.user_role assignment ON assignment.user_id = credential.user_id 
			                            AND assignment.workspace_id = '00000000-0000-0000-0000-000000000000'
			JOIN %s.platform_roles role  ON role.id = assignment.role_id 
			                            AND role.version = assignment.role_version
			ORDER BY assignment.role_level ASC, assignment.role_id ASC
			LIMIT 1
		),
		tenant_authority AS MATERIALIZED (
			SELECT assignment.role_level
			FROM credential
			JOIN %s.tenant_memberships membership ON membership.user_id = credential.user_id 
			                                     AND membership.tenant_id = $2::uuid 
			                                     AND membership.status = 'active'
			JOIN %s.tenants tenant                ON tenant.id = membership.tenant_id 
			                                     AND tenant.status = 'active'
			JOIN %s.membership_role assignment    ON assignment.membership_id = membership.id 
			                                     AND assignment.workspace_id = '00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_roles role             ON role.id = assignment.tenant_role_id 
			                                     AND role.tenant_id = membership.tenant_id 
			                                     AND role.version = assignment.role_version
			WHERE $2::uuid IS NOT NULL
			ORDER BY assignment.role_level ASC, assignment.tenant_role_id ASC
			LIMIT 1
		)
		SELECT 
			EXISTS (SELECT 1 FROM credential),
			CASE 
				WHEN $2::uuid IS NULL THEN EXISTS (SELECT 1 FROM platform_authority)
				ELSE EXISTS (SELECT 1 FROM tenant_authority)
			END,
			$2::uuid IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM tenant_authority)
			  AND EXISTS (SELECT 1 FROM platform_authority),
			COALESCE((SELECT user_id FROM credential), '00000000-0000-0000-0000-000000000000'::uuid),
			COALESCE((SELECT device_id FROM credential), '00000000-0000-0000-0000-000000000000'::uuid),
			COALESCE((SELECT client_device_id FROM credential), ''),
			COALESCE((SELECT username FROM credential), ''),
			CASE 
				WHEN EXISTS (SELECT 1 FROM tenant_authority) THEN $2::uuid 
				ELSE NULL::uuid 
			END,
			COALESCE(
				(SELECT role_level FROM tenant_authority),
				(SELECT role_level FROM platform_authority),
				0
			)
	`, r.schema.IAM, r.schema.IAM, r.schema.IAM, r.schema.IAM, r.schema.IAM,
		r.schema.Hierarchy, r.schema.Hierarchy, r.schema.IAM, r.schema.IAM)

	out := &iamEntity.RecoverUserSession{
		TokenHash:         in.TokenHash,
		RequestedTenantID: in.RequestedTenantID,
		Now:               in.Now,
	}
	if err := r.db.QueryRow(ctx, query, in.TokenHash, in.RequestedTenantID, in.Now).Scan(
		&out.CredentialValid,
		&out.ContextAuthorized,
		&out.PersonalFallbackAuthorized,
		&out.UserID,
		&out.DeviceID,
		&out.ClientDeviceID,
		&out.Username,
		&out.ResolvedTenantID,
		&out.RoleLevel,
	); err != nil {
		return nil, fmt.Errorf("refresh token repo: recover user session: %w", err)
	}
	if !out.CredentialValid {
		return out, iamTaxonomy.ErrInvalidCredential
	}
	if !out.ContextAuthorized {
		return out, iamTaxonomy.ErrActionNotAllowed
	}
	return out, nil
}

// [COMMENT]: DeleteByHash thu hồi trực tiếp refresh token dựa trên token_hash
func (r *RefreshTokenRepository) DeleteByHash(ctx context.Context, tokenHash string) (int64, error) {
	query := fmt.Sprintf(`
		DELETE FROM %s.refresh_tokens 
		WHERE token_hash = $1
	`, r.schema.IAM)

	result, err := r.db.Exec(ctx, query, tokenHash)
	if err != nil {
		return 0, fmt.Errorf("refresh token repo: delete credential: %w", err)
	}
	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return 0, iamTaxonomy.ErrNotFound
	}
	return rowsAffected, nil
}
