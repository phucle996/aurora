pub mod admin;
pub mod user;

pub mod trinity {
    tonic::include_proto!("trinity.rpc");
}
