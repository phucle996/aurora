package infra

import (
	"context"
	"fmt"
	"strings"

	"cost-manager/api/pkg/logger"
	"github.com/redis/go-redis/v9"
)

const (
	SharedL2ConnectionPath  = "secret/data/connections/redis/shared-l2/role-wallet-command-rw"
	AuthStateConnectionPath = "secret/data/connections/redis/auth-state/role-proof-rw"
)

type redisConnectionRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Addr          string `json:"addr"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	DB            int    `json:"db"`
}

func ConnectRedis(ctx context.Context, vaultClient *VaultClient, path string) (*redis.Client, error) {
	const op = "infra.redis.connect"
	var connection redisConnectionRecord
	if err := vaultClient.ReadJSON(ctx, path, &connection); err != nil {
		return nil, fmt.Errorf("read Vault Redis connection: %w", err)
	}
	if connection.SchemaVersion != 1 || strings.TrimSpace(connection.Addr) == "" {
		return nil, fmt.Errorf("infra redis: Vault connection record is incomplete")
	}
	if connection.DB < 0 {
		return nil, fmt.Errorf("infra redis: Vault database index is invalid")
	}

	client := redis.NewClient(&redis.Options{
		Addr:     connection.Addr,
		Username: connection.Username,
		Password: connection.Password,
		DB:       connection.DB,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}
	logger.SysInfo(op, "Successfully connected to Redis server")
	return client, nil
}
