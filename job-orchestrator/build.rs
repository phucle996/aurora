fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch các file proto job_event.proto, mail_job.proto và job_result.proto cục bộ
    prost_build::compile_protos(
        &[
            "proto/job_event.proto",
            "proto/mail_job.proto",
            "proto/job_result.proto",
            "proto/zone_report.proto"
        ],
        &["proto/"],
    )?;



    Ok(())
}
