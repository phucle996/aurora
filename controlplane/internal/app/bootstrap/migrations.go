package bootstrap

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	"controlplane/internal/hierarchy"
	"controlplane/internal/hypervisor" // [NEW COMMENT]: Import phân hệ Hypervisor để thực thi migrations
	"controlplane/internal/iam"
	"controlplane/internal/mail"
	"controlplane/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	migrationLockKey1 int32 = 20260422
	migrationLockKey2 int32 = 1
)

func RunMigrations(ctx context.Context, db *pgxpool.Pool, cfg *config.Config) error {
	// không ccheck nil db và config
	// db đã fail close ở app.go
	// config mà nil thì nó sẽ fallback về default values (schema là hard code trong config file hết)
	// ----------------------------

	// acquire lock connection
	conn, err := db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migration: acquire lock connection: %w", err)
	}
	defer conn.Release()

	// bắt đầu một transaction để đảm bảo toàn bộ migration được thực hiện một lần
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("migration: begin transaction: %w", err)
	}

	// nếu có lỗi thì rollback transaction
	rollback := true
	defer func() {
		if rollback {
			if _, err := conn.Exec(context.Background(), "ROLLBACK"); err != nil {
				logger.SysWarn("app.migration", fmt.Sprintf("Failed to rollback migration transaction: %v", err))
			}
		}
	}()

	// acquire advisory lock để tránh xung đột migration giữa các instance
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, migrationLockKey1, migrationLockKey2); err != nil {
		return fmt.Errorf("migration: acquire lock: %w", err)
	}

	// core migrations
	if err := core.ApplyMigrations(ctx, conn, cfg); err != nil {
		return err
	}

	// iam migrations
	if err := iam.ApplyMigrations(ctx, conn, cfg); err != nil {
		return err
	}

	// mail migrations
	if err := mail.ApplyMigrations(ctx, conn, cfg); err != nil {
		return err
	}

	// [NEW COMMENT]: hypervisor migrations thực thi độc lập bằng transactional lock
	if err := hypervisor.ApplyMigrations(ctx, conn, cfg); err != nil {
		return err
	}

	// commit toàn bộ migration một lần
	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("migration: commit transaction: %w", err)
	}
	rollback = false

	return nil
}
