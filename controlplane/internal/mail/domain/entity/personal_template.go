package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

// --- Create Personal Template Flow ---

// [COMMENT]: CreatePersonalTemplateRequest mang thông tin đầu vào thuần túy từ handler.
// Không chứa field staging nào — SHA256, nén zstd, outbox record là concern của service layer.
type CreatePersonalTemplateRequest struct {
	ActorUserID     uuid.UUID
	ZoneID          uuid.UUID
	WorkspaceID     uuid.UUID
	Code            string
	Name            string
	SubjectTemplate string
	RawHTML         string
}

// [COMMENT]: CreatePersonalTemplateResponse mang dữ liệu đầu ra phẳng cho luồng tạo mới.
type CreatePersonalTemplateResponse struct {
	ID               string
	WorkspaceID      *uuid.UUID
	Code             string
	Name             string
	CurrentVersion   uint64
	TemplateRevision uint64
	SubjectTemplate  string
	RawHTML          string
	ContentSHA256    []byte
	OperationID      uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// --- Get Personal Template Flow ---

// [COMMENT]: GetPersonalTemplateRequest mang tham số truy vấn đọc chi tiết template.
type GetPersonalTemplateRequest struct {
	ActorUserID uuid.UUID
	ZoneID      uuid.UUID
	WorkspaceID uuid.UUID
	TemplateID  string
}

// [COMMENT]: GetPersonalTemplateResponse mang kết quả đọc chi tiết template (dữ liệu phẳng, không lồng struct).
type GetPersonalTemplateResponse struct {
	ID               string
	WorkspaceID      *uuid.UUID
	Code             string
	Name             string
	CurrentVersion   uint64
	TemplateRevision uint64
	SubjectTemplate  string
	RawHTML          string
	ContentSHA256    []byte
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// --- List Personal Templates Flow ---

// [COMMENT]: ListPersonalTemplatesRequest mang thông tin phân trang danh sách template.
type ListPersonalTemplatesRequest struct {
	ActorUserID uuid.UUID
	ZoneID      uuid.UUID
	WorkspaceID uuid.UUID
	AfterID     string
	Limit       uint32
}

// [COMMENT]: PersonalTemplateItem đại diện cho một phần tử phẳng trong danh sách template.
type PersonalTemplateItem struct {
	ID               string
	WorkspaceID      *uuid.UUID
	Code             string
	Name             string
	CurrentVersion   uint64
	TemplateRevision uint64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// --- List Personal Template Versions Flow ---

// [COMMENT]: ListPersonalTemplateVersionsRequest mang thông tin truy vấn lịch sử phiên bản.
type ListPersonalTemplateVersionsRequest struct {
	ActorUserID   uuid.UUID
	ZoneID        uuid.UUID
	WorkspaceID   uuid.UUID
	TemplateID    string
	BeforeVersion uint64
	Limit         uint32
}

// [COMMENT]: PersonalTemplateVersionItem đại diện cho một bản ghi phiên bản phẳng.
type PersonalTemplateVersionItem struct {
	TemplateID      string
	Version         uint64
	SubjectTemplate string
	RawHTML         string
	ContentSHA256   []byte
	CreatedAt       time.Time
}

// --- Publish Personal Template Version Flow ---

// [COMMENT]: PublishPersonalTemplateVersionRequest mang dữ liệu đầu vào thuần túy từ handler.
// Không chứa field staging — SHA256, nén zstd, outbox record là concern của service layer.
type PublishPersonalTemplateVersionRequest struct {
	ActorUserID      uuid.UUID
	ZoneID           uuid.UUID
	WorkspaceID      uuid.UUID
	TemplateID       string
	ExpectedRevision uint64
	SubjectTemplate  string
	RawHTML          string
}

// [COMMENT]: PublishPersonalTemplateVersionResponse mang kết quả nạp phiên bản phẳng.
// CurrentRevision = active head revision (không đổi sau publish candidate).
// PublishedRevision = candidate revision vừa được ghi — phân biệt rõ để JO promote đúng.
type PublishPersonalTemplateVersionResponse struct {
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
	OperationID        uuid.UUID
	HeadCreatedAt      time.Time
	CandidateCreatedAt time.Time
}

// --- Delete Personal Template Flow ---

// [COMMENT]: DeletePersonalTemplateRequest mang thông tin đầu vào thuần túy từ handler.
// Không chứa field staging — outbox record là concern của service layer.
type DeletePersonalTemplateRequest struct {
	ActorUserID      uuid.UUID
	ZoneID           uuid.UUID
	WorkspaceID      uuid.UUID
	TemplateID       string
	ExpectedRevision uint64
}
