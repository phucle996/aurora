package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

// --- Create Tenant Template Flow ---

// [COMMENT]: CreateTenantTemplateRequest mang thông tin đầu vào thuần túy từ handler.
// Không chứa field staging nào — SHA256, nén zstd, outbox record là concern của service layer.
type CreateTenantTemplateRequest struct {
	ActorUserID     uuid.UUID
	TenantID        uuid.UUID
	ZoneID          uuid.UUID
	WorkspaceID     uuid.UUID
	Code            string
	Name            string
	SubjectTemplate string
	RawHTML         string
}

// [COMMENT]: CreateTenantTemplateResponse mang dữ liệu đầu ra phẳng cho luồng tạo mới Tenant.
type CreateTenantTemplateResponse struct {
	ID               string
	WorkspaceID      *uuid.UUID
	Code             string
	Name             string
	CurrentVersion   uint64
	TemplateRevision uint64
	SubjectTemplate  string
	RawHTML          string
	ContentSHA256    []byte
	CreatedBy        *uuid.UUID
	UpdatedBy        *uuid.UUID
	OperationID      uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// --- Get Tenant Template Flow ---

// [COMMENT]: GetTenantTemplateRequest mang tham số đọc chi tiết mẫu Tenant.
type GetTenantTemplateRequest struct {
	ActorUserID uuid.UUID
	TenantID    uuid.UUID
	ZoneID      uuid.UUID
	WorkspaceID uuid.UUID
	TemplateID  string
}

// [COMMENT]: GetTenantTemplateResponse mang kết quả đọc chi tiết phẳng cho Tenant.
type GetTenantTemplateResponse struct {
	ID               string
	WorkspaceID      *uuid.UUID
	Code             string
	Name             string
	CurrentVersion   uint64
	TemplateRevision uint64
	SubjectTemplate  string
	RawHTML          string
	ContentSHA256    []byte
	CreatedBy        *uuid.UUID
	UpdatedBy        *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// --- List Tenant Templates Flow ---

// [COMMENT]: ListTenantTemplatesRequest mang tham số phân trang danh sách mẫu Tenant.
type ListTenantTemplatesRequest struct {
	ActorUserID uuid.UUID
	TenantID    uuid.UUID
	ZoneID      uuid.UUID
	WorkspaceID uuid.UUID
	AfterID     string
	Limit       uint32
}

// [COMMENT]: TenantTemplateItem đại diện cho một phần tử phẳng trong danh sách mẫu Tenant.
type TenantTemplateItem struct {
	ID               string
	WorkspaceID      *uuid.UUID
	Code             string
	Name             string
	CurrentVersion   uint64
	TemplateRevision uint64
	CreatedBy        *uuid.UUID
	UpdatedBy        *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// --- List Tenant Template Versions Flow ---

// [COMMENT]: ListTenantTemplateVersionsRequest mang thông tin đọc danh sách lịch sử phiên bản mẫu Tenant.
type ListTenantTemplateVersionsRequest struct {
	ActorUserID   uuid.UUID
	TenantID      uuid.UUID
	ZoneID        uuid.UUID
	WorkspaceID   uuid.UUID
	TemplateID    string
	BeforeVersion uint64
	Limit         uint32
}

// [COMMENT]: TenantTemplateVersionItem đại diện cho một bản ghi phiên bản phẳng của Tenant.
type TenantTemplateVersionItem struct {
	TemplateID      string
	Version         uint64
	SubjectTemplate string
	RawHTML         string
	ContentSHA256   []byte
	CreatedBy       *uuid.UUID
	CreatedAt       time.Time
}

// --- Publish Tenant Template Version Flow ---

// [COMMENT]: PublishTenantTemplateVersionRequest mang tham số nạp phiên bản mới cho Tenant.
// Không chứa field staging — SHA256, nén zstd, outbox record là concern của service layer.
type PublishTenantTemplateVersionRequest struct {
	ActorUserID      uuid.UUID
	TenantID         uuid.UUID
	ZoneID           uuid.UUID
	WorkspaceID      uuid.UUID
	TemplateID       string
	ExpectedRevision uint64
	SubjectTemplate  string
	RawHTML          string
}

// [COMMENT]: PublishTenantTemplateVersionResponse mang kết quả nạp phiên bản phẳng của Tenant.
// CurrentRevision = active head revision (không đổi sau publish candidate).
// PublishedRevision = candidate revision vừa được ghi — phân biệt rõ để JO promote đúng.
type PublishTenantTemplateVersionResponse struct {
	ID                 string
	WorkspaceID        *uuid.UUID
	Code               string
	Name               string
	CurrentVersion     uint64
	CurrentRevision    uint64
	PublishedVersion   uint64
	PublishedRevision  uint64
	SubjectTemplate    string
	RawHTML            string
	ContentSHA256      []byte
	UpdatedBy          *uuid.UUID
	OperationID        uuid.UUID
	HeadCreatedAt      time.Time
	CandidateCreatedAt time.Time
}

// --- Delete Tenant Template Flow ---

// [COMMENT]: DeleteTenantTemplateRequest mang thông tin đầu vào thuần túy từ handler.
// Không chứa field staging — outbox record là concern của service layer.
type DeleteTenantTemplateRequest struct {
	ActorUserID      uuid.UUID
	TenantID         uuid.UUID
	ZoneID           uuid.UUID
	WorkspaceID      uuid.UUID
	TemplateID       string
	ExpectedRevision uint64
}
