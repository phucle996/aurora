// Sinh mã Rust từ gRPC protobuf definitions
pub mod auth {
    tonic::include_proto!("auth");
}

use tonic::transport::{Channel, Endpoint};
use auth::auth_service_client::AuthServiceClient;
use auth::{
    VerifyAdminTrinityTokenRequest, VerifyAdminTrinityTokenResponse,
    VerifyUserTrinityTokenRequest, VerifyUserTrinityTokenResponse,
};
use crate::observability::logger::Logger;

#[derive(Clone)]
pub struct GrpcAuthClient {
    client: AuthServiceClient<Channel>,
}

impl GrpcAuthClient {
    pub fn new(endpoint: String) -> Self {
        // [ignoring loop detection]
        let url = format!("http://{}", endpoint);
        
        // Thiết lập endpoint với lazy connection, keep-alive và timeout để chống treo kết nối
        let endpoint_configured = Endpoint::from_shared(url)
            .expect("Invalid Controlplane gRPC endpoint URI")
            .connect_timeout(std::time::Duration::from_secs(5))
            .timeout(std::time::Duration::from_secs(5))
            .tcp_keepalive(Some(std::time::Duration::from_secs(15)));

        // Khởi tạo kênh kết nối lazy (không block startup nếu Controlplane chưa sẵn sàng)
        let channel = endpoint_configured.connect_lazy();
        let client = AuthServiceClient::new(channel);

        Self { client }
    }

    // Gửi yêu cầu xác thực Admin qua gRPC đến Controlplane
    pub async fn verify_admin_trinity_token(
        &self,
        admin_api_token: String,
        access_key: String,
        access_secret: String,
    ) -> Result<VerifyAdminTrinityTokenResponse, tonic::Status> {
        Logger::sys_info(
            "grpc.auth_call",
            "Verifying admin trinity token via lazy-connected gRPC Channel pool",
        );

        // Client của tonic rẻ để clone (chỉ clone underlying channel reference)
        let mut client = self.client.clone();

        let request = tonic::Request::new(VerifyAdminTrinityTokenRequest {
            admin_api_token,
            access_key,
            access_secret,
        });

        let response = client.verify_admin_trinity_token(request).await?;
        Ok(response.into_inner())
    }

    // Gửi yêu cầu xác thực User qua gRPC đến Controlplane
    pub async fn verify_user_trinity_token(
        &self,
        access_token: String,
        access_key: String,
        access_secret: String,
    ) -> Result<VerifyUserTrinityTokenResponse, tonic::Status> {
        Logger::sys_info(
            "grpc.auth_call",
            "Verifying user trinity token via lazy-connected gRPC Channel pool",
        );

        // Client của tonic rẻ để clone (chỉ clone underlying channel reference)
        let mut client = self.client.clone();

        let request = tonic::Request::new(VerifyUserTrinityTokenRequest {
            access_token,
            access_key,
            access_secret,
        });

        let response = client.verify_user_trinity_token(request).await?;
        Ok(response.into_inner())
    }
}
