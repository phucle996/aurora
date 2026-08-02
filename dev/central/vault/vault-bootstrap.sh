#!/usr/bin/env bash

# ============================================================================
# 🔒 HashiCorp Vault Bootstrap Script
# ============================================================================
#
# Bootstrap Vault sau khi đã Init + Unseal.
#
# Chức năng:
#   - Enable Transit Engine
#   - Tạo và cấu hình auto-rotation cho các Transit key
#   - Enable KV-v2
#   - Seed connection/integration records theo capability
#   - Tạo policy và AppRole riêng cho từng workload
#   - Seed/rotate complete Google và GitHub OAuth provider records
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
OAUTH_ONLY=false

##############################################################################
# Usage
##############################################################################

usage() {
cat <<EOF
Usage:
    $0 -t <root-token> [-a <vault-address>] [-o]

Options:
    -t TOKEN      Vault Root Token (Required)
    -a ADDRESS    Vault Address (Default: http://localhost:8200)
    -o            Only write OAuth provider records; KV-v2 must already exist
    -h            Show help

Example:
    $0 -t hvs.xxxxxxxxxxxxxxxxx

    $0 \\
       -t hvs.xxxxxxxxxxxxxxxxx \\
       -a http://192.168.1.100:8200

    VAULT_ACR_OAUTH_GITHUB_CLIENT_ID='<client-id>' \\
    VAULT_ACR_OAUTH_GITHUB_CLIENT_SECRET='<client-secret>' \\
    $0 -o -t hvs.xxxxxxxxxxxxxxxxx

OAuth callback URL and scope can be overridden with:
    VAULT_ACR_OAUTH_GOOGLE_CALLBACK_URL
    VAULT_ACR_OAUTH_GOOGLE_SCOPE
    VAULT_ACR_OAUTH_GITHUB_CALLBACK_URL
    VAULT_ACR_OAUTH_GITHUB_SCOPE
EOF
}

##############################################################################
# Parse Arguments
##############################################################################

while getopts ":t:a:oh" opt
do
    case "$opt" in

        t)
            VAULT_TOKEN="$OPTARG"
            ;;

        a)
            VAULT_ADDR="$OPTARG"
            ;;

        o)
            OAUTH_ONLY=true
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

if [[ "$OAUTH_ONLY" == "true" \
    && -z "${VAULT_ACR_OAUTH_GOOGLE_CLIENT_ID:-}" \
    && -z "${VAULT_ACR_OAUTH_GOOGLE_CLIENT_SECRET:-}" \
    && -z "${VAULT_ACR_OAUTH_GITHUB_CLIENT_ID:-}" \
    && -z "${VAULT_ACR_OAUTH_GITHUB_CLIENT_SECRET:-}" ]]; then
    echo "ERROR: OAuth-only mode requires Google or GitHub client credentials." >&2
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
# REST Helper
##############################################################################

request() {
    local METHOD="$1"
    local REQ_PATH="$2"
    local BODY="${3:-}"

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
    
    if [[ "$HTTP_CODE" -ge 400 ]]; then
        if [[ "$HTTP_CODE" -eq 400 &&
            ( "$REQ_PATH" == "sys/mounts/transit" ||
              "$REQ_PATH" == "sys/mounts/secret" ||
              "$REQ_PATH" == "sys/mounts/totp" ||
              "$REQ_PATH" == "sys/auth/approle" ||
              "$REQ_PATH" == "transit/keys/jwt-signer" ||
              "$REQ_PATH" == "transit/keys/zone-control-assertion" ||
              "$REQ_PATH" == "transit/keys/iam-mfa-secret" ) ]]; then
            echo "Vault engine or key already exists: $REQ_PATH"
            return 0
        fi
        # Never print Vault response bodies: an error payload can contain
        # secret-engine material. The caller decides whether an existing mount
        # is acceptable; writes below use PUT and are naturally idempotent.
        echo "Vault request failed: $METHOD /v1/$REQ_PATH (HTTP $HTTP_CODE)" >&2
        return 1
    fi
    echo "Vault request ok: $METHOD /v1/$REQ_PATH (HTTP $HTTP_CODE)"
}

