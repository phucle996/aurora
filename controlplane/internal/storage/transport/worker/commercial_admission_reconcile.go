package storageWorker

import (
	"context"
	"sync"
	"time"

	storageSvcInterface "controlplane/internal/storage/domain/service"
	"controlplane/pkg/logger"
)

type CommercialAdmissionReconcile struct {
	service storageSvcInterface.CommercialAdmissionReconcileService
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewCommercialAdmissionReconcile(service storageSvcInterface.CommercialAdmissionReconcileService) *CommercialAdmissionReconcile {
	return &CommercialAdmissionReconcile{service: service, cancel: func() {}}
}

func (w *CommercialAdmissionReconcile) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			for ctx.Err() == nil {
				reconciled, err := w.service.ReconcileBatch(ctx)
				if err != nil {
					if ctx.Err() == nil {
						logger.SysWarn("storage.commercial_admission.reconcile", err.Error())
					}
					break
				}
				if reconciled == 0 {
					break
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (w *CommercialAdmissionReconcile) Stop() {
	w.cancel()
	w.wg.Wait()
}
