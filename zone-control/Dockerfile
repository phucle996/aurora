# syntax=docker/dockerfile:1.7
FROM rust:1.97-slim-bookworm AS builder
WORKDIR /app
# Zone Control pulls the AWS SDK and the full protobuf/runtime graph. Keep
# release builds bounded on GitHub's shared builders so rustc does not exhaust
# the builder while compiling independent crates in parallel.
ENV CARGO_BUILD_JOBS=2
COPY proto /app/proto
COPY zone-control /app/zone-control
WORKDIR /app/zone-control
RUN cargo build --release --locked

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/zone-control/target/release/zone-control /usr/local/bin/zone-control
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zone-control"]
