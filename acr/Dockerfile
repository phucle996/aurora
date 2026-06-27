# ============================================================================
# 🏗️ Builder
# ============================================================================
FROM rust:1.90-slim-bookworm AS builder

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    pkg-config \
    libssl-dev \
    protobuf-compiler && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY Cargo.toml Cargo.lock* ./

RUN mkdir src && \
    echo "fn main() {}" > src/main.rs && \
    cargo build --release || true && \
    rm -rf src

COPY . .

RUN cargo build --release

# ============================================================================
# 🚀 Runtime (Distroless)
# ============================================================================
FROM gcr.io/distroless/cc-debian12

COPY --from=builder \
    /app/target/release/acr \
    /acr

USER 65532:65532

EXPOSE 50051

ENTRYPOINT ["/acr"]