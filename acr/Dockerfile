# ═══════════════════════════════════════════════════════════════
# 🏗️ STAGE 1: Builder — Biên dịch ACL Service từ source
# ═══════════════════════════════════════════════════════════════
FROM rust:1.82-slim-bookworm AS builder

# Cài đặt dependencies hệ thống cần thiết cho protobuf và TLS
RUN apt-get update && apt-get install -y \
    pkg-config \
    libssl-dev \
    protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy manifest trước để tận dụng Docker layer cache cho dependencies
COPY Cargo.toml Cargo.lock* ./

# Tạo dummy main.rs để cache compile dependencies layer
RUN mkdir src && \
    echo "fn main() {}" > src/main.rs && \
    cargo build --release 2>/dev/null || true && \
    rm -rf src

# Copy toàn bộ source code thực tế
COPY src/ src/
# [COMMENT]: Copy proto definitions và build script cần thiết cho tonic_build
COPY proto/ proto/
COPY build.rs build.rs

# Biên dịch release binary với tối ưu hóa tối đa
RUN cargo build --release


# ═══════════════════════════════════════════════════════════════
# 🚀 STAGE 2: Runtime — Container tối thiểu chạy production
# ═══════════════════════════════════════════════════════════════
FROM debian:bookworm-slim AS runtime

# Cài đặt runtime dependencies (TLS certificates, ca-certificates cho gRPC)
RUN apt-get update && apt-get install -y \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Tạo user không quyền root để chạy service (Nguyên tắc Least Privilege)
RUN groupadd -r acl && useradd -r -g acl -s /bin/false acl

WORKDIR /app

# Copy binary đã biên dịch từ builder stage
COPY --from=builder /app/target/release/acl /app/acl

# Chuyển sang user không quyền root
USER acl

# Expose port mặc định cho gRPC server
EXPOSE 50051

# Health check đơn giản kiểm tra process còn sống
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD pgrep acl || exit 1

# Entrypoint chạy ACL Service
ENTRYPOINT ["/app/acl"]
