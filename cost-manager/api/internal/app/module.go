package app

import (
	"cost-manager/api/internal/domain/repo"
	domainservice "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/handler"
	"cost-manager/api/internal/repository"
	implservice "cost-manager/api/internal/service"
	pubsubhandler "cost-manager/api/internal/transport/pubsub/handler"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

type Module struct {
	WalletRepo repo.WalletRepository
	PriceRepo  repo.PriceRepository
	BillingSvc domainservice.BillingService
	ZoneSvc    domainservice.ZoneService

	WalletHandler *handler.WalletHandler
	PriceHandler  *handler.PriceHandler
	ZoneHandler   *handler.ZoneHandler
}

func NewModule(dbPool *pgxpool.Pool, natsConn *nats.Conn, redisClient *redis.Client) *Module {
	// Repositories
	walletRepo := repository.NewWalletRepository(dbPool)
	priceRepo := repository.NewPriceRepository(dbPool)

	// NATS pubsub clients (transport layer) — injected vào service
	zoneNatsClient := pubsubhandler.NewZoneNatsClient(natsConn)

	// Services
	billingSvc := implservice.NewBillingService(walletRepo, priceRepo)
	zoneSvc := implservice.NewZoneService(zoneNatsClient, redisClient)

	// HTTP Handlers
	walletHandler := handler.NewWalletHandler(billingSvc)
	priceHandler := handler.NewPriceHandler(billingSvc)
	zoneHandler := handler.NewZoneHandler(zoneSvc)

	return &Module{
		WalletRepo:    walletRepo,
		PriceRepo:     priceRepo,
		BillingSvc:    billingSvc,
		ZoneSvc:       zoneSvc,
		WalletHandler: walletHandler,
		PriceHandler:  priceHandler,
		ZoneHandler:   zoneHandler,
	}
}
