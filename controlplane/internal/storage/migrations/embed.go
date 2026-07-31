package migrations

import "embed"

// Files nhúng toàn bộ các tệp tin SQL trong thư mục này phục vụ cho việc tự động chạy migrations lúc khởi động
//
//go:embed *.sql
var Files embed.FS
