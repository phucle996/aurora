# syntax=docker/dockerfile:1.7
FROM rust:1.90-slim-bookworm AS builder
WORKDIR /app
COPY proto /app/proto
COPY zone-control-edge-gateway/authorizer/Cargo.toml zone-control-edge-gateway/authorizer/Cargo.lock /app/zone-control-edge-gateway/authorizer/
COPY zone-control-edge-gateway/authorizer/build.rs /app/zone-control-edge-gateway/authorizer/build.rs
COPY zone-control-edge-gateway/authorizer/src /app/zone-control-edge-gateway/authorizer/src
WORKDIR /app/zone-control-edge-gateway/authorizer
RUN cargo build --release --locked

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/zone-control-edge-gateway/authorizer/target/release/zone-control-authorizer /usr/local/bin/zone-control-authorizer
USER 65532:65532
EXPOSE 50052
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/zone-control-authorizer"]
