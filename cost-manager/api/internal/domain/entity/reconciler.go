/*
============================================================================
MAP: BILLING DOMAIN ENTITY - RECONCILER
============================================================================
CONTRACT:
1. Định nghĩa thực thể UnreconciledProjection phục vụ đối soát trạng thái sở hữu tài nguyên qua gRPC.
============================================================================
*/

package entity

import "github.com/google/uuid"

// [COMMENT]: UnreconciledProjection đại diện cho bản ghi dự phóng sở hữu cần được đối soát với Controlplane.
type UnreconciledProjection struct {
	ID         uuid.UUID
	ResourceID uuid.UUID
}
