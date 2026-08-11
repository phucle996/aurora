# syntax=docker/dockerfile:1.7
FROM rust:1.90-slim-bookworm AS builder
WORKDIR /app
COPY proto /app/proto
COPY zone-control /app/zone-control
WORKDIR /app/zone-control
RUN cargo build --release --locked

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/zone-control/target/release/zone-control /usr/local/bin/zone-control
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zone-control"]
