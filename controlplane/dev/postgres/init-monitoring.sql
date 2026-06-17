-- [SECURITY]: Tạo role chuyên dụng với quyền tối thiểu (Least Privilege) cho postgres_exporter.
-- Tuyệt đối không cấp quyền SUPERUSER, CREATEDB, hoặc CREATEROLE để hạn chế tối đa rủi ro an ninh mạng.
CREATE USER postgres_exporter WITH PASSWORD 'postgres_exporter_password';

-- [SECURITY]: Gán role pg_monitor (có sẵn từ phiên bản PostgreSQL 10+) để cho phép
-- tài khoản giám sát đọc thông tin metadata và system catalogs (như pg_stat_database, 
-- pg_stat_replication, pg_stat_activity) mà không thể truy cập dữ liệu trong các bảng nghiệp vụ.
GRANT pg_monitor TO postgres_exporter;
