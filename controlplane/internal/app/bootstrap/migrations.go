package bootstrap

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	"controlplane/internal/core"
	"controlplane/internal/iam"
	"controlplane/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	migrationLockKey1 int32 = 20260422
	migrationLockKey2 int32 = 1
)

func RunMigrations(ctx context.Context, db *pgxpool.Pool, cfg *config.Config) error {
	if db == nil {
		return fmt.Errorf("migration: db is nil")
	}
	if cfg == nil {
		return fmt.Errorf("migration: config is nil")
	}

	conn, err := db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migration: acquire lock connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("migration: begin transaction: %w", err)
	}

	rollback := true
	defer func() {
		if rollback {
			if _, err := conn.Exec(context.Background(), "ROLLBACK"); err != nil {
				logger.SysWarn("app.migration", fmt.Sprintf("Failed to rollback migration transaction: %v", err))
			}
		}
	}()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, migrationLockKey1, migrationLockKey2); err != nil {
		return fmt.Errorf("migration: acquire lock: %w", err)
	}

	if err := core.ApplyMigrations(ctx, conn, cfg); err != nil {
		return err
	}
	if err := iam.ApplyMigrations(ctx, conn, cfg); err != nil {
		return err
	}

	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("migration: commit transaction: %w", err)
	}
	rollback = false

	return nil
}
