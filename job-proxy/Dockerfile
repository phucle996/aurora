# syntax=docker/dockerfile:1.7

# ============================================================================
# Stage 1: Builder - biên dịch binary release của aurora-job-proxy
# ============================================================================
FROM rust:bookworm AS builder
RUN apt-get update && apt-get install -y protobuf-compiler && rm -rf /var/lib/apt/lists/*
WORKDIR /src

# 1. Pre-warm dependency cache: chỉ copy manifest, build dummy để cache layer deps.
COPY Cargo.toml Cargo.lock* ./
RUN mkdir -p src \
    && echo "fn main() {}" > src/main.rs \
    && cargo build --release --bin aurora-job-proxy \
    && rm -rf src \
       target/release/aurora-job-proxy \
       target/release/aurora-job-proxy.d \
       target/release/deps/aurora_job_proxy* \
       target/release/.fingerprint/aurora-job-proxy-*

# 2. Copy toàn bộ source thật.
COPY . .

# 3. Force-touch các file .rs để cargo nhận thay đổi và rebuild crate chính.
RUN find src -name "*.rs" -exec touch {} + \
    && cargo build --release --bin aurora-job-proxy \
    && test -s target/release/aurora-job-proxy

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

COPY --from=builder /src/target/release/aurora-job-proxy /usr/local/bin/aurora-job-proxy

WORKDIR /var/lib/job-proxy
USER ${LOCAL_USER}:${LOCAL_USER}

ENTRYPOINT ["/usr/local/bin/aurora-job-proxy"]
