#!/usr/bin/env bash

# ============================================================================
# 🚀 HASHICORP VAULT BOOTSTRAP & SECRET CREATION SCRIPT (vault-bootstrap.sh)
# ============================================================================
# Script tự động khởi tạo (Init), mở khóa (Unseal), cấu hình Transit Engine
# và khởi tạo các secrets trong KV Engine cho HashiCorp Vault.
# Hướng đến thiết lập tự động hóa, dễ bảo trì cho môi trường High Availability (HA).

set -euo pipefail

# [COMMENT]: Định nghĩa các đường dẫn file và cấu hình cần thiết
VAULT_CONTAINER="controlplane-vault"
INIT_KEYS_FILE="dev/vault/init-keys.json"
DEFAULT_ROOT_TOKEN="myroot"

# [COMMENT]: Hàm helper thực thi lệnh Vault CLI trong container với URL HTTP chính xác
# Tránh lỗi tự động kết nối HTTPS của CLI khi Vault Server đã disable TLS
vault_cmd() {
  docker exec -e VAULT_ADDR="http://127.0.0.1:8200" "$VAULT_CONTAINER" vault "$@"
}

echo "=== [1/6] Đang chờ Vault Server sẵn sàng phản hồi... ==="
# [COMMENT]: Chờ cho đến khi Vault bắt đầu lắng nghe và phản hồi lệnh status (tránh lỗi connection refused)
# Đọc stdout của lệnh status và lưu vào biến, sử dụng || true để tránh set -e ngắt script do pipefail hoặc exit code của Vault
while true; do
  STATUS_OUT=$(vault_cmd status 2>/dev/null || true)
  if echo "$STATUS_OUT" | grep -qE "Initialized|Sealed"; then
    break
  fi
  echo "Vault đang khởi động, thử lại sau 2 giây..."
  sleep 2
done
echo "Vault Server đã sẵn sàng phản hồi!"

echo "=== [2/6] Kiểm tra trạng thái Khởi tạo (Initialization) ==="
# [COMMENT]: Đọc JSON status trước để tách biệt các lệnh trong pipeline, tránh lỗi lặp output và pipefail
STATUS_OUT=$(vault_cmd status -format=json 2>/dev/null || true)
INITIALIZED=$(echo "$STATUS_OUT" | jq -r '.initialized' 2>/dev/null || echo "false")

# [COMMENT]: So sánh khác "true" để bao quát toàn bộ trường hợp: false, null, hoặc empty khi command bị lỗi hoặc chưa init
if [ "$INITIALIZED" != "true" ]; then
  echo "Vault chưa được khởi tạo. Bắt đầu quá trình Operator Init..."
  # [COMMENT]: Khởi tạo Vault với 1 key share và threshold = 1 (phù hợp dev/testing)
  # Trong production thực tế nên dùng 5 key shares và threshold = 3 để đảm bảo tính an toàn HA
  vault_cmd operator init -key-shares=1 -key-threshold=1 -format=json > "$INIT_KEYS_FILE"
  echo "Khởi tạo thành công! Thông tin khóa được lưu tại: $INIT_KEYS_FILE"
else
  echo "Vault đã được khởi tạo trước đó."
  if [ ! -f "$INIT_KEYS_FILE" ]; then
    echo "🚨 LỖI: Không tìm thấy file $INIT_KEYS_FILE chứa Unseal Keys!"
    exit 1
  fi
fi

# [COMMENT]: Trích xuất Unseal Key và Root Token ban đầu từ file JSON lưu trữ
UNSEAL_KEY=$(jq -r '.unseal_keys_b64[0]' "$INIT_KEYS_FILE")
TEMP_ROOT_TOKEN=$(jq -r '.root_token' "$INIT_KEYS_FILE")

echo "=== [3/6] Kiểm tra trạng thái Khóa (Sealed/Unsealed) ==="
# [COMMENT]: Đọc JSON status mới sau khi init để kiểm tra trạng thái sealed chính xác
STATUS_OUT=$(vault_cmd status -format=json 2>/dev/null || true)
SEALED=$(echo "$STATUS_OUT" | jq -r '.sealed' 2>/dev/null || echo "true")

if [ "$SEALED" = "true" ]; then
  echo "Vault đang bị khóa (Sealed). Thực hiện Unseal..."
  # [COMMENT]: Gọi lệnh unseal của Vault CLI với khóa đã trích xuất
  vault_cmd operator unseal "$UNSEAL_KEY"
  echo "Vault đã được mở khóa (Unsealed) thành công!"
else
  echo "Vault đã được mở khóa trước đó."
fi

