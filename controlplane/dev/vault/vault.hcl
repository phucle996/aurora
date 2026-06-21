# ============================================================================
# 🔒 HASHICORP VAULT CONFIGURATION FILE (vault.hcl)
# ============================================================================
# Thiết lập cấu hình vận hành cho HashiCorp Vault trong môi trường Cloud-Native & HA.
# Sử dụng Raft làm Storage Backend để hỗ trợ cơ chế đồng thuận phân tán và khả năng chịu lỗi cao.

# [COMMENT]: Bật giao diện điều khiển (Vault Web UI) phục vụ quản trị và debug
ui = true

# [COMMENT]: Cấu hình Raft Storage Backend để lưu trữ secrets bền vững
# Phù hợp cho môi trường High-Availability (HA) thay vì chạy dev/in-memory
storage "raft" {
  path    = "/vault/data"
  node_id = "vault-node-1"
}

# [COMMENT]: Cấu hình TCP Listener lắng nghe các yêu cầu kết nối
# Trong môi trường dev, ta disable TLS. Ở môi trường Production HA, bắt buộc cấu hình TLS certs
listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = "true"
}

# [COMMENT]: Cấu hình mlock (Memory Lock)
# Tắt mlock khi chạy trong container Docker để tránh lỗi phân quyền (IPC_LOCK capability)
# Trong môi trường production thật, nên bật mlock = false hoặc cấp quyền IPC_LOCK cho container để tránh ghi secrets vào swap space
disable_mlock = true

# [COMMENT]: Cấu hình API và Cluster Redirect Address (dành cho chế độ Clustering HA)
api_addr     = "http://127.0.0.1:8200"
cluster_addr = "http://127.0.0.1:8201"
