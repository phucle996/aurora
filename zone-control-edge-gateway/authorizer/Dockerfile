# syntax=docker/dockerfile:1.7
FROM rust:1.90-slim-bookworm AS builder
WORKDIR /app
COPY Cargo.toml Cargo.lock ./
COPY src ./src
RUN cargo build --release --locked

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/target/release/zone-control-authorizer /usr/local/bin/zone-control-authorizer
USER 65532:65532
EXPOSE 50052
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/zone-control-authorizer"]
