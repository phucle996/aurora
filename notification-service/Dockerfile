# ============================================================================
# 🦀 RUST SERVICE (DISTROLESS - NO SHELL RUNTIME)
# ============================================================================

# ----------------------------------------------------------------------------
# Base builder (có shell để build)
# ----------------------------------------------------------------------------
FROM rust:1.90-slim-bookworm AS chef

WORKDIR /workspace

RUN cargo install --locked cargo-chef

# ----------------------------------------------------------------------------
# Planner
# ----------------------------------------------------------------------------
FROM chef AS planner

COPY . .

RUN cargo chef prepare --recipe-path recipe.json

# ----------------------------------------------------------------------------
# Builder
# ----------------------------------------------------------------------------
FROM chef AS builder

RUN apt-get update && \
    apt-get install -y --no-install-recommends protobuf-compiler && \
    rm -rf /var/lib/apt/lists/*

COPY --from=planner /workspace/recipe.json recipe.json

RUN cargo chef cook --release --recipe-path recipe.json

COPY . .

RUN cargo build --release

# ----------------------------------------------------------------------------
# Runtime (NO SHELL - DISTROLESS)
# ----------------------------------------------------------------------------
FROM gcr.io/distroless/cc-debian12

COPY --from=builder \
    /workspace/target/release/notification-service \
    /notification-service

EXPOSE 8083

ENTRYPOINT ["/notification-service"]