write_kv() {
    local REQ_PATH="$1"
    local BODY="$2"
    request PUT "$REQ_PATH" "$BODY"
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

if [[ "$OAUTH_ONLY" != "true" ]]; then

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
# Create the asymmetric Zone Control Edge assertion key.
##############################################################################

# [COMMENT]: The private Ed25519 material stays inside Vault. Deployments must
# distribute only the versioned public key returned by Transit to each Zone.
request \
POST \
transit/keys/zone-control-assertion \
'{
    "type":"ed25519"
}'

# Purpose-scoped symmetric key. Raw material never leaves Transit; old versions
# remain usable for decrypt after rotation.
request POST transit/keys/iam-mfa-secret '{}'
request POST transit/keys/iam-mfa-secret/config '{"auto_rotate_period":"30d"}'

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

# [DEV-ONLY]: Seed atomic, consumer-neutral connection records. Production
# operators must provide these values through a secure provisioning pipeline;
# applications never receive them in ConfigMap/Secret or DSN environment vars.
CONTROLPLANE_PSQL_HOST="${VAULT_CONTROLPLANE_PSQL_HOST:-pbouncer}"
CONTROLPLANE_PSQL_PORT="${VAULT_CONTROLPLANE_PSQL_PORT:-6432}"
CONTROLPLANE_PSQL_USER="${VAULT_CONTROLPLANE_PSQL_USER:-postgres}"
CONTROLPLANE_PSQL_PASSWORD="${VAULT_CONTROLPLANE_PSQL_PASSWORD:-postgres}"
CONTROLPLANE_PSQL_DATABASE="${VAULT_CONTROLPLANE_PSQL_DATABASE:-controlplane}"
CONTROLPLANE_REDIS_ADDR="${VAULT_CONTROLPLANE_REDIS_ADDR:-cp-redis:6379}"
CONTROLPLANE_AUTH_REDIS_ADDR="${VAULT_CONTROLPLANE_AUTH_REDIS_ADDR:-acr-redis:6379}"
CONTROLPLANE_AUTH_REDIS_USER="${VAULT_CONTROLPLANE_AUTH_REDIS_USER:-controlplane}"
CONTROLPLANE_AUTH_REDIS_PASSWORD="${VAULT_CONTROLPLANE_AUTH_REDIS_PASSWORD:-aurora-dev-controlplane-secret}"

write_kv secret/data/connections/postgres/pg-central/role-business-rw "$(jq -n \
    --arg host "$CONTROLPLANE_PSQL_HOST" \
    --argjson port "$CONTROLPLANE_PSQL_PORT" \
    --arg username "$CONTROLPLANE_PSQL_USER" \
    --arg password "$CONTROLPLANE_PSQL_PASSWORD" \
    --arg database "$CONTROLPLANE_PSQL_DATABASE" \
    '{data:{schema_version:1,host:$host,port:$port,username:$username,password:$password,database:$database,ssl_mode:"disable",tls_enabled:false}}')"

write_kv secret/data/connections/redis/shared-l2/role-request-reply-rw "$(jq -n \
    --arg redis_addr "$CONTROLPLANE_REDIS_ADDR" \
    '{data:{schema_version:1,addr:$redis_addr,username:"",password:"",db:0}}')"

write_kv secret/data/connections/redis/auth-state/role-authz-projection-rw "$(jq -n \
    --arg addr "$CONTROLPLANE_AUTH_REDIS_ADDR" \
    --arg username "$CONTROLPLANE_AUTH_REDIS_USER" \
    --arg password "$CONTROLPLANE_AUTH_REDIS_PASSWORD" \
    '{data:{schema_version:1,addr:$addr,username:$username,password:$password,db:0}}')"

COST_PSQL_HOST="${VAULT_COST_PSQL_HOST:-billing-psql}"
COST_PSQL_PORT="${VAULT_COST_PSQL_PORT:-5432}"
COST_PSQL_USER="${VAULT_COST_PSQL_USER:-billing_admin}"
COST_PSQL_PASSWORD="${VAULT_COST_PSQL_PASSWORD:-billing_secure_password}"
COST_PSQL_DATABASE="${VAULT_COST_PSQL_DATABASE:-billing}"
COST_SHARED_REDIS_ADDR="${VAULT_COST_SHARED_REDIS_ADDR:-cp-redis:6379}"
COST_AUTH_REDIS_ADDR="${VAULT_COST_AUTH_REDIS_ADDR:-acr-redis:6379}"
COST_AUTH_REDIS_USER="${VAULT_COST_AUTH_REDIS_USER:-cost}"
COST_AUTH_REDIS_PASSWORD="${VAULT_COST_AUTH_REDIS_PASSWORD:-aurora-dev-cost-secret}"

