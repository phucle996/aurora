# syntax=docker/dockerfile:1.7

# ============================================================================
# 🏗️ STAGE 1: BUILDER - BIÊN DỊCH BẢN RUST ACR IDENTITY & SECURITY SERVICE
# ============================================================================
# [COMMENT]: Môi trường biên dịch Rust 1.90 Bookworm với đầy đủ công cụ ssl và protobuf
FROM rust:1.90-slim-bookworm AS builder

# [COMMENT]: Cài đặt pkg-config, libssl-dev và protobuf-compiler cho ACR Auth Engine
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    pkg-config \
    libssl-dev \
    protobuf-compiler && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app/acr

# [COMMENT]: Cache dependencies layer trước khi compile source chính
COPY acr/Cargo.toml acr/Cargo.lock* ./
RUN mkdir src && \
    echo "fn main() {}" > src/main.rs && \
    cargo build --release || true && \
    rm -rf src

# build.rs resolves the canonical IAM proto from the root proto registry. Keep
# that sibling layout inside the builder rather than duplicating contracts.
COPY acr/ ./
COPY proto/ /app/proto/
RUN cargo build --release

# ============================================================================
# 🚀 STAGE 2: RUNNER - DISTROLESS CC DEBIAN 12 (NON-ROOT)
# ============================================================================
# [COMMENT]: Chuyển sang Google Distroless CC Debian 12 Non-root
# Bảo đảm an ninh tối đa cho dịch vụ Quản lý Khóa & Xác thực ACR (Zero Shell Access)
FROM gcr.io/distroless/cc-debian12:nonroot AS runtime

# [COMMENT]: Sao chép binary ACR từ Stage Builder
COPY --from=builder /app/acr/target/release/acr /usr/local/bin/acr

# [COMMENT]: Chạy ứng dụng dưới user nonroot (UID 65532:65532)
USER 65532:65532

# [COMMENT]: Expose cổng gRPC 50051 của ACR
EXPOSE 50051

# [COMMENT]: Khởi chạy trực tiếp binary ứng dụng
ENTRYPOINT ["/usr/local/bin/acr"]
