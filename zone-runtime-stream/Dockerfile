# syntax=docker/dockerfile:1.7

FROM rust:1.90-slim-bookworm AS builder
WORKDIR /app
COPY Cargo.toml Cargo.lock ./
COPY src ./src
RUN cargo build --release --locked

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/target/release/zone-runtime-stream /usr/local/bin/zone-runtime-stream
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zone-runtime-stream"]

