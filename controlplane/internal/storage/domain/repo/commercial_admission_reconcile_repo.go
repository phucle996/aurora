package storageRepoInterface

import "context"

type CommercialAdmissionReconcileRepository interface {
	ReconcileBatch(context.Context, int) (int, error)
}
