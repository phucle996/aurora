# syntax=docker/dockerfile:1.7
FROM rust:1.90-slim-bookworm AS builder
WORKDIR /app
COPY zone-transfer-contract /app/zone-transfer-contract
COPY zone-transfer-ticket-issuer /app/zone-transfer-ticket-issuer
WORKDIR /app/zone-transfer-ticket-issuer
RUN cargo build --release --locked

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/zone-transfer-ticket-issuer/target/release/zone-transfer-ticket-issuer /usr/local/bin/zone-transfer-ticket-issuer
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zone-transfer-ticket-issuer"]