write_kv secret/data/connections/postgres/pg-billing/role-ledger-rw "$(jq -n \
    --arg host "$COST_PSQL_HOST" --argjson port "$COST_PSQL_PORT" \
    --arg username "$COST_PSQL_USER" --arg password "$COST_PSQL_PASSWORD" \
    --arg database "$COST_PSQL_DATABASE" \
    '{data:{schema_version:1,host:$host,port:$port,username:$username,password:$password,database:$database,ssl_mode:"disable",tls_enabled:false}}')"

write_kv secret/data/connections/redis/shared-l2/role-wallet-command-rw "$(jq -n \
    --arg addr "$COST_SHARED_REDIS_ADDR" \
    '{data:{schema_version:1,addr:$addr,username:"",password:"",db:0}}')"

write_kv secret/data/connections/redis/auth-state/role-proof-rw "$(jq -n \
    --arg addr "$COST_AUTH_REDIS_ADDR" --arg username "$COST_AUTH_REDIS_USER" \
    --arg password "$COST_AUTH_REDIS_PASSWORD" \
    '{data:{schema_version:1,addr:$addr,username:$username,password:$password,db:0}}')"

JO_DATABASE_URL="${VAULT_JO_DATABASE_URL:-postgres://postgres:postgres@psql:5432/controlplane?sslmode=disable}"
JO_SHARED_REDIS_URL="${VAULT_JO_SHARED_REDIS_URL:-redis://cp-redis:6379/0}"
ENGINE_DATABASE_URL="${VAULT_ENGINE_DATABASE_URL:-postgres://billing_admin:billing_secure_password@billing-psql:5432/billing?sslmode=disable}"
ENGINE_REDIS_URL="${VAULT_ENGINE_REDIS_URL:-redis://cp-redis:6379/0}"
NOTIFICATION_SHARED_REDIS_URL="${VAULT_NOTIFICATION_SHARED_REDIS_URL:-redis://cp-redis:6379/0}"
ACR_AUTH_REDIS_URL="${VAULT_ACR_AUTH_REDIS_URL:-redis://acr:aurora-dev-acr-secret@acr-redis:6379/0}"
ACR_SHARED_REDIS_URL="${VAULT_ACR_SHARED_REDIS_URL:-redis://cp-redis:6379/0}"

write_kv secret/data/connections/postgres/pg-central/role-cdc-read "$(jq -n \
    --arg url "$JO_DATABASE_URL" \
    '{data:{schema_version:1,database_url:$url}}')"
# [DEV-ONLY]: This uses the same local DSN as the read record so the compose
# stack remains runnable. Production must replace it with a PostgreSQL role
# that can update only managed_service_outbox_records.status/updated_at.
write_kv secret/data/connections/postgres/pg-central/role-job-dispatch-rw "$(jq -n \
    --arg url "$JO_DATABASE_URL" \
    '{data:{schema_version:1,database_url:$url}}')"
# [DEV-ONLY]: Compose shares the local superuser DSN. Production grants this
# capability only the existing workflow result CTE tables; it must not inherit
# logical-replication or arbitrary Controlplane business writes.
write_kv secret/data/connections/postgres/pg-central/role-job-result-rw "$(jq -n \
    --arg url "$JO_DATABASE_URL" \
    '{data:{schema_version:1,database_url:$url}}')"
write_kv secret/data/connections/redis/shared-l2/role-runtime-bridge-rw "$(jq -n \
    --arg url "$JO_SHARED_REDIS_URL" \
    '{data:{schema_version:1,url:$url}}')"
write_kv secret/data/connections/postgres/pg-billing/role-engine-read "$(jq -n \
    --arg url "$ENGINE_DATABASE_URL" \
    '{data:{schema_version:1,database_url:$url}}')"
write_kv secret/data/connections/redis/engine/role-checkpoint-lock-rw "$(jq -n \
    --arg url "$ENGINE_REDIS_URL" \
    '{data:{schema_version:1,url:$url}}')"
