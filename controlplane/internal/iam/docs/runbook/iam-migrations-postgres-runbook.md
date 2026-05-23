# IAM Migration Runbook (Production PostgreSQL)

## 1) Mục tiêu

Hướng dẫn chạy migration IAM trên **DB production thật** theo quy trình an toàn:
- kiểm soát rủi ro,
- có backup trước khi chạy,
- có verify sau khi chạy,
- có rollback/forward-fix plan rõ ràng.

Phạm vi migration:
- `internal/iam/migrations/000001_iam_enums.*`
- `internal/iam/migrations/000002_iam_tables.*`
- `internal/iam/migrations/000003_iam_indexes.*`
- `internal/iam/migrations/000004_iam_funcs.*`
- `internal/iam/migrations/000005_iam_triggers.*`
- `internal/iam/migrations/000006_iam_seeds.*`

Verification bundle:
- `internal/iam/migrations/verification_iam_post_migrate.sql`

---

## 2) Change prerequisites (bắt buộc)

- Có maintenance window + thông báo stakeholders.
- Có approval từ owner IAM + DBA/oncall.
- Đã chạy thành công trên staging với data gần prod.
- Xác nhận version code deploy tương thích schema mới.
- Có backup snapshot/PITR checkpoint ngay trước migration.

Không đạt một điều kiện nào ở trên thì **không chạy prod**.

---

## 3) Pre-flight checklist

1. Xác nhận kết nối đúng môi trường production (host/db/schema).
2. Xác nhận quyền DB account đủ để chạy DDL/DML cần thiết.
3. Xác nhận không có migration job khác đang chạy đồng thời.
4. Chụp trạng thái baseline:
   - số lượng bảng IAM hiện hữu,
   - trạng thái auth/login error rate,
   - p95 latency các endpoint IAM chính.
5. Thực hiện backup:
   - snapshot volume hoặc PITR bookmark,
   - lưu timestamp + backup id vào ticket change.

---

## 4) Cách chạy migration trên prod

> Ví dụ dưới đây dùng `psql` trực tiếp vào DB thật.  
> Không dùng `docker exec` cho production database.

```bash
export PGHOST="<prod-db-host>"
export PGPORT="<prod-db-port>"
export PGDATABASE="<prod-db-name>"
export PGUSER="<prod-db-user>"
export PGPASSWORD="<prod-db-password>"
```

Chạy lần lượt `up.sql`:

```bash
for f in internal/iam/migrations/*_iam_*.up.sql; do
  echo "[UP] $(basename "$f")"
  psql -v ON_ERROR_STOP=1 -f "$f"
done
```

Nguyên tắc:
- Dừng ngay khi có lỗi đầu tiên.
- Không “skip file lỗi rồi chạy tiếp”.

---

## 5) Post-migrate verification (prod)

Chạy verify bundle:

```bash
psql -v ON_ERROR_STOP=1 -f internal/iam/migrations/verification_iam_post_migrate.sql
```

Tiêu chí pass:
- `to_regclass` trả về object đầy đủ.
- Index quan trọng có trong `pg_indexes`.
- Constraint quan trọng có trong `pg_constraint`.
- `COMMENT ON` contract có đủ cho object trọng yếu.

Kiểm tra hash format user seed (nếu có seed user hệ thống):

```bash
psql -v ON_ERROR_STOP=1 -c \
"SELECT email, split_part(password_hash, '$', 1) = 'argon2id' AS argon2id_prefix_ok
 FROM users
 WHERE email IN ('root','sys_admin','support_operator','audit_viewer')
 ORDER BY email;"
```

---

## 6) Application smoke test sau migration

Chạy ngay sau DB verify pass:
- Login success path (admin + user).
- Refresh token path.
- RBAC check path (`roles`, `permissions`, `assignments`).
- Audit event write/read path.

Nếu smoke test fail, chuyển sang mục 7.

---

## 7) Rollback / Forward-fix strategy

### 7.1 Khi nào rollback
- Lỗi nghiêm trọng ảnh hưởng auth production và chưa có fix nhanh.
- Tỷ lệ lỗi tăng mạnh vượt ngưỡng SLO.

Rollback thứ tự:

```bash
for f in $(ls -1 internal/iam/migrations/*_iam_*.down.sql | sort -r); do
  echo "[DOWN] $(basename "$f")"
  psql -v ON_ERROR_STOP=1 -f "$f"
done
```

### 7.2 Khi nào forward-fix
- Lỗi nhỏ, fix migration/script nhanh, rủi ro rollback cao.
- Data đã thay đổi và rollback có thể gây mất dữ liệu.

Ưu tiên production thường là **forward-fix** nếu an toàn hơn rollback.

---

## 8) Observability sau release (30–60 phút)

Theo dõi:
- auth/login failure rate,
- DB lock/wait events,
- query latency IAM tables chính,
- error log `invalid password hash`, `constraint violation`, `relation does not exist`.

Nếu vượt ngưỡng, kích hoạt incident process.

---

## 9) Common prod failure patterns

- `relation ... does not exist`:
  - sai thứ tự object/FK trong up migration.
- `cannot drop ... depends on it`:
  - sai thứ tự down migration.
- `no unique or exclusion constraint matching ON CONFLICT`:
  - dùng `ON CONFLICT(col)` nhưng cột không có unique/PK.
- Login fail sau seed:
  - hash không đúng format Argon2id theo `internal/security/password.go`.

---

## 10) Change record (bắt buộc lưu)

Sau khi chạy xong, ghi vào ticket:
- thời gian bắt đầu/kết thúc,
- operator thực thi,
- danh sách file đã chạy,
- kết quả verification,
- backup id/snapshot id,
- kết luận: success / rollback / forward-fix.
