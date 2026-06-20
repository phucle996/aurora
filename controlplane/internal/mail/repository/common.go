package mailRepoImpl

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Định nghĩa context key dùng cho truyền tải transaction nội bộ trong package postgres
type txKey struct{}

// QueryExecutor đại diện cho một đối tượng thực thi truy vấn SQL (có thể là pgxpool.Pool hoặc pgx.Tx)
type QueryExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
