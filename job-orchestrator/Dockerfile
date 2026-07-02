# syntax=docker/dockerfile:1.7

# syntax=docker/dockerfile:1.7

# ============================================================================
# Stage 1: Builder - Sửa lỗi Read-only bằng cách tách biệt thư mục mount
# ============================================================================
FROM rust:bookworm AS builder
WORKDIR /workspace

# Cài đặt protobuf-compiler
RUN apt-get update && apt-get install -y protobuf-compiler && rm -rf /var/lib/apt/lists/*

# 1. Bind code vào thư mục `/src` (Read-only)
# 2. Định nghĩa thư mục làm việc hiện tại là `/workspace`
# 3. Trỏ CARGO_TARGET_DIR ra một thư mục độc lập khác ngoài `/src` để ghi cache tự do
RUN --mount=type=cache,target=/usr/local/cargo/registry/ \
    --mount=type=cache,target=/workspace/target/ \
    --mount=type=bind,source=.,target=/src \
    export CARGO_TARGET_DIR=/workspace/target && \
    cargo build --manifest-path /src/Cargo.toml --release --bin aurora-job-orchestrator && \
    cp /workspace/target/release/aurora-job-orchestrator /usr/local/bin/aurora-job-orchestrator

# ============================================================================
# Stage 2: Runtime - debian slim tối giản, non-root user
# ============================================================================
FROM debian:bookworm-slim AS runtime
ARG LOCAL_USER=phucle
ARG LOCAL_UID=1000
ARG LOCAL_GID=1000

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata libssl-dev \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid ${LOCAL_GID} ${LOCAL_USER} \
    && useradd --uid ${LOCAL_UID} --gid ${LOCAL_GID} --home-dir /var/lib/job-proxy --shell /bin/bash ${LOCAL_USER} \
    && mkdir -p /var/lib/job-proxy /var/log/job-proxy /etc/job-proxy \
    && chown -R ${LOCAL_USER}:${LOCAL_USER} /var/lib/job-proxy /var/log/job-proxy /etc/job-proxy

# Lấy binary từ đường dẫn ngoài đã copy ở stage trước
COPY --from=builder /usr/local/bin/aurora-job-orchestrator /usr/local/bin/aurora-job-orchestrator

WORKDIR /var/lib/job-proxy
USER ${LOCAL_USER}:${LOCAL_USER}

ENTRYPOINT ["/usr/local/bin/aurora-job-orchestrator"]