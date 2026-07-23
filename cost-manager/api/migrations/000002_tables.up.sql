-- Migration 000002: Khởi tạo tất cả các bảng cơ sở dữ liệu cho billing module

-- 1. Bảng Packs (Gói giải pháp thương mại tổng hợp: Student, Free Tier, Enterprise)
CREATE TABLE IF NOT EXISTS billing.packs (
    id            UUID PRIMARY KEY,
    name          VARCHAR(128) NOT NULL,
    code          VARCHAR(64)  NOT NULL UNIQUE, -- VD: 'STUDENT_PACK', 'FREE_TIER', 'ENTERPRISE_PACK'
    tier_target   VARCHAR(32)  NOT NULL DEFAULT 'FREE_TIER', -- Mục tiêu áp dụng: 'STUDENT', 'FREE_TIER', 'ENTERPRISE'
    monthly_price BIGINT       NOT NULL DEFAULT 0, -- Phí trọn gói cơ bản hàng tháng (USD Micro-units)
    currency      CHAR(3)      NOT NULL DEFAULT 'USD',
    discount_rate NUMERIC(5, 2)  NOT NULL DEFAULT 0.00,  -- Tỷ lệ chiết khấu giảm giá (%)
    status        VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- Trạng thái gói: 'ACTIVE' | 'DEPRECATED'
    description   TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 2. Bảng Tiers (Biểu giá cơ sở chung cho từng loại tài nguyên)
CREATE TABLE IF NOT EXISTS billing.tiers (
    id               UUID PRIMARY KEY,
    name             VARCHAR(128) NOT NULL, -- VD: 'Standard Storage Base Tier'
    code             VARCHAR(64)  NOT NULL, -- VD: 'STORAGE_STD_BASE'
    service_type     billing.service_type NOT NULL, -- Loại tài nguyên
    metadata_version INT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_tier_code_service UNIQUE (code, service_type)
);

-- 3. Bảng Plans (Resource SKU Plans - Lớp vỏ Abstraction liên kết Tiers với từng Zone và hệ số nhân giá vùng)
CREATE TABLE IF NOT EXISTS billing.plans (
    id              UUID PRIMARY KEY,
    name            VARCHAR(128) NOT NULL, -- VD: 'Standard Storage - VN Zone'
    code            VARCHAR(64)  NOT NULL UNIQUE, -- VD: 'STORAGE_SKU_VN'
    service_type    billing.service_type NOT NULL,        -- Loại dịch vụ hỗ trợ query
    zone_id         UUID         NOT NULL,        -- Liên kết tới Zone UUID tương ứng
    tier_id         UUID         NOT NULL REFERENCES billing.tiers(id), -- Tham chiếu tới Tiers chứa biểu giá cơ sở
    zone_multiplier NUMERIC(4, 2) NOT NULL DEFAULT 1.00,  -- Hệ số điều chỉnh giá theo vùng (VD: 1.05 = đắt hơn 5% so với giá base)
    monthly_price   BIGINT       NOT NULL DEFAULT 0, -- Giá thuê bao cố định, USD micro-units
    currency        CHAR(3)      NOT NULL DEFAULT 'USD',
    status          VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- Trạng thái plan: 'ACTIVE' | 'DEPRECATED'
    description     TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 4. Bảng Pack_Plans (Bảng liên kết N:N giữa Pack thương mại và các Resource SKU Plan)
CREATE TABLE IF NOT EXISTS billing.pack_plans (
    id                 UUID PRIMARY KEY,
    pack_id            UUID NOT NULL REFERENCES billing.packs(id) ON DELETE CASCADE, -- Liên kết với gói thương mại
    plan_id            UUID NOT NULL REFERENCES billing.plans(id) ON DELETE RESTRICT, -- Liên kết với plan tài nguyên
    included_quota     NUMERIC(18, 4) NOT NULL DEFAULT 0, -- Hạn mức định lượng miễn phí đi kèm (VD: 10GB storage)
    overage_unit_price BIGINT         NOT NULL DEFAULT 0, -- Giá phụ trội tính khi dùng quá định lượng (USD Micro-units)
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pack_plan UNIQUE (pack_id, plan_id)
);

-- 5. Bảng Subscriptions (Đăng ký / mua gói Pack của các Tenant hoặc tài khoản cá nhân)
CREATE TABLE IF NOT EXISTS billing.subscriptions (
    id              UUID        PRIMARY KEY,
    owner_id        UUID        NOT NULL,
    owner_type      billing.owner_type NOT NULL,
    pack_id         UUID        NOT NULL REFERENCES billing.packs(id), -- Tham chiếu đến Pack đã chọn
    status          VARCHAR(16) NOT NULL DEFAULT 'ACTIVE', -- Trạng thái gói: 'ACTIVE' | 'CANCELLED' | 'EXPIRED'
    version         INT         NOT NULL DEFAULT 1,        -- Cột dùng cho Optimistic Concurrency Control (OCC)
    idempotency_key VARCHAR(128),                           -- Idempotency scope theo owner, không chiếm key của owner khác
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ,
    renewed_at      TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_subscription_owner_idempotency UNIQUE (owner_id, owner_type, idempotency_key),
    CONSTRAINT ck_subscription_window CHECK (expires_at IS NULL OR expires_at > started_at)
);

-- 6. Bảng tier_versions (Immutable Tier pricing versions)
CREATE TABLE IF NOT EXISTS billing.tier_versions (
    id              UUID PRIMARY KEY,
    tier_id         UUID NOT NULL REFERENCES billing.tiers(id) ON DELETE RESTRICT,
    version_number  INT NOT NULL,
    status          VARCHAR(16) NOT NULL,
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_to    TIMESTAMPTZ,
    checksum        VARCHAR(64) NOT NULL,
    change_reason   TEXT NOT NULL,
    created_by      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_tier_version_number UNIQUE (tier_id, version_number),
    CONSTRAINT ck_tier_version_number_positive CHECK (version_number > 0),
    CONSTRAINT ck_tier_version_status CHECK (status IN ('SCHEDULED', 'ACTIVE', 'SUPERSEDED', 'CANCELLED')),
    CONSTRAINT ck_tier_version_effective_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);

-- 7. Bảng tier_version_ranges (Ranges cho từng pricing version)
CREATE TABLE IF NOT EXISTS billing.tier_version_ranges (
    id              UUID PRIMARY KEY,
    tier_version_id UUID NOT NULL REFERENCES billing.tier_versions(id) ON DELETE RESTRICT,
    range_start     BIGINT NOT NULL,
    range_end       BIGINT NOT NULL,
    base_unit_price BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_tier_version_ranges_start_non_negative CHECK (range_start >= 0),
    CONSTRAINT ck_tier_version_ranges_end_after_start CHECK (range_end = 0 OR range_end > range_start),
    CONSTRAINT ck_tier_version_ranges_price_non_negative CHECK (base_unit_price >= 0),
    CONSTRAINT uq_tier_version_range_start UNIQUE (tier_version_id, range_start)
);

-- 8. Bảng pricing_outbox (Outbox cho pricing updates)
CREATE TABLE IF NOT EXISTS billing.pricing_outbox (
    id              UUID PRIMARY KEY,
    event_type      VARCHAR(64) NOT NULL,
    tier_id         UUID NOT NULL REFERENCES billing.tiers(id) ON DELETE RESTRICT,
    tier_version_id UUID NOT NULL REFERENCES billing.tier_versions(id) ON DELETE RESTRICT,
    version_number  INT NOT NULL,
    service_type    billing.service_type NOT NULL,
    effective_from  TIMESTAMPTZ NOT NULL,
    checksum        VARCHAR(64) NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ,
    retry_count     INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    CONSTRAINT ck_pricing_outbox_retry_non_negative CHECK (retry_count >= 0)
);

-- 9. Bảng billing_runs (Nhật ký chu kỳ chạy cước)
CREATE TABLE IF NOT EXISTS billing.billing_runs (
    id                UUID PRIMARY KEY,
    service_type      billing.service_type NOT NULL,
    tier_version_id   UUID NOT NULL REFERENCES billing.tier_versions(id) ON DELETE RESTRICT,
    window_start      TIMESTAMPTZ NOT NULL,
    window_end        TIMESTAMPTZ NOT NULL,
    status            VARCHAR(16) NOT NULL DEFAULT 'RUNNING',
    fencing_token     BIGINT NOT NULL,
    checkpoint        TIMESTAMPTZ,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at      TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_billing_run_window CHECK (window_end > window_start),
    CONSTRAINT ck_billing_run_status CHECK (status IN ('RUNNING', 'RETRYING', 'COMPLETED', 'FAILED'))
);

-- 10. Projection effective-dated của resource ownership. Controlplane là SoT; billing không sửa ownership.
CREATE TABLE IF NOT EXISTS billing.resource_ownership_projection (
    id                UUID PRIMARY KEY,
    resource_type     VARCHAR(32) NOT NULL,
    resource_id       UUID NOT NULL,
    resource_name     VARCHAR(255) NOT NULL,
    owner_id          UUID NOT NULL,
    owner_type        billing.owner_type NOT NULL,
    zone_id           UUID NOT NULL,
    ownership_version INT NOT NULL DEFAULT 1,
    effective_from    TIMESTAMPTZ NOT NULL,
    effective_to      TIMESTAMPTZ,
    source_updated_at TIMESTAMPTZ NOT NULL,
    reconciled_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_resource_ownership_version CHECK (ownership_version > 0),
    CONSTRAINT ck_resource_ownership_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);

-- 11. Binding credential phục vụ audit/reconcile; access key là identifier, không lưu secret/signature.
CREATE TABLE IF NOT EXISTS billing.credential_bindings (
    id             UUID PRIMARY KEY,
    access_key     VARCHAR(255) NOT NULL,
    credential_kind billing.credential_kind NOT NULL DEFAULT 'STATIC',
    resource_type  VARCHAR(32) NOT NULL,
    resource_id    UUID NOT NULL,
    valid_from     TIMESTAMPTZ NOT NULL,
    valid_to       TIMESTAMPTZ,
    status         VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    source_updated_at TIMESTAMPTZ NOT NULL,
    reconciled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_credential_binding_window CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT ck_credential_binding_status CHECK (status IN ('ACTIVE', 'REVOKED', 'EXPIRED'))
);

-- 12. Wallet tiền chính xác theo micro-unit; promo không trộn với cash để expiry/refund có thể audit.
CREATE TABLE IF NOT EXISTS billing.wallets (
    id                    UUID PRIMARY KEY,
    owner_id              UUID NOT NULL,
    owner_type            billing.owner_type NOT NULL,
    currency              CHAR(3) NOT NULL DEFAULT 'USD',
    cash_balance          BIGINT NOT NULL DEFAULT 0,
    promotional_balance   BIGINT NOT NULL DEFAULT 0,
    overdraft_limit       BIGINT NOT NULL DEFAULT 0,
    status                billing.wallet_status NOT NULL DEFAULT 'ACTIVE',
    version               BIGINT NOT NULL DEFAULT 1,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_wallet_owner_currency UNIQUE (owner_id, owner_type, currency),
    CONSTRAINT ck_wallet_currency_upper CHECK (currency = UPPER(currency)),
    CONSTRAINT ck_wallet_promo_non_negative CHECK (promotional_balance >= 0),
    CONSTRAINT ck_wallet_overdraft_non_negative CHECK (overdraft_limit >= 0),
    CONSTRAINT ck_wallet_version_positive CHECK (version > 0)
);

-- 13. Campaign catalog định nghĩa grant; không seed trực tiếp wallet của customer.
CREATE TABLE IF NOT EXISTS billing.promotion_campaigns (
    id                 UUID PRIMARY KEY,
    code               VARCHAR(64) NOT NULL UNIQUE,
    name               VARCHAR(128) NOT NULL,
    amount_micro_units BIGINT NOT NULL,
    currency           CHAR(3) NOT NULL DEFAULT 'USD',
    service_scope      billing.service_type,
    starts_at          TIMESTAMPTZ NOT NULL,
    ends_at            TIMESTAMPTZ,
    status             VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_promotion_amount_positive CHECK (amount_micro_units > 0),
    CONSTRAINT ck_promotion_window CHECK (ends_at IS NULL OR ends_at > starts_at),
    CONSTRAINT ck_promotion_status CHECK (status IN ('ACTIVE', 'PAUSED', 'ENDED'))
);

-- 14. Grant idempotent theo campaign + owner; một retry không thể cộng tiền lần hai.
CREATE TABLE IF NOT EXISTS billing.credit_grants (
    id                 UUID PRIMARY KEY,
    campaign_id        UUID NOT NULL REFERENCES billing.promotion_campaigns(id) ON DELETE RESTRICT,
    wallet_id          UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id           UUID NOT NULL,
    owner_type         billing.owner_type NOT NULL,
    amount_micro_units BIGINT NOT NULL,
    currency           CHAR(3) NOT NULL,
    expires_at         TIMESTAMPTZ,
    idempotency_key    VARCHAR(128) NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_credit_grant_campaign_owner UNIQUE (campaign_id, owner_id, owner_type),
    CONSTRAINT uq_credit_grant_owner_idempotency UNIQUE (owner_id, owner_type, idempotency_key),
    CONSTRAINT ck_credit_grant_amount_positive CHECK (amount_micro_units > 0)
);

-- 15. Resource-plan assignment effective-dated; không tạo cross-service FK tới Controlplane resource.
CREATE TABLE IF NOT EXISTS billing.resource_plan_assignments (
    id                 UUID PRIMARY KEY,
    resource_type      VARCHAR(32) NOT NULL,
    resource_id        UUID NOT NULL,
    subscription_id    UUID NOT NULL REFERENCES billing.subscriptions(id) ON DELETE RESTRICT,
    plan_id            UUID NOT NULL REFERENCES billing.plans(id) ON DELETE RESTRICT,
    entitlement_version INT NOT NULL DEFAULT 1,
    effective_from     TIMESTAMPTZ NOT NULL,
    effective_to       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_resource_plan_version CHECK (entitlement_version > 0),
    CONSTRAINT ck_resource_plan_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);

-- 16. Append-only money ledger. Balance snapshots hỗ trợ reconcile mà không thay đổi lịch sử.
CREATE TABLE IF NOT EXISTS billing.wallet_ledger_entries (
    id                       UUID PRIMARY KEY,
    wallet_id                UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id                 UUID NOT NULL,
    owner_type               billing.owner_type NOT NULL,
    amount_micro_units       BIGINT NOT NULL,
    cash_balance_after       BIGINT NOT NULL,
    promotional_balance_after BIGINT NOT NULL,
    currency                 CHAR(3) NOT NULL,
    entry_type               billing.ledger_entry_type NOT NULL,
    service_type             billing.service_type,
    reference_id             VARCHAR(255) NOT NULL,
    billing_run_id           UUID REFERENCES billing.billing_runs(id) ON DELETE RESTRICT,
    tier_version_id          UUID REFERENCES billing.tier_versions(id) ON DELETE RESTRICT,
    resource_id              UUID,
    resource_type            VARCHAR(32),
    usage_quantity           BIGINT,
    usage_unit               VARCHAR(16),
    description              TEXT NOT NULL,
    occurred_at              TIMESTAMPTZ NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_ledger_amount_non_zero CHECK (amount_micro_units <> 0),
    CONSTRAINT ck_ledger_promo_after_non_negative CHECK (promotional_balance_after >= 0),
    CONSTRAINT ck_ledger_usage_pair CHECK ((usage_quantity IS NULL) = (usage_unit IS NULL))
);

-- 17. Durable queue cho usage chưa rate được; checkpoint có thể tiến mà charge không bị mất.
CREATE TABLE IF NOT EXISTS billing.unrated_usage (
    id                 UUID PRIMARY KEY,
    service_type       billing.service_type NOT NULL,
    resource_type      VARCHAR(32) NOT NULL,
    resource_id        UUID,
    resource_name      VARCHAR(255) NOT NULL,
    access_key         VARCHAR(255),
    metering_hour      TIMESTAMPTZ NOT NULL,
    usage_quantity     BIGINT NOT NULL,
    usage_unit         VARCHAR(16) NOT NULL,
    reason             VARCHAR(64) NOT NULL,
    status             VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    retry_count        INT NOT NULL DEFAULT 0,
    last_error         TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_unrated_usage_non_negative CHECK (usage_quantity >= 0),
    CONSTRAINT ck_unrated_retry_non_negative CHECK (retry_count >= 0),
    CONSTRAINT ck_unrated_status CHECK (status IN ('PENDING', 'PROCESSING', 'RESOLVED', 'DEAD'))
);

-- 18. Inbox idempotency cho ownership lifecycle events nhận từ JetStream (chống trùng lặp event)
CREATE TABLE IF NOT EXISTS billing.ownership_event_inbox (
    event_id        UUID PRIMARY KEY,                      -- UUID duy nhất của event (deterministic từ source job)
    event_type      VARCHAR(32) NOT NULL,                  -- 'RESOURCE_CREATED' hoặc 'RESOURCE_DELETED'
    schema_version  INT NOT NULL DEFAULT 1,                -- Phiên bản schema protobuf
    payload_hash    VARCHAR(64) NOT NULL,                  -- SHA-256 hex kiểm tra tính toàn vẹn payload
    resource_id     UUID NOT NULL,                         -- UUID của tài nguyên (bucket)
    source_version  BIGINT NOT NULL DEFAULT 1,             -- Version ownership từ Controlplane
    status          VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',-- Trạng thái inbox: RECEIVED | APPLIED | DEAD
    error_message   TEXT,                                  -- Thông tin lỗi chi tiết nếu xử lý thất bại
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),    -- Thời điểm nhận tin nhắn từ JetStream
    processed_at    TIMESTAMPTZ,                           -- Thời điểm hoàn tất xử lý event
    CONSTRAINT ck_inbox_status CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD'))
);

-- 19. Resource lifecycle head table để xử lý out-of-order delivery giữa các JetStream events
CREATE TABLE IF NOT EXISTS billing.resource_ownership_head (
    resource_id         UUID PRIMARY KEY,                  -- UUID của tài nguyên (bucket)
    last_source_version BIGINT NOT NULL,                   -- Version ownership mới nhất đã ghi nhận
    resource_state      VARCHAR(16) NOT NULL DEFAULT 'ACTIVE', -- Resource state phục vụ ownership projection
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),-- Thời điểm cập nhật head gần nhất
    CONSTRAINT ck_resource_ownership_state CHECK (resource_state IN ('ACTIVE', 'DELETED'))
);

-- 20. Inbox idempotency cho personal wallet provisioning events nhận từ JetStream
CREATE TABLE IF NOT EXISTS billing.wallet_provision_inbox (
    event_id        UUID PRIMARY KEY,
    schema_version  INT NOT NULL CHECK (schema_version = 1),
    owner_id        UUID NOT NULL,
    payload_hash    VARCHAR(64) NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'RECEIVED' CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD')),
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ
);
