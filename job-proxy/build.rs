fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch file proto job_event.proto và mail_job.proto cục bộ
    prost_build::compile_protos(
        &[
            "proto/job_event.proto",
            "proto/mail_job.proto"
        ],
        &["proto/"],
    )?;

    // Biên dịch gRPC client cho backpressure.proto
    tonic_build::configure()
        .build_server(false)
        .build_client(true)
        .compile(&["proto/backpressure.proto"], &["proto/"])?;

    Ok(())
}
