package app

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	// Domain interfaces và repositories
)

type Module struct {
}

func NewModule(dbPool *pgxpool.Pool, natsConn *nats.Conn, redisClient *redis.Client) *Module {

	return &Module{}
}
