package hypervisorEntity

import (
	"time"

	"github.com/google/uuid"
)

type ImageState string

const (
	ImageStateUploading   ImageState = "UPLOADING"
	ImageStateImporting   ImageState = "IMPORTING"
	ImageStateAvailable   ImageState = "AVAILABLE"
	ImageStateQuarantined ImageState = "QUARANTINED"
	ImageStateFailed      ImageState = "FAILED"
	ImageStateDeleting    ImageState = "DELETING"
)

// ImageArtifact is one immutable image revision in one physical Zone. A
// successful delete removes the row; there is intentionally no soft-delete
// state or deleted_at column.
// Its object key and checksum never move between Zones.
type ImageArtifact struct {
	ID                   uuid.UUID
	ZoneID               uuid.UUID
	Name                 string
	Code                 string
	Distribution         string
	Release              string
	Revision             int64
	Architecture         string
	Format               string
	SizeBytes            int64
	SHA256               []byte
	ObjectKey            string
	State                ImageState
	CreatedBy            string
	ProviderTemplateVMID *int64
	ErrorCode            *string
	ErrorMessage         *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	AvailableAt          *time.Time
}

type RegisterImageMetadata struct {
	ZoneID       uuid.UUID
	Name         string
	Code         string
	Distribution string
	Release      string
	Revision     int64
	Architecture string
	Format       string
	SizeBytes    int64
	SHA256       []byte
	CreatedBy    string
}

type ImageImportRequest struct {
	ImageID uuid.UUID
	ZoneID  uuid.UUID
}

type ImageDeleteRequest struct {
	ImageID uuid.UUID
	ZoneID  uuid.UUID
}
