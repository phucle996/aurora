package storageRepoInterface

import "context"

// WalletAdmissionRepo reads the local Storage wallet-admission projection.
// It never calls Billing at request time; a missing or stale projection denies.
type WalletAdmissionRepo interface {
	RequireOwnerAdmission(ctx context.Context, ownerID, ownerType string) error
}