write_kv secret/data/connections/redis/shared-l2/role-notification-consume "$(jq -n \
    --arg url "$NOTIFICATION_SHARED_REDIS_URL" \
    '{data:{schema_version:1,url:$url}}')"
write_kv secret/data/connections/redis/auth-state/role-session-rw "$(jq -n \
    --arg url "$ACR_AUTH_REDIS_URL" \
    '{data:{schema_version:1,url:$url}}')"
write_kv secret/data/connections/redis/shared-l2/role-auth-request-rw "$(jq -n \
    --arg url "$ACR_SHARED_REDIS_URL" \
    '{data:{schema_version:1,url:$url}}')"

# [DEV-ONLY]: These are Central transport records. Job Orchestrator is the
# only Central workload that consumes the Kafka/NATS Core capability here;
# Controlplane's synthetic mail adapter intentionally keeps its Kafka wiring
# in controlplane/.env. Dataplane is Zone-local and cannot read Vault, so its
# Kafka/NATS/Zone-NATS values remain in the Zone app environment.
JO_KAFKA_BOOTSTRAP_SERVERS="${VAULT_JO_KAFKA_BOOTSTRAP_SERVERS:-kafka-1:9092}"
JO_KAFKA_SECURITY_PROTOCOL="${VAULT_JO_KAFKA_SECURITY_PROTOCOL:-plaintext}"
JO_KAFKA_CLIENT_ID="${VAULT_JO_KAFKA_CLIENT_ID:-aurora-job-orchestrator}"
JO_KAFKA_USERNAME="${VAULT_JO_KAFKA_USERNAME:-}"
JO_KAFKA_PASSWORD="${VAULT_JO_KAFKA_PASSWORD:-}"
JO_KAFKA_TLS_ENABLED="${VAULT_JO_KAFKA_TLS_ENABLED:-false}"
JO_KAFKA_TLS_TRUST_SOURCE="${VAULT_JO_KAFKA_TLS_TRUST_SOURCE:-}"
JO_KAFKA_CA_CERT_PATH="${VAULT_JO_KAFKA_CA_CERT_PATH:-}"
JO_KAFKA_CLIENT_CERT_PATH="${VAULT_JO_KAFKA_CLIENT_CERT_PATH:-}"
JO_KAFKA_CLIENT_KEY_PATH="${VAULT_JO_KAFKA_CLIENT_KEY_PATH:-}"
JO_KAFKA_SERVER_NAME="${VAULT_JO_KAFKA_SERVER_NAME:-}"

write_kv secret/data/connections/kafka/central/role-job-orchestrator "$(jq -n \
    --arg bootstrap_servers "$JO_KAFKA_BOOTSTRAP_SERVERS" \
    --arg security_protocol "$JO_KAFKA_SECURITY_PROTOCOL" \
    --arg client_id "$JO_KAFKA_CLIENT_ID" \
    --arg username "$JO_KAFKA_USERNAME" \
    --arg password "$JO_KAFKA_PASSWORD" \
    --argjson tls_enabled "$JO_KAFKA_TLS_ENABLED" \
    --arg tls_trust_source "$JO_KAFKA_TLS_TRUST_SOURCE" \
    --arg ca_cert_path "$JO_KAFKA_CA_CERT_PATH" \
    --arg client_cert_path "$JO_KAFKA_CLIENT_CERT_PATH" \
    --arg client_key_path "$JO_KAFKA_CLIENT_KEY_PATH" \
    --arg server_name "$JO_KAFKA_SERVER_NAME" \
    '{
        data:{
            schema_version:1,
            bootstrap_servers:($bootstrap_servers|split(",")|map(select(length > 0))),
            security_protocol:$security_protocol,
            client_id:$client_id,
            username:(if $username == "" then null else $username end),
            password:(if $password == "" then null else $password end),
            tls_enabled:$tls_enabled,
            tls_trust_source:(if $tls_trust_source == "" then null else $tls_trust_source end),
            ca_cert_path:(if $ca_cert_path == "" then null else $ca_cert_path end),
            client_cert_path:(if $client_cert_path == "" then null else $client_cert_path end),
            client_key_path:(if $client_key_path == "" then null else $client_key_path end),
            server_name:(if $server_name == "" then null else $server_name end)
        }
    }')"

