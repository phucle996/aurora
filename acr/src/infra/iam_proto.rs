// [COMMENT]: Central IAM wire contracts dùng chung cho Shared Redis request/reply.
// Tên module không gắn transport để protobuf không bị hiểu nhầm là contract NATS.
#[allow(dead_code)]
#[allow(unused_imports)]
pub mod auth {
    tonic::include_proto!("iam.rpc");
}

#[allow(dead_code)]
#[allow(unused_imports)]
pub mod trinity {
    tonic::include_proto!("trinity.rpc");
}
