# ============================================================================
# 🐳 MULTI-STAGE DOCKERFILE FOR RUST NOTIFICATION SERVICE (OPTIMIZED)
# ============================================================================

# Stage 1: Chuẩn bị recipe
FROM rust:slim-bookworm AS planner
WORKDIR /workspace
RUN cargo install cargo-chef
COPY . .
RUN cargo chef prepare --recipe-path recipe.json

# Stage 2: Caching & Build các thư viện (Dependencies)
FROM rust:slim-bookworm AS builder
WORKDIR /workspace
RUN cargo install cargo-chef
RUN apt-get update && apt-get install -y protobuf-compiler && rm -rf /var/lib/apt/lists/*

# Sao chép recipe từ Stage 1 và build trước các dependencies
COPY --from=planner /workspace/recipe.json recipe.json
RUN cargo chef cook --release --recipe-path recipe.json

# Stage 3: Build Source Code chính
COPY . .
RUN cargo build --release

# Stage 4: Runtime image tối giản
FROM debian:bookworm-slim
WORKDIR /app
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

COPY --from=builder /workspace/target/release/notification-service /app/notification-service

EXPOSE 8083
CMD ["/app/notification-service"]