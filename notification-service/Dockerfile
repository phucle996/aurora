# ============================================================================
# 🐳 MULTI-STAGE DOCKERFILE FOR RUST NOTIFICATION SERVICE
# ============================================================================

FROM rust:1.85-slim-bookworm AS builder
WORKDIR /workspace

# Cài đặt protobuf-compiler cần thiết cho việc compile gRPC
RUN apt-get update && apt-get install -y protobuf-compiler && rm -rf /var/lib/apt/lists/*

COPY . .
RUN cargo build --release

# Runtime image tối giản chạy binary tĩnh
FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

COPY --from=builder /workspace/target/release/notification-service /app/notification-service

EXPOSE 8083

CMD ["/app/notification-service"]
