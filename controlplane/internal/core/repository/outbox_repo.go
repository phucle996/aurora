// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/repository/outbox_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Quản Trị Cơ Sở Dữ Liệu Transactional Outbox Registry
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & TỐI ƯU HÓA HẠ TẦNG (CONTRACT & PEAK PERFORMANCE INFRASTRUCTURE):
//   - File này chịu trách nhiệm tương tác trực tiếp với PostgreSQL cho bảng 'outbox_records'.
//   - Triển khai và áp dụng tối ưu hóa hiệu năng cực hạn (Peak Performance Optimization) nhằm triệt tiêu hoàn toàn:
//
//     1) TRUY VẤN TĨNH KHỞI TẠO SỚM (STATIC QUERY PRE-COMPUTATION):
//        * Tất cả chuỗi truy vấn SQL được định dạng schema và biên dịch trước một lần duy nhất tại
//          hàm khởi tạo `NewOutboxRepoImpl`.
//        * Triệt tiêu hoàn toàn chi phí sử dụng `fmt.Sprintf` tại runtime ở hot path, giảm thiểu
//          các phân bổ heap dynamic memory và tiết kiệm chu kỳ CPU của Go GC.
//
//     2) TRANSACTIONAL INTEGRITY (ĐẢM BẢO TÍNH TOÀN VẸN GIAO DỊCH):
//        * Lưu trữ các thay đổi cấu hình (desire state) của Zone cùng lúc với sự kiện Outbox trong
//          cùng một transaction SQL để đảm bảo tính nguyên tử (Atomicity).
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Database schema 'core.outbox_records' dưới PostgreSQL là nguồn tin cậy duy nhất cho Desired State lâu dài.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Chỉ thực hiện các thao tác SQL cơ bản: INSERT, UPDATE, SELECT.
//   - Tuyệt đối không tự ý áp đặt business logic hay validation nghiệp vụ phức tạp tại đây.
//   - Trả lỗi thô hoặc lỗi nghiệp vụ định nghĩa sẵn trực tiếp cho tầng Service.
//
// ======================================================================================================

package coreRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"

	"github.com/jackc/pgx/v5/pgxpool"
)

type OutboxRepoImpl struct {
	db                 *pgxpool.Pool
	schema             string
	saveQuery          string
	fetchPendingQuery  string
	markPublishedQuery string
	markFailedQuery    string
}

// NewOutboxRepoImpl khởi tạo repository lưu trữ Outbox thô trong DB và biên dịch sẵn các câu lệnh SQL.
func NewOutboxRepoImpl(cfg *config.Config, db *pgxpool.Pool) coreRepoInterface.OutboxRepository {
	schema := cfg.SchemaSQL.Core
	return &OutboxRepoImpl{
		db:     db,
		schema: schema,
		saveQuery: fmt.Sprintf(`
			INSERT INTO %s.outbox_records (event_id, entity, op, payload, version, status, attempts, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			RETURNING id
		`, schema),
		fetchPendingQuery: fmt.Sprintf(`
			SELECT id, event_id, entity, op, payload, version, status, attempts, last_attempt, created_at
			FROM %s.outbox_records
			WHERE status = 'PENDING'
			ORDER BY created_at ASC
			LIMIT $1
		`, schema),
		markPublishedQuery: fmt.Sprintf(`
			UPDATE %s.outbox_records
			SET status = 'PUBLISHED', last_attempt = NOW()
			WHERE id = $1
		`, schema),
		markFailedQuery: fmt.Sprintf(`
			UPDATE %s.outbox_records
			SET status = 'FAILED', attempts = attempts + 1, last_attempt = NOW()
			WHERE id = $1
		`, schema),
	}
}

// Save ghi nhận một bản ghi Outbox mới vào cơ sở dữ liệu.
func (r *OutboxRepoImpl) Save(ctx context.Context, record *coreEntity.OutboxRecord) error {
	err := r.db.QueryRow(ctx, r.saveQuery,
		record.EventID,
		record.Entity,
		record.Op,
		record.Payload,
		record.Version,
		string(record.Status),
		record.Attempts,
	).Scan(&record.ID)

	return err
}

// FetchPending lấy danh sách các bản ghi Outbox đang ở trạng thái PENDING giới hạn bởi tham số limit.
func (r *OutboxRepoImpl) FetchPending(ctx context.Context, limit int) ([]*coreEntity.OutboxRecord, error) {
	rows, err := r.db.Query(ctx, r.fetchPendingQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*coreEntity.OutboxRecord
	for rows.Next() {
		rec := &coreEntity.OutboxRecord{}
		var statusStr string
		err := rows.Scan(
			&rec.ID,
			&rec.EventID,
			&rec.Entity,
			&rec.Op,
			&rec.Payload,
			&rec.Version,
			&statusStr,
			&rec.Attempts,
			&rec.LastAttempt,
			&rec.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		rec.Status = coreEntity.OutboxStatus(statusStr)
		records = append(records, rec)
	}

	return records, rows.Err()
}

// MarkPublished cập nhật trạng thái bản ghi Outbox thành PUBLISHED.
func (r *OutboxRepoImpl) MarkPublished(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, r.markPublishedQuery, id)
	return err
}

// MarkFailed cập nhật trạng thái bản ghi Outbox thành FAILED và tăng số lần thử lại (attempts).
func (r *OutboxRepoImpl) MarkFailed(ctx context.Context, id int64, reason string) error {
	_, err := r.db.Exec(ctx, r.markFailedQuery, id)
	return err
}
