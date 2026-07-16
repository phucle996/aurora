package infra

import (
	"context"
	"fmt"

	"cost-manager/api/pkg/logger"
	"github.com/redis/go-redis/v9"
)

func ConnectRedis(redisURL string) (*redis.Client, error) {
	const op = "infra.redis.connect"
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url: %w", err)
	}

	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	logger.SysInfo(op, "Successfully connected to Redis server")
	return client, nil
}