JO_NATS_URLS="${VAULT_JO_NATS_URLS:-nats://nats:4222}"
JO_NATS_CLIENT_NAME="${VAULT_JO_NATS_CLIENT_NAME:-aurora-job-orchestrator}"
JO_NATS_AUTH_MODE="${VAULT_JO_NATS_AUTH_MODE:-none}"
JO_NATS_TOKEN="${VAULT_JO_NATS_TOKEN:-}"
JO_NATS_USERNAME="${VAULT_JO_NATS_USERNAME:-}"
JO_NATS_PASSWORD="${VAULT_JO_NATS_PASSWORD:-}"
JO_NATS_CREDENTIALS_FILE="${VAULT_JO_NATS_CREDENTIALS_FILE:-}"
JO_NATS_TLS_ENABLED="${VAULT_JO_NATS_TLS_ENABLED:-false}"
JO_NATS_TLS_TRUST_SOURCE="${VAULT_JO_NATS_TLS_TRUST_SOURCE:-}"
JO_NATS_CA_CERT_PATH="${VAULT_JO_NATS_CA_CERT_PATH:-}"
JO_NATS_CLIENT_CERT_PATH="${VAULT_JO_NATS_CLIENT_CERT_PATH:-}"
JO_NATS_CLIENT_KEY_PATH="${VAULT_JO_NATS_CLIENT_KEY_PATH:-}"
JO_NATS_TLS_FIRST="${VAULT_JO_NATS_TLS_FIRST:-false}"

write_kv secret/data/connections/nats/central/role-job-orchestrator "$(jq -n \
    --arg urls "$JO_NATS_URLS" \
    --arg client_name "$JO_NATS_CLIENT_NAME" \
    --arg auth_mode "$JO_NATS_AUTH_MODE" \
    --arg token "$JO_NATS_TOKEN" \
    --arg username "$JO_NATS_USERNAME" \
    --arg password "$JO_NATS_PASSWORD" \
    --arg credentials_file "$JO_NATS_CREDENTIALS_FILE" \
    --argjson tls_enabled "$JO_NATS_TLS_ENABLED" \
    --arg tls_trust_source "$JO_NATS_TLS_TRUST_SOURCE" \
    --arg ca_cert_path "$JO_NATS_CA_CERT_PATH" \
    --arg client_cert_path "$JO_NATS_CLIENT_CERT_PATH" \
    --arg client_key_path "$JO_NATS_CLIENT_KEY_PATH" \
    --argjson tls_first "$JO_NATS_TLS_FIRST" \
    '{
        data:{
            schema_version:1,
            urls:($urls|split(",")|map(select(length > 0))),
            client_name:$client_name,
            auth_mode:$auth_mode,
            token:(if $token == "" then null else $token end),
            username:(if $username == "" then null else $username end),
            password:(if $password == "" then null else $password end),
            credentials_file:(if $credentials_file == "" then null else $credentials_file end),
            tls_enabled:$tls_enabled,
            tls_trust_source:(if $tls_trust_source == "" then null else $tls_trust_source end),
            ca_cert_path:(if $ca_cert_path == "" then null else $ca_cert_path end),
            client_cert_path:(if $client_cert_path == "" then null else $client_cert_path end),
            client_key_path:(if $client_key_path == "" then null else $client_key_path end),
            tls_first:$tls_first
        }
    }')"

NOTIFICATION_SCYLLA_CONTACT_POINTS="${VAULT_NOTIFICATION_SCYLLA_CONTACT_POINTS:-scylla:9042}"
NOTIFICATION_SCYLLA_LOCAL_DC="${VAULT_NOTIFICATION_SCYLLA_LOCAL_DC:-datacenter1}"
NOTIFICATION_SCYLLA_KEYSPACE="${VAULT_NOTIFICATION_SCYLLA_KEYSPACE:-aurora_timeline}"
NOTIFICATION_SCYLLA_USERNAME="${VAULT_NOTIFICATION_SCYLLA_USERNAME:-timeline_service}"
NOTIFICATION_SCYLLA_PASSWORD="${VAULT_NOTIFICATION_SCYLLA_PASSWORD:-timeline-dev-password}"
NOTIFICATION_SCYLLA_TLS_MODE="${VAULT_NOTIFICATION_SCYLLA_TLS_MODE:-disabled}"
NOTIFICATION_SCYLLA_CA_CERT_PATH="${VAULT_NOTIFICATION_SCYLLA_CA_CERT_PATH:-}"
NOTIFICATION_SCYLLA_CLIENT_CERT_PATH="${VAULT_NOTIFICATION_SCYLLA_CLIENT_CERT_PATH:-}"
NOTIFICATION_SCYLLA_CLIENT_KEY_PATH="${VAULT_NOTIFICATION_SCYLLA_CLIENT_KEY_PATH:-}"

