# syntax=docker/dockerfile:1.7

# ============================================================================
# 🦀 STAGE 1: RUST BUILDER (CARGO CHEF CACHING)
# ============================================================================
# [COMMENT]: Môi trường builder sử dụng cargo-chef để tối ưu layer caching trong Docker
FROM rust:1.90-slim-bookworm AS chef
WORKDIR /workspace
RUN cargo install --locked cargo-chef

# ----------------------------------------------------------------------------
# Planner Stage: Phân tích file recipe.json của dự án
# ----------------------------------------------------------------------------
FROM chef AS planner
WORKDIR /workspace/notification-service
COPY notification-service/ .
RUN cargo chef prepare --recipe-path recipe.json

# ----------------------------------------------------------------------------
# Builder Stage: Biên dịch dependencies và bản release thực sự
# ----------------------------------------------------------------------------
FROM chef AS builder
RUN apt-get update && \
    apt-get install -y --no-install-recommends protobuf-compiler && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /workspace/notification-service
COPY --from=planner /workspace/notification-service/recipe.json recipe.json
RUN cargo chef cook --release --recipe-path recipe.json

COPY notification-service/ ./
COPY proto/ /workspace/proto/
RUN cargo build --release

# ============================================================================
# 🚀 STAGE 2: RUNNER - DISTROLESS CC DEBIAN 12 (NON-ROOT)
# ============================================================================
# [COMMENT]: Sử dụng Google Distroless CC Debian 12 Non-root
# Không chứa Shell hay OS utilities, đảm bảo an toàn tuyệt đối cho dịch vụ thông báo Realtime
FROM gcr.io/distroless/cc-debian12:nonroot AS runtime

# [COMMENT]: Sao chép release binary notification-service sang runtime distroless
COPY --from=builder /workspace/notification-service/target/release/notification-service /usr/local/bin/notification-service

# [COMMENT]: Thiết lập phân quyền chạy dưới user nonroot (UID 65532:65532)
USER 65532:65532

# [COMMENT]: Expose port HTTP Service (8083)
EXPOSE 8083

# [COMMENT]: Khởi chạy trực tiếp binary
ENTRYPOINT ["/usr/local/bin/notification-service"]