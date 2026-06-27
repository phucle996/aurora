#!/usr/bin/env bash

# ============================================================================
# 🔒 HashiCorp Vault Bootstrap Script (Fixed Variable Shadowing)
# ============================================================================
#
# Bootstrap Vault sau khi đã Init + Unseal.
#
# Chức năng:
#   - Enable Transit Engine
#   - Tạo Transit Key: jwt-signer
#   - Cấu hình Auto Rotation
#   - Enable KV-v2
#   - Cấu hình Metadata
#   - Tạo Secrets mẫu
#
# Chỉ sử dụng Vault HTTP REST API.
#
# ============================================================================

set -Eeuo pipefail

##############################################################################
# Default Configuration
##############################################################################

VAULT_ADDR="http://localhost:8200"
VAULT_TOKEN=""

##############################################################################
# Usage
##############################################################################

usage() {
cat <<EOF
Usage:
    $0 -t <root-token> [-a <vault-address>]

Options:
    -t TOKEN      Vault Root Token (Required)
    -a ADDRESS    Vault Address (Default: http://localhost:8200)
    -h            Show help

Example:
    $0 -t hvs.xxxxxxxxxxxxxxxxx

    $0 \\
       -t hvs.xxxxxxxxxxxxxxxxx \\
       -a http://192.168.1.100:8200
EOF
}

##############################################################################
# Parse Arguments
##############################################################################

while getopts ":t:a:h" opt
do
    case "$opt" in

        t)
            VAULT_TOKEN="$OPTARG"
            ;;

        a)
            VAULT_ADDR="$OPTARG"
            ;;

        h)
            usage
            exit 0
            ;;

        :)
            echo "ERROR: Option -$OPTARG requires a value."
            exit 1
            ;;

        \?)
            echo "ERROR: Unknown option -$OPTARG"
            usage
            exit 1
            ;;

    esac
done

if [[ -z "$VAULT_TOKEN" ]]; then
    echo "ERROR: Vault Root Token is required."
    echo
    usage
    exit 1
fi

##############################################################################
# Check Dependency
##############################################################################

command -v curl >/dev/null || {
    echo "curl not found."
    exit 1
}

command -v jq >/dev/null || {
    echo "jq not found."
    exit 1
}

##############################################################################
# REST Helper (FIXED: Thay PATH bằng REQ_PATH để tránh crash hệ thống)
##############################################################################

request() {
    local METHOD="$1"
    local REQ_PATH="$2"
    local BODY="${3:-}"

    echo "================================================================"
    echo "$METHOD /v1/$REQ_PATH"
    echo "================================================================"

    local RESPONSE
    local HTTP_CODE

    # Tách làm 2 trường hợp rõ ràng để không bao giờ lỗi tham số curl
    if [[ -n "$BODY" ]]; then
        RESPONSE=$(curl \
            --silent \
            --show-error \
            -X "$METHOD" \
            -H "X-Vault-Token: $VAULT_TOKEN" \
            -H "Content-Type: application/json" \
            -d "$BODY" \
            --write-out "\n%{http_code}" \
            "$VAULT_ADDR/v1/$REQ_PATH")
    else
        RESPONSE=$(curl \
            --silent \
            --show-error \
            -X "$METHOD" \
            -H "X-Vault-Token: $VAULT_TOKEN" \
            --write-out "\n%{http_code}" \
            "$VAULT_ADDR/v1/$REQ_PATH")
    fi

    # Tách lấy HTTP Status Code (dòng cuối cùng)
    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    
    # Tách lấy phần JSON Body thực tế (loại bỏ dòng cuối cùng)
    local JSON_BODY
    JSON_BODY=$(echo "$RESPONSE" | sed '$d')

    echo "HTTP Status : $HTTP_CODE"

    # Hiển thị và format dữ liệu JSON trả về nếu có
    if [[ -n "$JSON_BODY" ]]; then
        echo "$JSON_BODY" | jq . 2>/dev/null || echo "$JSON_BODY"
    fi

    echo

    if [[ "$HTTP_CODE" -ge 400 ]]; then
        echo "❌ Request failed."
        return 1
    else
        echo "✅ Success."
    fi
}

##############################################################################
# Wait Vault
##############################################################################

echo "Waiting for Vault..."

until curl --silent "$VAULT_ADDR/v1/sys/health" >/dev/null 2>&1
do
    sleep 2
done

echo "Vault is ready."

##############################################################################
# Enable Transit
##############################################################################

request \
POST \
sys/mounts/transit \
'{
    "type":"transit"
}'

##############################################################################
# Create jwt-signer
##############################################################################

request \
POST \
transit/keys/jwt-signer \
'{}'

##############################################################################
# Configure Auto Rotation
##############################################################################

request \
POST \
transit/keys/jwt-signer/config \
'{
    "auto_rotate_period":"30d"
}'

##############################################################################
# Enable KV-v2
##############################################################################

request \
POST \
sys/mounts/secret \
'{
    "type":"kv",
    "options":{
        "version":"2"
    }
}'

##############################################################################
# Configure Metadata
##############################################################################

request \
POST \
secret/metadata/admin/api-key \
'{
    "max_versions":2
}'

##############################################################################
# Write Secrets
##############################################################################

request \
POST \
secret/data/controlplane \
'{
    "data":{
        "database_password":"postgres_dev_password",
        "smtp_password":"smtp_dev_password",
        "master_signing_key":"super_secret_master_signing_key_512"
    }
}'

# [COMMENT]: Khởi tạo SRE Admin API Key dùng để đăng nhập tại Biên (Rust acr)
request \
POST \
secret/data/admin/api-key \
'{
    "data":{
        "api_key":"sre_admin_secret_api_key_2026"
    }
}'

# [COMMENT]: Kích hoạt TOTP secrets engine cho việc sinh/xác thực mã OTP 2FA
request \
POST \
sys/mounts/totp \
'{
    "type":"totp"
}'

# [COMMENT]: Cấu hình khóa bí mật 2FA static (Base32) để SRE Admin có thể lưu cố định trên app xác thực
request \
POST \
totp/keys/admin \
'{
    "key":"AURORASRE2FASECRETKEY2226BASE32",
    "issuer":"Aurora",
    "account_name":"sre@aurora.local"
}'

##############################################################################
# Finished
##############################################################################

echo
echo "=============================================================="
echo "Vault Bootstrap Completed"
echo "=============================================================="
echo
echo "Vault Address : $VAULT_ADDR"
echo "Transit Key   : jwt-signer"
echo "Secret Path   : secret/controlplane"
echo