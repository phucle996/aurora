/*
============================================================================
MAP: BILLING DOMAIN ENTITY - PRICING OUTBOX ROW
============================================================================
CONTRACT:
1. Định nghĩa thực thể PricingOutboxRow đại diện cho bản ghi Transactional Outbox phát sự kiện thay đổi bảng giá.
============================================================================
*/

package entity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: PricingOutboxRow lưu thuộc tính dòng outbox nguyên tử khi thay đổi phiên bản bảng giá.
type PricingOutboxRow struct {
	ID                uuid.UUID
	PricingScheduleID uuid.UUID
	VersionID         uuid.UUID
	VersionNumber     int32
	ModuleCode        string
	ChargeKindCode    ChargeKindCode
	ScopeType         PricingScope
	ZoneID            *uuid.UUID
	EffectiveFrom     time.Time
	Checksum          string
	OccurredAt        time.Time
}
