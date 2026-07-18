package billingmigrations

import "embed"

// Files nhúng toàn bộ file SQL migration từ thư mục migrations/ vào binary
//
//go:embed *.sql
var Files embed.FS