write_kv secret/data/connections/scylla/central/role-notification-service "$(jq -n \
    --arg contact_points "$NOTIFICATION_SCYLLA_CONTACT_POINTS" \
    --arg local_dc "$NOTIFICATION_SCYLLA_LOCAL_DC" \
    --arg keyspace "$NOTIFICATION_SCYLLA_KEYSPACE" \
    --arg username "$NOTIFICATION_SCYLLA_USERNAME" \
    --arg password "$NOTIFICATION_SCYLLA_PASSWORD" \
    --arg tls_mode "$NOTIFICATION_SCYLLA_TLS_MODE" \
    --arg ca_cert_path "$NOTIFICATION_SCYLLA_CA_CERT_PATH" \
    --arg client_cert_path "$NOTIFICATION_SCYLLA_CLIENT_CERT_PATH" \
    --arg client_key_path "$NOTIFICATION_SCYLLA_CLIENT_KEY_PATH" \
    '{
        data:{
            schema_version:1,
            contact_points:($contact_points|split(",")|map(select(length > 0))),
            local_dc:$local_dc,
            keyspace:$keyspace,
            username:$username,
            password:$password,
            tls_mode:$tls_mode,
            ca_cert_path:(if $ca_cert_path == "" then null else $ca_cert_path end),
            client_cert_path:(if $client_cert_path == "" then null else $client_cert_path end),
            client_key_path:(if $client_key_path == "" then null else $client_key_path end)
        }
    }')"

# [DEV-ONLY]: Payment HMAC material is provisioned in Vault; the Cost Manager
# process receives only its Vault identity, never these values through env.
PAYMENT_CHECKOUT_SIGNING_SECRET="${VAULT_PAYMENT_CHECKOUT_SIGNING_SECRET:-dev-checkout-signing-key-change-before-production-2026}"
PAYMENT_WEBHOOK_SIGNING_SECRET="${VAULT_PAYMENT_WEBHOOK_SIGNING_SECRET:-dev-webhook-signing-key-change-before-production-2026}"
write_kv secret/data/integrations/payment/cost-manager-api "$(jq -n \
    --arg checkout "$PAYMENT_CHECKOUT_SIGNING_SECRET" \
    --arg webhook "$PAYMENT_WEBHOOK_SIGNING_SECRET" \
    '{data:{schema_version:1,checkout_signing_secret:$checkout,webhook_signing_secret:$webhook}}')"

# Runtime policies are capability-scoped. The record itself has no consumer
# metadata; Vault auth identity is the isolation and audit boundary.
write_policy() {
    local NAME="$1"
    local POLICY="$2"
    request PUT "sys/policies/acl/$NAME" "$(jq -n --arg policy "$POLICY" '{policy:$policy}')"
}

write_policy controlplane-connections-read \
'path "secret/data/connections/postgres/pg-central/role-business-rw" { capabilities = ["read"] }
path "secret/data/connections/redis/shared-l2/role-request-reply-rw" { capabilities = ["read"] }
path "secret/data/connections/redis/auth-state/role-authz-projection-rw" { capabilities = ["read"] }
path "transit/encrypt/iam-mfa-secret" { capabilities = ["update"] }
path "transit/decrypt/iam-mfa-secret" { capabilities = ["update"] }'

