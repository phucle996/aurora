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
	PlanRepo   repo.PlanRepository
	SubRepo    repo.SubscriptionRepository

	BillingSvc domainservice.BillingService
	ZoneSvc    domainservice.ZoneService
	PlanSvc    domainservice.PlanService

	WalletHandler *handler.WalletHandler
	PriceHandler  *handler.PriceHandler
	ZoneHandler   *handler.ZoneHandler
	PlanHandler   *handler.PlanHandler
	SubHandler    *handler.SubscriptionHandler
}

func NewModule(dbPool *pgxpool.Pool, natsConn *nats.Conn, redisClient *redis.Client) *Module {
	// Repositories
	walletRepo := repository.NewWalletRepository(dbPool)
	priceRepo := repository.NewPriceRepository(dbPool)
	planRepo := repository.NewPlanRepository(dbPool)
	subRepo := repository.NewSubscriptionRepository(dbPool)

	// NATS pubsub clients (transport layer) — injected vào service
	zoneNatsClient := pubsubhandler.NewZoneNatsClient(natsConn)

	// Services
	billingSvc := implservice.NewBillingService(walletRepo, priceRepo, subRepo)
	zoneSvc := implservice.NewZoneService(zoneNatsClient, redisClient)
	planSvc := implservice.NewPlanService(planRepo, subRepo, walletRepo)

	// HTTP Handlers
	walletHandler := handler.NewWalletHandler(billingSvc)
	priceHandler := handler.NewPriceHandler(billingSvc)
	zoneHandler := handler.NewZoneHandler(zoneSvc)
	planHandler := handler.NewPlanHandler(planSvc)
	subHandler := handler.NewSubscriptionHandler(planSvc)

	return &Module{
		WalletRepo:    walletRepo,
		PriceRepo:     priceRepo,
		PlanRepo:      planRepo,
		SubRepo:       subRepo,
		BillingSvc:    billingSvc,
		ZoneSvc:       zoneSvc,
		PlanSvc:       planSvc,
		WalletHandler: walletHandler,
		PriceHandler:  priceHandler,
		ZoneHandler:   zoneHandler,
		PlanHandler:   planHandler,
		SubHandler:    subHandler,
	}
}
