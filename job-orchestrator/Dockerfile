# syntax=docker/dockerfile:1.7

# ============================================================================
# 🏗️ STAGE 1: BUILDER - BIÊN DỊCH BẢN RUST AURORA-JOB-ORCHESTRATOR
# ============================================================================
# [COMMENT]: Build context phải là monorepo root để build.rs đọc canonical
# contracts/proto mà không cần service-local proto copy.
# [COMMENT]: Môi trường biên dịch Rust chính thức dựa trên Debian Bookworm
FROM rust:bookworm AS builder
WORKDIR /workspace

# [COMMENT]: Cài đặt protobuf-compiler để biên dịch schema mã hóa thông điệp CDC & Job Result
RUN apt-get update && \
    apt-get install -y --no-install-recommends protobuf-compiler && \
    rm -rf /var/lib/apt/lists/*

# [COMMENT]: Sử dụng BuildKit cache mount để tách biệt CARGO_TARGET_DIR và tăng tốc thời gian biên dịch
RUN --mount=type=cache,target=/usr/local/cargo/registry/ \
    --mount=type=cache,target=/workspace/target/ \
    --mount=type=bind,source=.,target=/src \
    export CARGO_TARGET_DIR=/workspace/target && \
    cargo build --manifest-path /src/job-orchestrator/Cargo.toml --release --bin aurora-job-orchestrator && \
    cp /workspace/target/release/aurora-job-orchestrator /usr/local/bin/aurora-job-orchestrator

# ============================================================================
# 🚀 STAGE 2: RUNNER - CONTAINER DISTROLESS CC DEBIAN 12 (NON-ROOT)
# ============================================================================
# [COMMENT]: Chuyển sang Google Distroless CC Debian 12 Non-root
# Image tối giản chỉ chứa glibc runtime, libssl và CA certs, không chứa bất kỳ Shell hoặc OS utilities nào.
FROM gcr.io/distroless/cc-debian12:nonroot AS runtime

# [COMMENT]: Sao chép binary aurora-job-orchestrator đã biên dịch từ Stage 1
COPY --from=builder /usr/local/bin/aurora-job-orchestrator /usr/local/bin/aurora-job-orchestrator

# [COMMENT]: Thiết lập thư mục làm việc tiêu chuẩn cho Job Orchestrator
WORKDIR /var/lib/job-proxy

# [COMMENT]: Chạy ứng dụng bằng user phi quản trị nonroot (UID 65532:65532)
USER 65532:65532

# [COMMENT]: Khởi chạy trực tiếp binary ứng dụng
ENTRYPOINT ["/usr/local/bin/aurora-job-orchestrator"]
