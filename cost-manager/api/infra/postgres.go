package infra

import (
	"context"
	"fmt"
	"time"

	"cost-manager/api/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ConnectPostgres(dbURL string) (*pgxpool.Pool, error) {
	const op = "infra.postgres.connect"
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse postgres config: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnIdleTime = 15 * time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	logger.SysInfo(op, "Successfully connected to Postgres database pool")
	return pool, nil
}
