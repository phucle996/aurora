fn main() -> Result<(), Box<dyn std::error::Error>> {
    tonic_build::configure().compile_protos(
        &[
            "../proto/iam/trinity/v1/token_verification.proto",
            "../proto/notification/job/v1/job_notification.proto",
            "../proto/notification/activity/v1/user_activity.proto",
        ],
        &["../proto"],
    )?;
    Ok(())
}
