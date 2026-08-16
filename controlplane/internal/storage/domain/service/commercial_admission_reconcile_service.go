package storageSvcInterface

import "context"

type CommercialAdmissionReconcileService interface {
	ReconcileBatch(context.Context) (int, error)
}
