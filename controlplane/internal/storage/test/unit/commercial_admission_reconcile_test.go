package unit_test

import (
	"context"
	"testing"

	storageSvcImpl "controlplane/internal/storage/service"
)

type commercialAdmissionReconcileRepoStub struct {
	limit int
	count int
}

func (r *commercialAdmissionReconcileRepoStub) ReconcileBatch(_ context.Context, intLimit int) (int, error) {
	r.limit = intLimit
	return r.count, nil
}

func TestStorageCommercialAdmissionReconcileServiceUsesBoundedBatch(t *testing.T) {
	repo := &commercialAdmissionReconcileRepoStub{count: 37}
	service := storageSvcImpl.NewStorageCommercialAdmissionReconcileService(repo)

	count, err := service.ReconcileBatch(context.Background())
	if err != nil {
		t.Fatalf("reconcile batch: %v", err)
	}
	if count != 37 || repo.limit != 100 {
		t.Fatalf("unexpected bounded reconciliation: count=%d limit=%d", count, repo.limit)
	}
}