write_policy acr-connections-read \
'path "secret/data/connections/redis/auth-state/role-session-rw" { capabilities = ["read"] }
path "secret/data/connections/redis/shared-l2/role-auth-request-rw" { capabilities = ["read"] }
path "secret/data/acr/oauth/*" { capabilities = ["read"] }
path "secret/data/admin/api-key" { capabilities = ["read"] }
path "transit/hmac/jwt-signer" { capabilities = ["update"] }
path "transit/verify/jwt-signer" { capabilities = ["update"] }
path "transit/sign/zone-control-assertion" { capabilities = ["update"] }
path "totp/keys/admin" { capabilities = ["read"] }
path "totp/code/admin" { capabilities = ["update"] }'

write_policy job-orchestrator-connections-read \
'path "secret/data/connections/postgres/pg-central/role-cdc-read" { capabilities = ["read"] }
path "secret/data/connections/postgres/pg-central/role-job-dispatch-rw" { capabilities = ["read"] }
path "secret/data/connections/postgres/pg-central/role-job-result-rw" { capabilities = ["read"] }
path "secret/data/connections/redis/shared-l2/role-runtime-bridge-rw" { capabilities = ["read"] }
path "secret/data/connections/kafka/central/role-job-orchestrator" { capabilities = ["read"] }
path "secret/data/connections/nats/central/role-job-orchestrator" { capabilities = ["read"] }'

write_policy cost-manager-api-connections-read \
'path "secret/data/connections/postgres/pg-billing/role-ledger-rw" { capabilities = ["read"] }
path "secret/data/connections/redis/shared-l2/role-wallet-command-rw" { capabilities = ["read"] }
path "secret/data/connections/redis/auth-state/role-proof-rw" { capabilities = ["read"] }
path "secret/data/integrations/payment/cost-manager-api" { capabilities = ["read"] }'

write_policy cost-manager-engine-connections-read \
'path "secret/data/connections/postgres/pg-billing/role-engine-read" { capabilities = ["read"] }
path "secret/data/connections/redis/engine/role-checkpoint-lock-rw" { capabilities = ["read"] }'

write_policy notification-service-connections-read \
'path "secret/data/connections/redis/shared-l2/role-notification-consume" { capabilities = ["read"] }
path "secret/data/connections/scylla/central/role-notification-service" { capabilities = ["read"] }'

# AppRole is the local/dev equivalent of Kubernetes auth. Each role is
# independently policy-bound; operators retrieve role_id/secret_id out of band
# and inject them only into the matching workload.
request POST sys/auth/approle \
'{"type":"approle","description":"per-application runtime identities"}'

write_approle() {
    local NAME="$1"
    local POLICY="$2"
    request POST "auth/approle/role/$NAME" "$(jq -n \
        --arg policy "$POLICY" \
        '{token_policies:[$policy],token_ttl:"1h",token_max_ttl:"24h",secret_id_ttl:"24h"}')"
}

write_approle controlplane controlplane-connections-read
write_approle acr acr-connections-read
write_approle job-orchestrator job-orchestrator-connections-read
write_approle cost-manager-api cost-manager-api-connections-read
write_approle cost-manager-engine cost-manager-engine-connections-read
write_approle notification-service notification-service-connections-read

create_static_token() {
    local TOKEN_ID="$1"
    local POLICY="$2"
    request POST "auth/token/revoke" "$(jq -n --arg id "$TOKEN_ID" '{token:$id}')" >/dev/null 2>&1 || true
    request POST "auth/token/create" "$(jq -n \
        --arg id "$TOKEN_ID" \
        --arg policy "$POLICY" \
        '{id:$id,policies:[$policy],ttl:"0s",renewable:false}')" || true
}

# [DEV-ONLY]: Tạo Static Tokens cố định cho từng Service với Policy độc lập
create_static_token "root" "root"
create_static_token "aurora-dev-root-token" "root"
create_static_token "aurora-dev-controlplane-token" "controlplane-connections-read"
create_static_token "aurora-dev-acr-token" "acr-connections-read"
create_static_token "aurora-dev-job-orchestrator-token" "job-orchestrator-connections-read"
create_static_token "aurora-dev-cost-manager-api-token" "cost-manager-api-connections-read"
create_static_token "aurora-dev-cost-manager-engine-token" "cost-manager-engine-connections-read"
create_static_token "aurora-dev-notification-token" "notification-service-connections-read"

# [COMMENT]: Khởi tạo SRE Admin API Key dùng để đăng nhập tại Biên (Rust acr)
write_kv secret/data/admin/api-key \
'{
    "data":{
        "api_key":"sre_admin_secret_api_key_2026"
    }
}'

