package storageWorker

import (
	"context"
	"sync"
	"time"

	storageSvcInterface "controlplane/internal/storage/domain/service"
	"controlplane/pkg/logger"
)

type CommercialAdmissionZoneRelay struct {
	service storageSvcInterface.CommercialAdmissionZoneRelayService
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewCommercialAdmissionZoneRelay(
	service storageSvcInterface.CommercialAdmissionZoneRelayService,
) *CommercialAdmissionZoneRelay {
	return &CommercialAdmissionZoneRelay{service: service, cancel: func() {}}
}

func (w *CommercialAdmissionZoneRelay) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			for ctx.Err() == nil {
				published, err := w.service.RelayBatch(ctx)
				if err != nil {
					if ctx.Err() == nil {
						logger.SysWarn("storage.commercial_admission.zone_relay", err.Error())
					}
					break
				}
				if published == 0 {
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

func (w *CommercialAdmissionZoneRelay) Stop() {
	w.cancel()
	w.wg.Wait()
}
