mod image;
mod vm;

pub struct VmResultRequest<'a> {
    pub job_id: uuid::Uuid,
    pub job_topic: &'a str,
    pub status: &'a str,
    pub error_code: Option<&'a str>,
    pub error_message: Option<&'a str>,
    pub result_payload: &'a [u8],
    pub result_payload_schema_version: u32,
}

pub struct ImageResultRequest<'a> {
    pub job_id: uuid::Uuid,
    pub job_topic: &'a str,
    pub status: &'a str,
    pub error_code: Option<&'a str>,
    pub error_message: Option<&'a str>,
    pub result_payload: &'a [u8],
    pub result_payload_schema_version: u32,
}

pub use image::apply_image_result;
pub use vm::{apply_vm_create_result, apply_vm_delete_result};