echo "=== [4/6] Khởi tạo Token tĩnh 'myroot' khớp cấu hình .env ==="
# [COMMENT]: Sử dụng token gốc tạm thời để sinh ra token tĩnh 'myroot' với quyền 'root'
# Điều này giúp Go Backend kết nối ngay lập tức mà không cần sửa đổi biến VAULT_TOKEN=myroot trong file .env
docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$TEMP_ROOT_TOKEN" "$VAULT_CONTAINER" \
  vault token create -id="$DEFAULT_ROOT_TOKEN" -policy="root" >/dev/null 2>&1 || true

echo "=== [5/6] Cấu hình Transit Engine & Tạo Khóa ký JWT ==="
# [COMMENT]: Kiểm tra xem transit secrets engine đã được enable chưa bằng jq
TRANSIT_ENABLED=$(docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$DEFAULT_ROOT_TOKEN" "$VAULT_CONTAINER" vault secrets list -format=json 2>/dev/null | jq -e '."transit/"' >/dev/null 2>&1 && echo "true" || echo "false")
if [ "$TRANSIT_ENABLED" = "false" ]; then
  echo "Đang kích hoạt Transit Secrets Engine..."
  docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$DEFAULT_ROOT_TOKEN" "$VAULT_CONTAINER" vault secrets enable transit
fi

# [COMMENT]: Tạo khóa ký đối xứng 'jwt-signer' dùng để sinh HMAC cho JWT tokens nếu chưa tồn tại
KEY_EXISTS=$(docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$DEFAULT_ROOT_TOKEN" "$VAULT_CONTAINER" vault read -format=json transit/keys/jwt-signer >/dev/null 2>&1 && echo "true" || echo "false")
if [ "$KEY_EXISTS" = "false" ]; then
  echo "Đang tạo khóa ký đối xứng 'jwt-signer'..."
  docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$DEFAULT_ROOT_TOKEN" "$VAULT_CONTAINER" vault write -f transit/keys/jwt-signer
fi

# [COMMENT]: Cấu hình tự động rotate khóa ký 'jwt-signer' sau mỗi 30 ngày (an toàn bảo mật Cloud-Native)
echo "Cấu hình tự động xoay vòng khóa (Auto Rotation - 30 ngày) cho 'jwt-signer'..."
docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$DEFAULT_ROOT_TOKEN" "$VAULT_CONTAINER" \
  vault write transit/keys/jwt-signer/config auto_rotate_period="30d"

echo "=== [6/6] Tạo Secret Engine và ghi dữ liệu mẫu (KV Engine) ==="
# [COMMENT]: Kích hoạt Key-Value Version 2 (KV-v2) engine tại path 'secret' nếu chưa có
KV_ENABLED=$(docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$DEFAULT_ROOT_TOKEN" "$VAULT_CONTAINER" vault secrets list -format=json 2>/dev/null | jq -e '."secret/"' >/dev/null 2>&1 && echo "true" || echo "false")
if [ "$KV_ENABLED" = "false" ]; then
  echo "Đang kích hoạt KV-v2 Secrets Engine..."
  docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$DEFAULT_ROOT_TOKEN" "$VAULT_CONTAINER" vault secrets enable -path=secret kv-v2
fi

# [COMMENT]: Cấu hình metadata giới hạn tối đa 2 phiên bản cho secret/admin/api-key
echo "Cấu hình metadata max-versions=2 cho secret/admin/api-key..."
docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$DEFAULT_ROOT_TOKEN" "$VAULT_CONTAINER" \
  vault kv metadata put -max-versions=2 secret/admin/api-key || true

# [COMMENT]: Thực hiện lưu trữ thông tin mật mã mẫu vào Vault KV
# Đây là file config tạo secret mà hệ thống cần dùng (ví dụ: DB Password, SMTP, OTP...)
echo "Đang tạo mật khẩu và cấu hình nhạy cảm mẫu vào 'secret/controlplane'..."
docker exec -e VAULT_ADDR="http://127.0.0.1:8200" -e VAULT_TOKEN="$DEFAULT_ROOT_TOKEN" "$VAULT_CONTAINER" \
  vault kv put secret/controlplane \
    database_password="postgres_dev_password" \
    smtp_password="smtp_dev_password" \
    master_signing_key="super_secret_master_signing_key_512"

echo "============================================================================"
echo "🎉 HOÀN THÀNH BOOTSTRAP VAULT & KHỞI TẠO SECRETS THÀNH CÔNG!"
echo "============================================================================"
echo "👉 Vault Address: http://localhost:8200"
echo "👉 Static Token: $DEFAULT_ROOT_TOKEN"
echo "👉 Transit Key: jwt-signer (Auto-rotate: 30 days)"
echo "👉 Sample Secret written to: secret/controlplane"
echo "============================================================================"
