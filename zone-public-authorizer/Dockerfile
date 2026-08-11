# syntax=docker/dockerfile:1.7
FROM rust:1.90-slim-bookworm AS builder
WORKDIR /app
COPY zone-transfer-contract /app/zone-transfer-contract
COPY zone-public-authorizer /app/zone-public-authorizer
WORKDIR /app/zone-public-authorizer
RUN cargo build --release --locked

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/zone-public-authorizer/target/release/zone-public-authorizer /usr/local/bin/zone-public-authorizer
USER 65532:65532
EXPOSE 50053
ENTRYPOINT ["/usr/local/bin/zone-public-authorizer"]