fi

# [COMMENT]: OAuth provider configuration is supplied only to this bootstrap
# process and is written to Vault KV; never place client secrets in the ACR
# container environment or in a tracked file. A partially supplied record is
# rejected so an enabled provider cannot start with an incomplete contract.
if [[ -n "${VAULT_ACR_OAUTH_GOOGLE_CLIENT_ID:-}" \
    || -n "${VAULT_ACR_OAUTH_GOOGLE_CLIENT_SECRET:-}" \
    || -n "${VAULT_ACR_OAUTH_GOOGLE_CALLBACK_URL:-}" \
    || -n "${VAULT_ACR_OAUTH_GOOGLE_SCOPE:-}" ]]; then
    : "${VAULT_ACR_OAUTH_GOOGLE_CLIENT_ID:?Google OAuth client id is required when seeding Google}"
    : "${VAULT_ACR_OAUTH_GOOGLE_CLIENT_SECRET:?Google OAuth client secret is required when seeding Google}"
    # [DEV-ONLY]: Google accepts localhost redirect URIs without a registered
    # public domain. Production must override this with the verified HTTPS
    # Cloud Console domain.
    GOOGLE_CALLBACK_URL="${VAULT_ACR_OAUTH_GOOGLE_CALLBACK_URL:-https://localhost/api/v1/auth/oauth/google/callback}"
    GOOGLE_SCOPE="${VAULT_ACR_OAUTH_GOOGLE_SCOPE:-openid email profile}"
    write_kv secret/data/acr/oauth/google \
    "$(jq -n \
        --arg client_id "$VAULT_ACR_OAUTH_GOOGLE_CLIENT_ID" \
        --arg client_secret "$VAULT_ACR_OAUTH_GOOGLE_CLIENT_SECRET" \
        --arg callback_url "$GOOGLE_CALLBACK_URL" \
        --arg scope "$GOOGLE_SCOPE" \
        '{data:{callback_url:$callback_url,client_id:$client_id,client_secret:$client_secret,scope:$scope}}')"
fi

# [COMMENT]: GitHub uses the same complete record contract as Google.
if [[ -n "${VAULT_ACR_OAUTH_GITHUB_CLIENT_ID:-}" \
    || -n "${VAULT_ACR_OAUTH_GITHUB_CLIENT_SECRET:-}" \
    || -n "${VAULT_ACR_OAUTH_GITHUB_CALLBACK_URL:-}" \
    || -n "${VAULT_ACR_OAUTH_GITHUB_SCOPE:-}" ]]; then
    : "${VAULT_ACR_OAUTH_GITHUB_CLIENT_ID:?GitHub OAuth client id is required when seeding GitHub}"
    : "${VAULT_ACR_OAUTH_GITHUB_CLIENT_SECRET:?GitHub OAuth client secret is required when seeding GitHub}"
    # [DEV-ONLY]: Keep GitHub on the same localhost browser origin while the
    # dev Cloud Console is served through Envoy. Production must override it.
    GITHUB_CALLBACK_URL="${VAULT_ACR_OAUTH_GITHUB_CALLBACK_URL:-https://localhost/api/v1/auth/oauth/github/callback}"
    GITHUB_SCOPE="${VAULT_ACR_OAUTH_GITHUB_SCOPE:-read:user user:email}"
    write_kv secret/data/acr/oauth/github \
    "$(jq -n \
        --arg client_id "$VAULT_ACR_OAUTH_GITHUB_CLIENT_ID" \
        --arg client_secret "$VAULT_ACR_OAUTH_GITHUB_CLIENT_SECRET" \
        --arg callback_url "$GITHUB_CALLBACK_URL" \
        --arg scope "$GITHUB_SCOPE" \
        '{data:{callback_url:$callback_url,client_id:$client_id,client_secret:$client_secret,scope:$scope}}')"
fi

if [[ "$OAUTH_ONLY" == "true" ]]; then
    echo "OAuth provider bootstrap completed."
    exit 0
fi

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
echo "Transit Keys  : jwt-signer, zone-control-assertion, iam-mfa-secret"
echo "Runtime ACLs  : one policy and AppRole per Central workload"
echo
