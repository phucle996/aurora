package storageRepoInterface

import "context"

// CommercialAdmissionRepo reads the local Storage commercial-admission projection.
// It never calls Billing at request time; a missing or stale projection denies.
type CommercialAdmissionRepo interface {
	RequireOwnerAdmission(ctx context.Context, ownerID, ownerType string) error
}
