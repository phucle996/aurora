use crate::infra::centrifugo::CentrifugoClient;
use crate::observability::logger::Logger;

pub struct NotifierService {
    centrifugo: CentrifugoClient,
}

impl NotifierService {
    pub fn new(centrifugo: CentrifugoClient) -> Self {
        // [ignoring loop detection]
        Self { centrifugo }
    }

    pub async fn handle_job_result(&self, _job_id: &str, _user_id: &str, _payload: serde_json::Value) {
        Logger::sys_info("notifier.handle_result", "Handling job execution result event...");
        
        // TODO: Định dạng thông tin kết quả và gửi thông báo tới kênh cá nhân
    }
}
